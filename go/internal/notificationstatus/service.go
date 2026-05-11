package notificationstatus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/providerstatus"
)

// StatusChangedEvent mirrors C#
// `ProviderStatusChangedEvent<TProvider>` at
// `ThingiProvider/Events/ProviderStatusChangedEvent.cs`. Published
// after every RecordSuccess that mutated the row (i.e. EscalationLevel
// was non-zero), and after every RecordFailure /
// RecordConnectionFailure (which always mutate). Per-package event
// type because downstream subscribers care about which provider
// kind changed.
type StatusChangedEvent struct {
	ProviderID int
	Status     Status
}

// Service ports `ProviderStatusServiceBase<NotificationDefinition,
// NotificationStatus>` (the typed derivative for the Notifications
// providers — the C# class
// `Notifications/NotificationStatusService.cs` is a 4-line subclass
// that just wires its base class through).
//
// The shared decision matrix lives in [providerstatus.ApplyFailure]
// and [providerstatus.ApplySuccess]; this Service threads them
// through the per-type Repo + event publication.
//
// Concurrency: the C# class uses `lock (_syncRoot)` around the
// FindByProviderId / Upsert pair to keep two callers from
// double-incrementing or racing on the unique-on-ProviderId
// constraint. The Go port uses a `sync.Mutex` with the same scope.
//
// Bus may be nil (tests, migrations). Logging is not threaded
// through — no logger plumbing yet.
type Service struct {
	repo *Repo
	bus  *messaging.Bus
	now  func() time.Time
	tun  providerstatus.Tunables

	mu sync.Mutex
}

// MaxEscalationLevel is the per-type override applied by [NewService].
// Mirrors the C# `NotificationStatusService` constructor override at
// `Notifications/NotificationStatusService.cs:19`
// (`MaximumEscalationLevel = 5`). Cap at level 5 means the longest
// back-off period a failing notification can incur is the
// providerstatus.BackOffPeriods[5] tier (1 hour) — vs the 24-hour
// tier-9 the indexer fleet allows. Matches the download-client family
// cap; the rationale (user-visible failure mode, transient blips
// shouldn't disable for a day) is the same.
const MaxEscalationLevel = 5

// MinTimeSinceInitialFailure is the per-type override applied by
// [NewService]. Mirrors the C# override at
// `Notifications/NotificationStatusService.cs:18`
// (`MinimumTimeSinceInitialFailure = TimeSpan.FromMinutes(5)`). The
// notification family fires user-visible alerts (Discord/Slack/Email/
// etc) on grab + download + import + health events, so a transient
// network blip during an OnGrab webhook POST should not instantly
// disable the provider; the C# code waits 5 minutes after the first
// failure before escalating.
const MinTimeSinceInitialFailure = 5 * time.Minute

// NewService binds a Repo + Bus + RuntimeStart into a Service. The
// runtime-start time is required and cannot be changed after
// construction (matches the C# `IRuntimeInfo.StartTime` immutable).
// Bus may be nil to skip event publication.
//
// Applies the C# `NotificationStatusService` Tunables overrides at
// `Notifications/NotificationStatusService.cs:18-19` — see the
// [MaxEscalationLevel] and [MinTimeSinceInitialFailure] consts above.
// Callers that want to tweak per-test can override via
// [Service.SetTunables].
func NewService(r *Repo, bus *messaging.Bus, runtimeStart time.Time) (*Service, error) {
	if r == nil {
		return nil, errors.New("notificationstatus: nil repo")
	}
	tun := providerstatus.DefaultTunables(runtimeStart)
	tun.MaxEscalationLevel = MaxEscalationLevel
	tun.MinTimeSinceInitialFailure = MinTimeSinceInitialFailure
	return &Service{
		repo: r,
		bus:  bus,
		now:  func() time.Time { return time.Now().UTC() },
		tun:  tun,
	}, nil
}

// SetClock overrides the now-function for deterministic tests.
// Should not be called after Record* in production. Not exported as
// part of the regular surface.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SetTunables overrides the per-service Tunables (matches the C#
// class's `protected` setters at `:31-33` that subclasses use to
// customize). Default is [providerstatus.DefaultTunables].
func (s *Service) SetTunables(t providerstatus.Tunables) { s.tun = t }

// GetBlockedProviders returns the subset of statuses that are still
// inside their DisabledTill window at the current clock. Mirrors C#
// `:43-46`.
func (s *Service) GetBlockedProviders(ctx context.Context) ([]Status, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("notificationstatus: get blocked: %w", err)
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
// the matching row. No-op on a fresh provider (level=0). Mirrors C#
// `:60-83`.
func (s *Service) RecordSuccess(ctx context.Context, providerID int) error {
	if providerID <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ctx, providerID, providerstatus.ApplySuccess)
}

// RecordFailure escalates and sets DisabledTill. Optional minimum
// back-off (TimeSpan.Zero in C#) seeks EscalationLevel up to a tier
// whose back-off >= minimumBackOff. Mirrors C# `:142-145`.
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

// RecordConnectionFailure is RecordFailure with escalate=false —
// connection-level errors don't bump the tier on their own (the
// expectation is the connection will recover). Mirrors C#
// `:147-150`.
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

// OnProviderDeleted is the Go-side translation of C#
// `HandleAsync(ProviderDeletedEvent<TProvider>)` at `:152-155`.
// Callers wire this to whatever notification-deletion event bus
// they're running; the message broker isn't part of this slice.
func (s *Service) OnProviderDeleted(ctx context.Context, providerID int) error {
	if err := s.repo.DeleteByProviderID(ctx, providerID); err != nil {
		return fmt.Errorf("notificationstatus: on-deleted: %w", err)
	}
	return nil
}

// applyLocked loads the existing status by ProviderId (or
// constructs a fresh one), runs `mutate`, and persists + publishes
// when mutate returns true. Caller MUST hold s.mu.
func (s *Service) applyLocked(ctx context.Context, providerID int, mutate func(*providerstatus.State) bool) error {
	row, err := s.repo.GetByProviderID(ctx, providerID)
	switch {
	case errors.Is(err, ErrNotFound):
		row = &Status{ProviderID: providerID}
	case err != nil:
		return fmt.Errorf("notificationstatus: load(%d): %w", providerID, err)
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
		return fmt.Errorf("notificationstatus: upsert(%d): %w", providerID, err)
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, StatusChangedEvent{ProviderID: providerID, Status: *row})
	}
	return nil
}
