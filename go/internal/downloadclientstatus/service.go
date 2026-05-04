package downloadclientstatus

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
// `ProviderStatusChangedEvent<DownloadClientDefinition>` analogue.
// See notificationstatus.StatusChangedEvent for the cross-type
// design notes — this struct is identical in shape, kept per-package
// because subscribers care about which provider kind changed.
type StatusChangedEvent struct {
	ProviderID int
	Status     Status
}

// Service ports the C# `DownloadClientStatusService` (a 4-line
// subclass of `ProviderStatusServiceBase<DownloadClientDefinition,
// DownloadClientStatus>` at
// `Download/DownloadClientStatusService.cs`). The Go shape mirrors
// notificationstatus.Service exactly — only the Repo + event
// types differ.
type Service struct {
	repo *Repo
	bus  *messaging.Bus
	now  func() time.Time
	tun  providerstatus.Tunables

	mu sync.Mutex
}

// NewService binds a Repo + Bus + RuntimeStart into a Service.
// Bus may be nil to skip event publication.
func NewService(r *Repo, bus *messaging.Bus, runtimeStart time.Time) (*Service, error) {
	if r == nil {
		return nil, errors.New("downloadclientstatus: nil repo")
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

// GetBlockedProviders returns the subset of statuses that are still
// inside their DisabledTill window at the current clock.
func (s *Service) GetBlockedProviders(ctx context.Context) ([]Status, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("downloadclientstatus: get blocked: %w", err)
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

// RecordSuccess decrements EscalationLevel + clears DisabledTill on
// the matching row. No-op on a fresh provider.
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
		return fmt.Errorf("downloadclientstatus: on-deleted: %w", err)
	}
	return nil
}

func (s *Service) applyLocked(ctx context.Context, providerID int, mutate func(*providerstatus.State) bool) error {
	row, err := s.repo.GetByProviderID(ctx, providerID)
	switch {
	case errors.Is(err, ErrNotFound):
		row = &Status{ProviderID: providerID}
	case err != nil:
		return fmt.Errorf("downloadclientstatus: load(%d): %w", providerID, err)
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
		return fmt.Errorf("downloadclientstatus: upsert(%d): %w", providerID, err)
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, StatusChangedEvent{ProviderID: providerID, Status: *row})
	}
	return nil
}
