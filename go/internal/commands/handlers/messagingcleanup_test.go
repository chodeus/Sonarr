package handlers_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/messaging"
)

// emptyHouseSvc returns a housekeeping.Service with zero tasks.
// Sufficient for tests that only need RegisterAll to wire the
// Housekeeping handler without invoking it.
func emptyHouseSvc(t *testing.T) *housekeeping.Service {
	t.Helper()
	svc, err := housekeeping.NewService(nil, slog.Default())
	if err != nil {
		t.Fatalf("housekeeping.NewService: %v", err)
	}
	return svc
}

// emptyHealthSvc returns a healthcheck.Service with zero checks.
// Sufficient for tests that only need RegisterAll to wire the
// CheckHealth handler without invoking it. Zero debounce so any
// event-driven enqueues drain synchronously.
func emptyHealthSvc(t *testing.T) *healthcheck.Service {
	t.Helper()
	svc, err := healthcheck.NewService(nil, nil, slog.Default(),
		healthcheck.WithDebounce(0))
	if err != nil {
		t.Fatalf("healthcheck.NewService: %v", err)
	}
	return svc
}

// allDeps builds a Deps wired against the supplied commands.Service
// and empty housekeeping + healthcheck services.
func allDeps(t *testing.T, svc *commands.Service) handlers.Deps {
	t.Helper()
	return handlers.Deps{
		Commands:     svc,
		Housekeeping: emptyHouseSvc(t),
		HealthCheck:  emptyHealthSvc(t),
	}
}

// cleanupHarness wires a real Service + Repo over a fresh test DB so
// tests can both push commands through the public surface AND seed
// terminal-state rows directly through the repo.
type cleanupHarness struct {
	db   *datastore.DB
	repo *commands.Repo
	svc  *commands.Service
	bus  *messaging.Bus
}

func newCleanupHarness(t *testing.T, t0 time.Time) *cleanupHarness {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := commands.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, err := commands.NewService(r, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetClock(func() time.Time { return t0 })
	return &cleanupHarness{db: db, repo: r, svc: svc, bus: bus}
}

// seedTerminal inserts a Completed row with EndedAt at the supplied
// time. The C# repo Trim cutoff compares against EndedAt, so this
// direct-write path is the cleanest way to land rows at specific past
// times (Push + Complete uses Service.now, which is fixed at t0).
func (h *cleanupHarness) seedTerminal(t *testing.T, name string, endedAt time.Time) *commands.Command {
	t.Helper()
	row := &commands.Command{
		Name:     name,
		Body:     "{}",
		Priority: int(commands.PriorityNormal),
		Status:   int(commands.StatusCompleted),
		QueuedAt: endedAt.Add(-time.Minute),
		EndedAt:  &endedAt,
		Trigger:  int(commands.TriggerManual),
		Result:   int(commands.ResultSuccessful),
	}
	if _, err := h.repo.Insert(t.Context(), row); err != nil {
		t.Fatalf("seed Insert(%q): %v", name, err)
	}
	return row
}

func TestMessagingCleanup_Execute_DeletesAgedRows(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newCleanupHarness(t, t0)

	old := h.seedTerminal(t, "Old", t0.Add(-30*time.Hour))
	young := h.seedTerminal(t, "Young", t0.Add(-12*time.Hour))

	hh := handlers.NewMessagingCleanup(h.svc)
	hh.SetClock(func() time.Time { return t0 })
	if err := hh.Execute(t.Context(), "{}"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := h.svc.Get(t.Context(), old.ID); !errors.Is(err, commands.ErrNotFound) {
		t.Errorf("old row: want ErrNotFound, got err=%v", err)
	}
	if _, err := h.svc.Get(t.Context(), young.ID); err != nil {
		t.Errorf("young row: want kept, got err=%v", err)
	}
}

func TestMessagingCleanup_Execute_NoOpOnEmpty(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newCleanupHarness(t, t0)
	hh := handlers.NewMessagingCleanup(h.svc)
	hh.SetClock(func() time.Time { return t0 })
	if err := hh.Execute(t.Context(), "{}"); err != nil {
		t.Errorf("Execute empty: %v", err)
	}
}

func TestMessagingCleanup_Execute_CutoffMatchesC1Day(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newCleanupHarness(t, t0)

	// Boundary: row at exactly -24h should survive (Trim uses strict
	// `<`); row at -24h-1ns should be deleted.
	atBoundary := h.seedTerminal(t, "AtBoundary", t0.Add(-handlers.MessagingCleanupRetention))
	beyond := h.seedTerminal(t, "Beyond", t0.Add(-handlers.MessagingCleanupRetention).Add(-time.Nanosecond))

	hh := handlers.NewMessagingCleanup(h.svc)
	hh.SetClock(func() time.Time { return t0 })
	if err := hh.Execute(t.Context(), "{}"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := h.svc.Get(t.Context(), atBoundary.ID); err != nil {
		t.Errorf("atBoundary row: want kept, got err=%v", err)
	}
	if _, err := h.svc.Get(t.Context(), beyond.ID); !errors.Is(err, commands.ErrNotFound) {
		t.Errorf("beyond row: want ErrNotFound, got err=%v", err)
	}
}

func TestRegisterAll_NilGuards(t *testing.T) {
	t.Parallel()
	if err := handlers.RegisterAll(nil, handlers.Deps{}); err == nil {
		t.Errorf("RegisterAll(nil registry): want error")
	}
	r := commands.NewRegistry()
	if err := handlers.RegisterAll(r, handlers.Deps{}); err == nil {
		t.Errorf("RegisterAll(empty Deps): want error")
	}
	if err := handlers.RegisterAll(r, handlers.Deps{Commands: nil, Housekeeping: emptyHouseSvc(t)}); err == nil {
		t.Errorf("RegisterAll(nil Commands): want error")
	}
}

func TestRegisterAll_RegistersMessagingCleanup(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newCleanupHarness(t, t0)
	r := commands.NewRegistry()
	if err := handlers.RegisterAll(r, allDeps(t, h.svc)); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := r.Lookup(handlers.MessagingCleanupName); !ok {
		t.Errorf("Registry.Lookup(%q): not registered", handlers.MessagingCleanupName)
	}
	if _, ok := r.Lookup(handlers.HousekeepingName); !ok {
		t.Errorf("Registry.Lookup(%q): not registered", handlers.HousekeepingName)
	}
}

func TestDefaultScheduledTasks_Includes_MessagingCleanup(t *testing.T) {
	t.Parallel()
	defaults := handlers.DefaultScheduledTasks()
	var found bool
	for _, d := range defaults {
		if d.TypeName == handlers.MessagingCleanupName {
			found = true
			if d.Interval != 5 {
				t.Errorf("MessagingCleanup interval: %d, want 5", d.Interval)
			}
		}
	}
	if !found {
		t.Errorf("DefaultScheduledTasks missing %q", handlers.MessagingCleanupName)
	}
}

// dispatchHarness adds the queue + dispatcher on top of cleanupHarness
// so we can verify the registry → dispatcher round-trip.
type dispatchHarness struct {
	*cleanupHarness
	queue  *commands.Queue
	cancel context.CancelFunc
	done   chan struct{}
}

func newDispatchHarness(t *testing.T, t0 time.Time) *dispatchHarness {
	t.Helper()
	c := newCleanupHarness(t, t0)
	registry := commands.NewRegistry()
	queue := commands.NewQueue(registry.Properties)
	queue.SetClock(func() time.Time { return t0 })
	c.svc.SetQueue(queue)

	if err := handlers.RegisterAll(registry, allDeps(t, c.svc)); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	disp, err := commands.NewDispatcher(c.svc, queue, registry, slog.Default())
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = disp.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return &dispatchHarness{cleanupHarness: c, queue: queue, cancel: cancel, done: done}
}

func waitForStatus(t *testing.T, svc *commands.Service, id int, want commands.Status) *commands.Command {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		row, err := svc.Get(t.Context(), id)
		if err == nil && commands.Status(row.Status) == want {
			return row
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waitForStatus(id=%d, want=%s): timeout", id, want)
	return nil
}

// TestMessagingCleanup_DispatchesViaRegistry pushes a MessagingCleanup
// command and observes the registry → dispatcher → handler →
// Service.Complete round-trip end-to-end.
func TestMessagingCleanup_DispatchesViaRegistry(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newDispatchHarness(t, t0)

	// Seed an aged row so the handler has something to delete; this
	// also doubles as a hidden invariant check — when the handler
	// completes, this row is gone.
	aged := h.seedTerminal(t, "Aged", t0.Add(-48*time.Hour))

	row, err := h.svc.Push(t.Context(), handlers.MessagingCleanupName, "{}",
		commands.PriorityLow, commands.TriggerScheduled)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	final := waitForStatus(t, h.svc, row.ID, commands.StatusCompleted)
	if final.EndedAt == nil {
		t.Errorf("EndedAt: nil, want stamped")
	}
	if final.Exception != "" {
		t.Errorf("Exception: %q, want empty", final.Exception)
	}
	if _, err := h.svc.Get(t.Context(), aged.ID); !errors.Is(err, commands.ErrNotFound) {
		t.Errorf("aged row: want ErrNotFound after dispatch, got err=%v", err)
	}
}
