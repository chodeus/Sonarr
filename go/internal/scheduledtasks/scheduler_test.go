package scheduledtasks_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// schedHarness wires a fresh tasks.Service + commands.Service +
// Scheduler around two sqlite test DBs (one per package).
type schedHarness struct {
	tasks     *scheduledtasks.Service
	tasksRepo *scheduledtasks.Repo
	cmds      *commands.Service
	sched     *scheduledtasks.Scheduler
	clock     func() time.Time
}

func newSchedHarness(t *testing.T, t0 time.Time) *schedHarness {
	t.Helper()
	// scheduledtasks side
	tdb := datastoretest.OpenSQLite(t)
	tr, err := scheduledtasks.NewRepo(tdb)
	if err != nil {
		t.Fatalf("NewRepo(tasks): %v", err)
	}
	tasksSvc, err := scheduledtasks.NewService(tr)
	if err != nil {
		t.Fatalf("NewService(tasks): %v", err)
	}
	clock := func() time.Time { return t0 }
	tasksSvc.SetClock(clock)

	// commands side
	cdb := datastoretest.OpenSQLite(t)
	cr, err := commands.NewRepo(cdb)
	if err != nil {
		t.Fatalf("NewRepo(commands): %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	cmdsSvc, err := commands.NewService(cr, bus)
	if err != nil {
		t.Fatalf("NewService(commands): %v", err)
	}
	cmdsSvc.SetClock(clock)

	sched, err := scheduledtasks.NewScheduler(tasksSvc, cmdsSvc, slog.Default())
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return &schedHarness{tasks: tasksSvc, tasksRepo: tr, cmds: cmdsSvc, sched: sched, clock: clock}
}

// seedDueTask inserts a ScheduledTasks row with LastExecution =
// t0-2h so a subsequent RegisterDefaults preserves the stale
// timestamp and GetPending sees the task as due. RegisterDefaults
// only stamps LastExecution = now for *fresh* (not pre-seeded) rows.
// typeName/intervalMin always take the same value in current tests
// but expressing them at the call site keeps the intent readable.
//
//nolint:unparam // see comment above
func (h *schedHarness) seedDueTask(t *testing.T, typeName string, intervalMin int, t0 time.Time) {
	t.Helper()
	row := &scheduledtasks.Task{
		TypeName:      typeName,
		Interval:      intervalMin,
		LastExecution: t0.Add(-2 * time.Hour),
	}
	if err := h.tasksRepo.Upsert(t.Context(), row); err != nil {
		t.Fatalf("seed Upsert(%q): %v", typeName, err)
	}
}

func TestScheduler_NewScheduler_NilGuards(t *testing.T) {
	t.Parallel()
	if _, err := scheduledtasks.NewScheduler(nil, nil, nil); err == nil {
		t.Errorf("NewScheduler(nil...): want error")
	}
}

func TestScheduler_Tick_PushesPending(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newSchedHarness(t, t0)

	// Pre-seed Refresh as already-stale so RegisterDefaults
	// preserves its LastExecution. FreshlyRun is fresh and gets
	// LastExecution=now from RegisterDefaults — not due.
	h.seedDueTask(t, "Refresh", 60, t0)
	defaults := []*scheduledtasks.Task{
		{TypeName: "Refresh", Interval: 60},
		{TypeName: "FreshlyRun", Interval: 60},
	}
	if err := h.tasks.RegisterDefaults(t.Context(), defaults); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}

	pushed, err := h.sched.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pushed != 1 {
		t.Errorf("Tick: pushed %d, want 1", pushed)
	}

	// Verify the queued row.
	queued, _ := h.cmds.GetQueued(t.Context())
	if len(queued) != 1 {
		t.Fatalf("GetQueued: got %d, want 1", len(queued))
	}
	got := queued[0]
	if got.Name != "Refresh" {
		t.Errorf("Name: %q, want Refresh", got.Name)
	}
	if commands.Trigger(got.Trigger) != commands.TriggerScheduled {
		t.Errorf("Trigger: %v, want Scheduled", commands.Trigger(got.Trigger))
	}
	if commands.Priority(got.Priority) != commands.PriorityLow {
		t.Errorf("Priority: %v, want Low", commands.Priority(got.Priority))
	}
	if got.Body != "{}" {
		t.Errorf("Body: %q, want {}", got.Body)
	}
}

func TestScheduler_Tick_EmptyPending(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newSchedHarness(t, t0)

	pushed, err := h.sched.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pushed != 0 {
		t.Errorf("Tick on empty: pushed %d, want 0", pushed)
	}
	queued, _ := h.cmds.GetQueued(t.Context())
	if len(queued) != 0 {
		t.Errorf("queued after empty Tick: %d, want 0", len(queued))
	}
}

func TestScheduler_Tick_RepeatedDeduplicates(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newSchedHarness(t, t0)

	h.seedDueTask(t, "Refresh", 60, t0)
	defaults := []*scheduledtasks.Task{
		{TypeName: "Refresh", Interval: 60},
	}
	if err := h.tasks.RegisterDefaults(t.Context(), defaults); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}

	// Two ticks in quick succession; the second should dedup against
	// the first via commands.Service.Push.
	if _, err := h.sched.Tick(t.Context()); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if _, err := h.sched.Tick(t.Context()); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	queued, _ := h.cmds.GetQueued(t.Context())
	if len(queued) != 1 {
		t.Errorf("after two ticks: queued=%d, want 1 (dedup)", len(queued))
	}
}

func TestScheduler_Run_RespectsCtxCancel(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newSchedHarness(t, t0)
	h.sched.SetInterval(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- h.sched.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run after cancel: got %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop on ctx cancel")
	}
}

func TestScheduler_Run_TickerFires(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newSchedHarness(t, t0)
	h.sched.SetInterval(20 * time.Millisecond)

	h.seedDueTask(t, "Refresh", 60, t0)
	defaults := []*scheduledtasks.Task{
		{TypeName: "Refresh", Interval: 60},
	}
	if err := h.tasks.RegisterDefaults(t.Context(), defaults); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.sched.Run(ctx) }()

	// Wait until the queue has one row, with a generous deadline so
	// -race + scheduler load don't flake.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		queued, _ := h.cmds.GetQueued(ctx)
		if len(queued) >= 1 {
			cancel()
			<-done
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("ticker did not produce a queued command within deadline")
}

func TestScheduler_SetInterval_RejectsNonPositive(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	h := newSchedHarness(t, t0)
	// Sentinel: SetInterval(0) and SetInterval(-1) should both be
	// no-ops; the scheduler still defaults to DefaultSchedulerInterval.
	h.sched.SetInterval(0)
	h.sched.SetInterval(-1 * time.Second)
	// We can't read the field directly; instead verify Run does NOT
	// fire within a 100ms window when the default 30s interval is in
	// effect. (This is implicit behaviour but worth pinning.)
	h.seedDueTask(t, "Refresh", 60, t0)
	defaults := []*scheduledtasks.Task{
		{TypeName: "Refresh", Interval: 60},
	}
	if err := h.tasks.RegisterDefaults(t.Context(), defaults); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.sched.Run(ctx) }()
	<-done
	queued, _ := h.cmds.GetQueued(t.Context())
	if len(queued) != 0 {
		t.Errorf("default 30s interval: queued=%d, want 0 (ticker shouldn't fire in 100ms)", len(queued))
	}
}
