package importliststatus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/providerstatus"
)

// StatusChangedEvent is the per-package
// `ProviderStatusChangedEvent<ImportListDefinition>` analogue.
// The full Status row (including the import-list-only
// LastInfoSync / HasRemovedItemSinceLastClean fields) is
// embedded so subscribers can route on either dimension.
type StatusChangedEvent struct {
	ProviderID int
	Status     Status
}

// Service ports the C# `ImportListStatusService` (a 4-line
// subclass of `ProviderStatusServiceBase<ImportListDefinition,
// ImportListStatus>` at
// `ImportLists/ImportListStatusService.cs`). Same shape as
// notificationstatus.Service; the only differences are the typed
// Repo + StatusChangedEvent and the carry-over of LastInfoSync /
// HasRemovedItemSinceLastClean (neither participates in the
// back-off logic — they're set by the import-list runner and
// merely round-trip through Upsert).
type Service struct {
	repo *Repo
	bus  *messaging.Bus
	now  func() time.Time
	tun  providerstatus.Tunables

	mu sync.Mutex
}

// NewService binds a Repo + Bus + RuntimeStart into a Service.
func NewService(r *Repo, bus *messaging.Bus, runtimeStart time.Time) (*Service, error) {
	if r == nil {
		return nil, errors.New("importliststatus: nil repo")
	}
	return &Service{
		repo: r,
		bus:  bus,
		now:  func() time.Time { return time.Now().UTC() },
		tun:  providerstatus.DefaultTunables(runtimeStart),
	}, nil
}

// SetClock overrides the now-function for deterministic tests.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SetTunables overrides the per-service Tunables.
func (s *Service) SetTunables(t providerstatus.Tunables) { s.tun = t }

// GetBlockedProviders returns the subset of statuses still inside
// their DisabledTill window at the current clock.
func (s *Service) GetBlockedProviders(ctx context.Context) ([]Status, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("importliststatus: get blocked: %w", err)
	}
	now := s.now()
	out := make([]Status, 0, len(all))
	for _, st := range all {
		row := st
		if row.IsDisabled(now) {
			out = append(out, row)
		}
	}
	return out, nil
}

// RecordSuccess decrements EscalationLevel + clears DisabledTill.
func (s *Service) RecordSuccess(ctx context.Context, providerID int) error {
	if providerID <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ctx, providerID, providerstatus.ApplySuccess)
}

// RecordFailure escalates and sets DisabledTill, optionally seeking
// EscalationLevel up to a tier whose back-off >= minimumBackOff.
func (s *Service) RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error {
	if providerID <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ctx, providerID, func(state *providerstatus.State) bool {
		providerstatus.ApplyFailure(state, s.now(), minimumBackOff, true, s.tun)
		return true
	})
}

// RecordConnectionFailure is RecordFailure with escalate=false.
func (s *Service) RecordConnectionFailure(ctx context.Context, providerID int) error {
	if providerID <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ctx, providerID, func(state *providerstatus.State) bool {
		providerstatus.ApplyFailure(state, s.now(), 0, false, s.tun)
		return true
	})
}

// OnProviderDeleted removes the status row for the given provider.
func (s *Service) OnProviderDeleted(ctx context.Context, providerID int) error {
	if err := s.repo.DeleteByProviderID(ctx, providerID); err != nil {
		return fmt.Errorf("importliststatus: on-deleted: %w", err)
	}
	return nil
}

func (s *Service) applyLocked(ctx context.Context, providerID int, mutate func(*providerstatus.State) bool) error {
	row, err := s.repo.GetByProviderID(ctx, providerID)
	switch {
	case errors.Is(err, ErrNotFound):
		row = &Status{ProviderID: providerID}
	case err != nil:
		return fmt.Errorf("importliststatus: load(%d): %w", providerID, err)
	}
	state := providerstatus.State{
		InitialFailure:    row.InitialFailure,
		MostRecentFailure: row.MostRecentFailure,
		DisabledTill:      row.DisabledTill,
		EscalationLevel:   row.EscalationLevel,
	}
	if !mutate(&state) {
		return nil
	}
	row.InitialFailure = state.InitialFailure
	row.MostRecentFailure = state.MostRecentFailure
	row.DisabledTill = state.DisabledTill
	row.EscalationLevel = state.EscalationLevel
	if err := s.repo.Upsert(ctx, row); err != nil {
		return fmt.Errorf("importliststatus: upsert(%d): %w", providerID, err)
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, StatusChangedEvent{ProviderID: providerID, Status: *row})
	}
	return nil
}
