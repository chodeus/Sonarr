package handlers_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// TestMessagingCleanup_ScheduledFireThrough wires the full
// scheduledtasks.Service + Scheduler + commands.Service + Queue +
// Dispatcher stack and pre-seeds a stale ScheduledTasks row for
// MessagingCleanup. One Tick should push a Queued command, the
// dispatcher executes the handler, the row transitions to Completed,
// and (via HandleCommandExecuted) the scheduledtask's LastExecution is
// stamped to "now".
func TestMessagingCleanup_ScheduledFireThrough(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	// Shared sqlite DB so the tasks repo + commands repo see each
	// other (in production they share the main DB).
	db := datastoretest.OpenSQLite(t)

	// commands stack
	cmdRepo, err := commands.NewRepo(db)
	if err != nil {
		t.Fatalf("commands.NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	cmdSvc, err := commands.NewService(cmdRepo, bus)
	if err != nil {
		t.Fatalf("commands.NewService: %v", err)
	}
	cmdSvc.SetClock(func() time.Time { return t0 })

	registry := commands.NewRegistry()
	queue := commands.NewQueue(registry.Properties)
	queue.SetClock(func() time.Time { return t0 })
	cmdSvc.SetQueue(queue)
	if err := handlers.RegisterAll(registry, cmdSvc); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	disp, err := commands.NewDispatcher(cmdSvc, queue, registry, slog.Default())
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	// scheduledtasks stack
	taskRepo, err := scheduledtasks.NewRepo(db)
	if err != nil {
		t.Fatalf("scheduledtasks.NewRepo: %v", err)
	}
	taskSvc, err := scheduledtasks.NewService(taskRepo)
	if err != nil {
		t.Fatalf("scheduledtasks.NewService: %v", err)
	}
	taskSvc.SetClock(func() time.Time { return t0 })

	// Pre-seed a stale row: LastExecution at t0 - 2h, interval 5 min,
	// so the task is overdue. RegisterDefaults preserves the
	// timestamp on existing rows.
	stale := &scheduledtasks.Task{
		TypeName:      handlers.MessagingCleanupName,
		Interval:      5,
		LastExecution: t0.Add(-2 * time.Hour),
	}
	if err := taskRepo.Upsert(t.Context(), stale); err != nil {
		t.Fatalf("seed Upsert: %v", err)
	}
	if err := taskSvc.RegisterDefaults(t.Context(), handlers.DefaultScheduledTasks()); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}

	// Subscribe HandleCommandExecuted so LastExecution updates after
	// the handler completes.
	messaging.Subscribe(bus, "scheduledtasks.HandleCommandExecuted",
		taskSvc.HandleCommandExecuted)

	scheduler, err := scheduledtasks.NewScheduler(taskSvc, cmdSvc, slog.Default())
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	// Bring up the dispatcher.
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { _ = disp.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	// Drive one Tick (deterministic; no time.Sleep on the 30s ticker).
	pushed, err := scheduler.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pushed != 1 {
		t.Fatalf("Tick pushed %d, want 1", pushed)
	}

	// Find the pushed row by name and wait for Completed.
	hits, err := cmdRepo.FindByName(t.Context(), handlers.MessagingCleanupName)
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("FindByName: got %d rows, want 1", len(hits))
	}
	cmdRow := waitForStatus(t, cmdSvc, hits[0].ID, commands.StatusCompleted)
	if cmdRow.EndedAt == nil {
		t.Errorf("EndedAt: nil, want stamped")
	}
	if cmdRow.Exception != "" {
		t.Errorf("Exception: %q, want empty", cmdRow.Exception)
	}

	// The scheduledtask's LastExecution should have advanced to t0
	// via HandleCommandExecuted. Bus subscribers run synchronously,
	// so by the time Service.Complete returns the cache is updated;
	// but Complete runs inside the dispatcher goroutine — give it a
	// moment to flush by polling the cache.
	deadline := time.Now().Add(2 * time.Second)
	var got time.Time
	for time.Now().Before(deadline) {
		got, err = taskSvc.GetNextExecution(t.Context(), handlers.MessagingCleanupName)
		if err == nil && !got.Equal(stale.LastExecution.Add(time.Duration(stale.Interval)*time.Minute)) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GetNextExecution: %v", err)
	}
	want := t0.Add(time.Duration(stale.Interval) * time.Minute)
	if !got.Equal(want) {
		t.Errorf("GetNextExecution after fire-through: %v, want %v", got, want)
	}
}

// TestBootRecovery_RePushesQueuedRows mirrors the cmd/sonarr boot
// recovery path: pre-seed Queued and Started rows, run
// HandleApplicationStarted, feed the result into the queue, and
// confirm the dispatcher drains them while the orphan-Started row
// transitions to Orphaned.
func TestBootRecovery_RePushesQueuedRows(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	db := datastoretest.OpenSQLite(t)
	cmdRepo, err := commands.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	cmdSvc, err := commands.NewService(cmdRepo, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	cmdSvc.SetClock(func() time.Time { return t0 })

	// Seed: Queued row (should be re-pushed) + Started row (should be
	// orphaned).
	queuedRow := &commands.Command{
		Name:     handlers.MessagingCleanupName,
		Body:     "{}",
		Priority: int(commands.PriorityLow),
		Status:   int(commands.StatusQueued),
		QueuedAt: t0.Add(-time.Hour),
		Trigger:  int(commands.TriggerScheduled),
		Result:   int(commands.ResultUnknown),
	}
	if _, err := cmdRepo.Insert(t.Context(), queuedRow); err != nil {
		t.Fatalf("seed Queued: %v", err)
	}
	startedAt := t0.Add(-30 * time.Minute)
	startedRow := &commands.Command{
		Name:      handlers.MessagingCleanupName,
		Body:      "{}",
		Priority:  int(commands.PriorityLow),
		Status:    int(commands.StatusStarted),
		QueuedAt:  t0.Add(-time.Hour),
		StartedAt: &startedAt,
		Trigger:   int(commands.TriggerScheduled),
		Result:    int(commands.ResultUnknown),
	}
	if _, err := cmdRepo.Insert(t.Context(), startedRow); err != nil {
		t.Fatalf("seed Started: %v", err)
	}

	// Bring up the queue + dispatcher.
	registry := commands.NewRegistry()
	queue := commands.NewQueue(registry.Properties)
	queue.SetClock(func() time.Time { return t0 })
	cmdSvc.SetQueue(queue)
	if err := handlers.RegisterAll(registry, cmdSvc); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	disp, err := commands.NewDispatcher(cmdSvc, queue, registry, slog.Default())
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	// Boot recovery.
	orphaned, queuedAtBoot, err := cmdSvc.HandleApplicationStarted(t.Context())
	if err != nil {
		t.Fatalf("HandleApplicationStarted: %v", err)
	}
	if orphaned != 1 {
		t.Errorf("orphaned: %d, want 1", orphaned)
	}
	if len(queuedAtBoot) != 1 {
		t.Errorf("queuedAtBoot: %d, want 1", len(queuedAtBoot))
	}
	ptrs := make([]*commands.Command, len(queuedAtBoot))
	for i := range queuedAtBoot {
		row := queuedAtBoot[i]
		ptrs[i] = &row
	}
	queue.AddMany(ptrs)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { _ = disp.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitForStatus(t, cmdSvc, queuedRow.ID, commands.StatusCompleted)

	// Orphan row stays Orphaned.
	got, err := cmdSvc.Get(t.Context(), startedRow.ID)
	if err != nil {
		t.Fatalf("Get orphan: %v", err)
	}
	if commands.Status(got.Status) != commands.StatusOrphaned {
		t.Errorf("orphan Status: %v, want Orphaned", commands.Status(got.Status))
	}
}
