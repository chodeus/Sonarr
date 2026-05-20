package commands_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
)

func newSvcWithBus(t *testing.T) (*commands.Service, *commands.Repo, *messaging.Bus) {
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
	// Pin the clock so tests are deterministic.
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return t0 })
	return svc, r, bus
}

func TestService_NewServiceErrors(t *testing.T) {
	t.Parallel()
	if _, err := commands.NewService(nil, nil); err == nil {
		t.Errorf("NewService(nil) want error")
	}
}

func TestService_Push_Insert(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)
	row, err := svc.Push(t.Context(), "RefreshSeries", `{"id":1}`, commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if row.ID == 0 {
		t.Errorf("Push: ID not assigned")
	}
	if commands.Status(row.Status) != commands.StatusQueued {
		t.Errorf("Push: Status=%v, want Queued", commands.Status(row.Status))
	}
	if commands.Trigger(row.Trigger) != commands.TriggerManual {
		t.Errorf("Push: Trigger=%v, want Manual", commands.Trigger(row.Trigger))
	}
}

func TestService_Push_DedupesOnQueuedOrStarted(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)
	first, _ := svc.Push(t.Context(), "RefreshSeries", `{"id":1}`, commands.PriorityNormal, commands.TriggerManual)
	dup, err := svc.Push(t.Context(), "RefreshSeries", `{"id":1}`, commands.PriorityHigh, commands.TriggerScheduled)
	if err != nil {
		t.Fatalf("Push dup: %v", err)
	}
	if dup.ID != first.ID {
		t.Errorf("Push: dedup failed; first=%d, dup=%d", first.ID, dup.ID)
	}
	// Different body → new row.
	other, err := svc.Push(t.Context(), "RefreshSeries", `{"id":2}`, commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push other: %v", err)
	}
	if other.ID == first.ID {
		t.Errorf("Push: different bodies should not dedup")
	}
}

func TestService_Push_NoDedupAgainstCompleted(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)
	first, _ := svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err := svc.Start(t.Context(), first); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.Complete(t.Context(), first, ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Should re-queue cleanly.
	again, err := svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push again: %v", err)
	}
	if again.ID == first.ID {
		t.Errorf("Push after Complete: should not dedup; got same ID %d", again.ID)
	}
}

func TestService_Push_ErrorOnEmptyName(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)
	if _, err := svc.Push(t.Context(), "", "{}", commands.PriorityNormal, commands.TriggerManual); err == nil {
		t.Errorf("Push(empty name) want error")
	}
}

func TestService_PushMany(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)
	items := []commands.PushItem{
		{Name: "RefreshSeries", Body: `{"id":1}`, Priority: commands.PriorityNormal, Trigger: commands.TriggerManual},
		{Name: "RefreshSeries", Body: `{"id":1}`, Priority: commands.PriorityNormal, Trigger: commands.TriggerManual}, // dup of first within batch
		{Name: "Backup", Body: `{}`, Priority: commands.PriorityHigh, Trigger: commands.TriggerScheduled},
	}
	out, err := svc.PushMany(t.Context(), items)
	if err != nil {
		t.Fatalf("PushMany: %v", err)
	}
	// First and third are new; second is a within-batch dup of first.
	if len(out) != 2 {
		t.Errorf("PushMany: returned %d, want 2", len(out))
	}
	if commands.Priority(out[1].Priority) != commands.PriorityHigh {
		t.Errorf("PushMany[1] priority: %v", commands.Priority(out[1].Priority))
	}
}

func TestService_StartCompletePublishesEvent(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)
	row, _ := svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)

	var fired atomic.Int32
	var lastEv atomic.Pointer[commands.CommandExecutedEvent]
	messaging.Subscribe(bus, "test.cmd.executed",
		func(_ context.Context, ev commands.CommandExecutedEvent) error {
			fired.Add(1)
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	if err := svc.Start(t.Context(), row); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if commands.Status(row.Status) != commands.StatusStarted {
		t.Errorf("after Start: %v, want Started", commands.Status(row.Status))
	}
	if row.StartedAt == nil {
		t.Errorf("after Start: StartedAt not set")
	}

	if err := svc.Complete(t.Context(), row, "ok"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if commands.Status(row.Status) != commands.StatusCompleted {
		t.Errorf("after Complete: %v, want Completed", commands.Status(row.Status))
	}
	if commands.Result(row.Result) != commands.ResultSuccessful {
		t.Errorf("Complete: Result=%v, want Successful", commands.Result(row.Result))
	}
	if row.Duration == nil {
		t.Errorf("Complete: Duration not computed")
	}
	if fired.Load() != 1 {
		t.Errorf("CommandExecutedEvent: fired=%d, want 1", fired.Load())
	}
	if ev := lastEv.Load(); ev == nil || ev.Command == nil || ev.Command.ID != row.ID {
		t.Errorf("event: %+v", ev)
	}
}

func TestService_Complete_PreservesNonUnknownResult(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)
	row, _ := svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err := svc.Start(t.Context(), row); err != nil {
		t.Fatalf("Start: %v", err)
	}
	row.Result = int(commands.ResultIndeterminate) // explicitly non-Unknown
	if err := svc.Complete(t.Context(), row, ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if commands.Result(row.Result) != commands.ResultIndeterminate {
		t.Errorf("Complete: clobbered explicit Result; got %v", commands.Result(row.Result))
	}
}

func TestService_Fail(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)
	row, _ := svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err := svc.Start(t.Context(), row); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var fired atomic.Int32
	messaging.Subscribe(bus, "test.cmd.fail",
		func(_ context.Context, _ commands.CommandExecutedEvent) error {
			fired.Add(1)
			return nil
		})
	if err := svc.Fail(t.Context(), row, "boom", "stack trace here"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if commands.Status(row.Status) != commands.StatusFailed {
		t.Errorf("after Fail: %v, want Failed", commands.Status(row.Status))
	}
	if commands.Result(row.Result) != commands.ResultUnsuccessful {
		t.Errorf("after Fail: Result=%v, want Unsuccessful", commands.Result(row.Result))
	}
	if row.Exception != "stack trace here" {
		t.Errorf("after Fail: Exception=%q", row.Exception)
	}
	if fired.Load() != 1 {
		t.Errorf("Fail event: fired=%d, want 1", fired.Load())
	}
}

func TestService_Cancel(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)
	row, _ := svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err := svc.Cancel(t.Context(), row.ID); err != nil {
		t.Fatalf("Cancel queued: %v", err)
	}
	got, _ := svc.Get(t.Context(), row.ID)
	if commands.Status(got.Status) != commands.StatusCancelled {
		t.Errorf("after Cancel: %v, want Cancelled", commands.Status(got.Status))
	}
	if got.EndedAt == nil {
		t.Errorf("after Cancel: EndedAt not set")
	}

	// Cancel a Started row → ErrCannotCancel.
	row2, _ := svc.Push(t.Context(), "Backup", `{"x":1}`, commands.PriorityNormal, commands.TriggerManual)
	if err := svc.Start(t.Context(), row2); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.Cancel(t.Context(), row2.ID); !errors.Is(err, commands.ErrCannotCancel) {
		t.Errorf("Cancel started: %v, want ErrCannotCancel", err)
	}

	// Cancel missing → ErrNotFound.
	if err := svc.Cancel(t.Context(), 99999); !errors.Is(err, commands.ErrNotFound) {
		t.Errorf("Cancel missing: %v, want ErrNotFound", err)
	}
}

func TestService_CleanCommands(t *testing.T) {
	t.Parallel()
	svc, r, _ := newSvcWithBus(t)
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	// Direct repo seed of an old completed row (bypasses the Push
	// dedup so we can pin EndedAt).
	old := &commands.Command{
		Name: "Backup", Body: "{}",
		Status:   int(commands.StatusCompleted),
		QueuedAt: now.Add(-48 * time.Hour),
		EndedAt:  ptrTime(now.Add(-2 * time.Hour)),
		Result:   int(commands.ResultSuccessful),
	}
	if _, err := r.Insert(t.Context(), old); err != nil {
		t.Fatalf("seed: %v", err)
	}
	deleted, err := svc.CleanCommands(t.Context(), now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("CleanCommands: %v", err)
	}
	if deleted != 1 {
		t.Errorf("CleanCommands: deleted=%d, want 1", deleted)
	}
}

func TestService_HandleApplicationStarted(t *testing.T) {
	t.Parallel()
	svc, r, _ := newSvcWithBus(t)
	// 2 queued + 1 started.
	if _, err := svc.Push(t.Context(), "A", "{}", commands.PriorityNormal, commands.TriggerManual); err != nil {
		t.Fatalf("Push A: %v", err)
	}
	if _, err := svc.Push(t.Context(), "B", "{}", commands.PriorityNormal, commands.TriggerManual); err != nil {
		t.Fatalf("Push B: %v", err)
	}
	startedRow, _ := svc.Push(t.Context(), "C", "{}", commands.PriorityNormal, commands.TriggerManual)
	if err := svc.Start(t.Context(), startedRow); err != nil {
		t.Fatalf("Start C: %v", err)
	}

	orphaned, queued, err := svc.HandleApplicationStarted(t.Context())
	if err != nil {
		t.Fatalf("HandleApplicationStarted: %v", err)
	}
	if orphaned != 1 {
		t.Errorf("orphaned: %d, want 1", orphaned)
	}
	if len(queued) != 2 {
		t.Errorf("queued: %d, want 2", len(queued))
	}
	// Verify the started row is now Orphaned in the DB.
	got, _ := r.Get(t.Context(), startedRow.ID)
	if commands.Status(got.Status) != commands.StatusOrphaned {
		t.Errorf("after HandleApplicationStarted: %v, want Orphaned", commands.Status(got.Status))
	}
}

func TestService_GettersPassthrough(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)
	a, _ := svc.Push(t.Context(), "A", "{}", commands.PriorityNormal, commands.TriggerManual)
	b, _ := svc.Push(t.Context(), "B", "{}", commands.PriorityNormal, commands.TriggerManual)
	if err := svc.Start(t.Context(), b); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if got, err := svc.Get(t.Context(), a.ID); err != nil || got.Name != "A" {
		t.Errorf("Get: %v / %+v", err, got)
	}
	all, err := svc.All(t.Context())
	if err != nil || len(all) != 2 {
		t.Errorf("All: %v / %d", err, len(all))
	}
	queued, err := svc.GetQueued(t.Context())
	if err != nil || len(queued) != 1 || queued[0].Name != "A" {
		t.Errorf("GetQueued: %v / %+v", err, queued)
	}
	started, err := svc.GetStarted(t.Context())
	if err != nil || len(started) != 1 || started[0].Name != "B" {
		t.Errorf("GetStarted: %v / %+v", err, started)
	}
}

func TestService_NilBusSkipsPublish(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	r, err := commands.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := commands.NewService(r, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	row, err := svc.Push(t.Context(), "Backup", "{}", commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if err := svc.Start(t.Context(), row); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.Complete(t.Context(), row, ""); err != nil {
		t.Fatalf("Complete (nil bus): %v", err)
	}
	if err := svc.Fail(t.Context(), row, "", ""); err != nil {
		// Fail on a Completed row is a defensive transition; persist
		// happens at Repo.End which doesn't validate state.
		t.Logf("Fail (defensive double-end): %v", err)
	}
}

// Slice 5.J25 (audit B.2 second wave). CommandUpdatedEvent fires at
// every lifecycle transition — Push, Start, Complete, Fail, Cancel.
// One row's full lifecycle should drive 4 events (Push → Start →
// Complete; Cancel exits early before Start in a separate run).
func TestService_PublishesCommandUpdatedAtEveryTransition(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)

	var statuses []commands.Status
	messaging.Subscribe(bus, "test.cmd.updated",
		func(_ context.Context, ev commands.CommandUpdatedEvent) error {
			if ev.Command != nil {
				statuses = append(statuses, commands.Status(ev.Command.Status))
			}
			return nil
		})

	row, err := svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if err := svc.Start(t.Context(), row); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.Complete(t.Context(), row, ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	want := []commands.Status{
		commands.StatusQueued,
		commands.StatusStarted,
		commands.StatusCompleted,
	}
	if len(statuses) != len(want) {
		t.Fatalf("CommandUpdatedEvent count: got %d, want %d (statuses=%v)", len(statuses), len(want), statuses)
	}
	for i, s := range statuses {
		if s != want[i] {
			t.Errorf("event %d status: got %v, want %v", i, s, want[i])
		}
	}
}

func TestService_CancelPublishesCommandUpdated(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)
	row, err := svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Skip the Push-triggered event by attaching after Push.
	var got atomic.Int32
	var lastStatus atomic.Int32
	messaging.Subscribe(bus, "test.cmd.cancelled",
		func(_ context.Context, ev commands.CommandUpdatedEvent) error {
			got.Add(1)
			if ev.Command != nil {
				lastStatus.Store(int32(ev.Command.Status))
			}
			return nil
		})
	if err := svc.Cancel(t.Context(), row.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("CommandUpdatedEvent on Cancel: fired=%d, want 1", got.Load())
	}
	if commands.Status(lastStatus.Load()) != commands.StatusCancelled {
		t.Errorf("event Status on Cancel: got %v, want Cancelled", commands.Status(lastStatus.Load()))
	}
}

func TestService_FailPublishesCommandUpdated(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)
	row, _ := svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err := svc.Start(t.Context(), row); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var got atomic.Int32
	messaging.Subscribe(bus, "test.cmd.fail.updated",
		func(_ context.Context, ev commands.CommandUpdatedEvent) error {
			if ev.Command != nil && commands.Status(ev.Command.Status) == commands.StatusFailed {
				got.Add(1)
			}
			return nil
		})
	if err := svc.Fail(t.Context(), row, "boom", "stack"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("CommandUpdatedEvent on Fail with Failed status: fired=%d, want 1", got.Load())
	}
}
