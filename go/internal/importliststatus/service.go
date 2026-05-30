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

// NewService binds a Repo + Bus + RuntimeStart into a Service. The
// runtime-start time is required and cannot be changed after
// construction (matches the C# `IRuntimeInfo.StartTime` immutable).
// Bus may be nil to skip event publication.
//
// Unlike notificationstatus.NewService / downloadclientstatus.NewService
// — which apply `MinimumTimeSinceInitialFailure = 5min` +
// `MaximumEscalationLevel = 5` per `NotificationStatusService.cs:18-19`
// / `DownloadClientStatusService.cs:18-19` — the C# class
// `ImportListStatusService` at `ImportLists/ImportListStatusService.cs:18-58`
// has NO constructor body overrides. It only adds three semantic methods
// (`GetListStatus`, `UpdateListSyncStatus`, `MarkListsAsCleaned`) over the
// shared ProviderStatusServiceBase. The Go port therefore uses
// [providerstatus.DefaultTunables] verbatim — back-off envelope is the
// shared default of `MinTimeSinceInitialFailure = 0` +
// `MaxEscalationLevel = len(BackOffPeriods)-1 = 9` (24-hour ceiling).
// Callers that want to tweak per-test can override via [Service.SetTunables].
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

// GetListStatus ports C# `ImportListStatusService.GetListStatus` at
// `ImportLists/ImportListStatusService.cs:25-28` (→ GetProviderStatus).
// Returns the persisted status row, or a zero-valued status (with
// ProviderID set) when none exists yet — matching the C# base which
// never returns null.
func (s *Service) GetListStatus(ctx context.Context, importListID int) (*Status, error) {
	row, err := s.repo.GetByProviderID(ctx, importListID)
	switch {
	case errors.Is(err, ErrNotFound):
		return &Status{ProviderID: importListID}, nil
	case err != nil:
		return nil, fmt.Errorf("importliststatus: getListStatus(%d): %w", importListID, err)
	}
	return row, nil
}

// UpdateListSyncStatus ports C#
// `ImportListStatusService.UpdateListSyncStatus` at `:30-41`. Records
// that the list completed a sync (LastInfoSync = now) and
// OR-accumulates whether that sync removed any items
// (HasRemovedItemSinceLastClean) — the flag the clean-library trigger
// later consults via [AllListsSuccessfulWithAPendingClean]. The other
// status fields (back-off state) round-trip untouched.
func (s *Service) UpdateListSyncStatus(ctx context.Context, importListID int, removedItems bool) error {
	if importListID <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.repo.GetByProviderID(ctx, importListID)
	switch {
	case errors.Is(err, ErrNotFound):
		row = &Status{ProviderID: importListID}
	case err != nil:
		return fmt.Errorf("importliststatus: updateListSyncStatus load(%d): %w", importListID, err)
	}
	now := s.now()
	row.LastInfoSync = &now
	row.HasRemovedItemSinceLastClean = row.HasRemovedItemSinceLastClean || removedItems
	if err := s.repo.Upsert(ctx, row); err != nil {
		return fmt.Errorf("importliststatus: updateListSyncStatus upsert(%d): %w", importListID, err)
	}
	return nil
}

// MarkListsAsCleaned ports C#
// `ImportListStatusService.MarkListsAsCleaned` at `:43-57`. Clears
// HasRemovedItemSinceLastClean on every status row — called by
// CleanLibrary after it has processed a pending clean. Rows already
// clear are skipped (avoids a redundant write; observable state is
// identical to the C# blanket UpdateMany).
func (s *Service) MarkListsAsCleaned(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.repo.All(ctx)
	if err != nil {
		return fmt.Errorf("importliststatus: markListsAsCleaned load: %w", err)
	}
	for i := range all {
		if !all[i].HasRemovedItemSinceLastClean {
			continue
		}
		row := all[i]
		row.HasRemovedItemSinceLastClean = false
		if err := s.repo.Upsert(ctx, &row); err != nil {
			return fmt.Errorf("importliststatus: markListsAsCleaned upsert(%d): %w", row.ProviderID, err)
		}
	}
	return nil
}

// AllListsSuccessfulWithAPendingClean ports C#
// `ImportListSyncService.AllListsSuccessfulWithAPendingClean` at
// `ImportListSyncService.cs:58-83`. The caller passes the ids of the
// auto-add-enabled lists (C# `_importListFactory.AutomaticAddEnabled(
// false)`). Returns true only when EVERY such list has synced at least
// once (LastInfoSync set) AND none currently sits in a back-off window
// (DisabledTill set — i.e. its last sync failed and hasn't recovered)
// AND at least one list has removed an item since the last clean. That
// last condition is the "pending clean" — there's nothing to clean up
// unless a list dropped a series.
func (s *Service) AllListsSuccessfulWithAPendingClean(ctx context.Context, listIDs []int) (bool, error) {
	anyRemoved := false
	for _, id := range listIDs {
		st, err := s.GetListStatus(ctx, id)
		if err != nil {
			return false, err
		}
		if st.DisabledTill != nil {
			// list failed the last time it was synced.
			return false, nil
		}
		if st.LastInfoSync == nil {
			// list has never been synced.
			return false, nil
		}
		anyRemoved = anyRemoved || st.HasRemovedItemSinceLastClean
	}
	return anyRemoved, nil
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
