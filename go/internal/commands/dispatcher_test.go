package commands_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
)

// dispatchHarness wires a fresh Service + Queue + Dispatcher around a
// sqlite test DB. The dispatcher runs on its own goroutine; cancel
// `cancel` to shut it down.
type dispatchHarness struct {
	t        *testing.T
	svc      *commands.Service
	queue    *commands.Queue
	registry *commands.CommandRegistry
	bus      *messaging.Bus
	cancel   context.CancelFunc
	done     chan struct{}
}

func newDispatchHarness(t *testing.T) *dispatchHarness {
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
	registry := commands.NewRegistry()
	queue := commands.NewQueue(registry.Properties)
	svc.SetQueue(queue)

	disp, err := commands.NewDispatcher(svc, queue, registry, slog.Default())
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
	return &dispatchHarness{
		t:        t,
		svc:      svc,
		queue:    queue,
		registry: registry,
		bus:      bus,
		cancel:   cancel,
		done:     done,
	}
}

// waitForStatus polls Service.Get until the row reaches the target
// status or the deadline expires.
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

func TestDispatcher_HappyPath(t *testing.T) {
	t.Parallel()
	h := newDispatchHarness(t)

	var seenBody atomic.Value
	h.registry.Register("RefreshSeries", commands.CommandSpec{
		Handler: commands.HandlerFunc(func(_ context.Context, body string) error {
			seenBody.Store(body)
			return nil
		}),
	})

	// Subscribe to CommandExecutedEvent so we can assert it fires.
	eventCh := make(chan *commands.Command, 1)
	messaging.Subscribe(h.bus, "test", func(_ context.Context, e commands.CommandExecutedEvent) error {
		select {
		case eventCh <- e.Command:
		default:
		}
		return nil
	})

	row, err := h.svc.Push(t.Context(), "RefreshSeries", `{"id":42}`, commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	final := waitForStatus(t, h.svc, row.ID, commands.StatusCompleted)
	if final.EndedAt == nil {
		t.Errorf("EndedAt: nil, want stamped")
	}
	if got := seenBody.Load(); got != `{"id":42}` {
		t.Errorf("handler body: got %q, want %q", got, `{"id":42}`)
	}

	// Result is persisted by repo.End as of J199; the in-memory event
	// payload carries the same value.
	select {
	case ev := <-eventCh:
		if ev.ID != row.ID {
			t.Errorf("event id=%d, want %d", ev.ID, row.ID)
		}
		if commands.Result(ev.Result) != commands.ResultSuccessful {
			t.Errorf("event Result: %v, want Successful", commands.Result(ev.Result))
		}
	case <-time.After(time.Second):
		t.Errorf("CommandExecutedEvent did not fire")
	}

	// Queue should have drained.
	if h.queue.Len() != 0 {
		t.Errorf("queue.Len after Complete: %d, want 0", h.queue.Len())
	}
}

func TestDispatcher_HandlerError_Fails(t *testing.T) {
	t.Parallel()
	h := newDispatchHarness(t)
	wantErr := errors.New("handler boom")
	h.registry.Register("Backup", commands.CommandSpec{
		Handler: commands.HandlerFunc(func(_ context.Context, _ string) error { return wantErr }),
	})

	eventCh := make(chan *commands.Command, 1)
	messaging.Subscribe(h.bus, "test", func(_ context.Context, e commands.CommandExecutedEvent) error {
		select {
		case eventCh <- e.Command:
		default:
		}
		return nil
	})

	row, err := h.svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	final := waitForStatus(t, h.svc, row.ID, commands.StatusFailed)
	if final.Exception == "" || final.Exception != wantErr.Error() {
		t.Errorf("Exception: %q, want %q", final.Exception, wantErr.Error())
	}
	select {
	case ev := <-eventCh:
		if commands.Result(ev.Result) != commands.ResultUnsuccessful {
			t.Errorf("event Result: %v, want Unsuccessful", commands.Result(ev.Result))
		}
	case <-time.After(time.Second):
		t.Errorf("CommandExecutedEvent did not fire")
	}
}

func TestDispatcher_HandlerPanic_Recovers(t *testing.T) {
	t.Parallel()
	h := newDispatchHarness(t)
	h.registry.Register("Boom", commands.CommandSpec{
		Handler: commands.HandlerFunc(func(_ context.Context, _ string) error {
			panic("kaboom")
		}),
	})

	row, err := h.svc.Push(t.Context(), "Boom", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	final := waitForStatus(t, h.svc, row.ID, commands.StatusFailed)
	if final.Exception == "" {
		t.Errorf("Exception empty after panic")
	}
	// Push another command; the dispatcher should still be alive and process it.
	h.registry.Register("Healthy", commands.CommandSpec{
		Handler: commands.HandlerFunc(func(_ context.Context, _ string) error { return nil }),
	})
	row2, _ := h.svc.Push(t.Context(), "Healthy", `{}`, commands.PriorityNormal, commands.TriggerManual)
	waitForStatus(t, h.svc, row2.ID, commands.StatusCompleted)
}

func TestDispatcher_NoHandler_Fails(t *testing.T) {
	t.Parallel()
	h := newDispatchHarness(t)
	row, err := h.svc.Push(t.Context(), "Unknown", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	final := waitForStatus(t, h.svc, row.ID, commands.StatusFailed)
	if final.Exception == "" {
		t.Errorf("Exception empty for no-handler row")
	}
}

func TestDispatcher_PriorityRespected(t *testing.T) {
	t.Parallel()
	h := newDispatchHarness(t)

	// Block first command's handler so the dispatcher is busy when
	// new rows arrive — that lets us verify the next pull picks
	// highest priority among queued rows.
	gate := make(chan struct{})
	released := make(chan struct{})
	h.registry.Register("Blocker", commands.CommandSpec{
		Handler: commands.HandlerFunc(func(_ context.Context, _ string) error {
			gate <- struct{}{}
			<-released
			return nil
		}),
	})

	executed := make(chan string, 3)
	for _, name := range []string{"Low", "High", "Normal"} {
		nm := name
		h.registry.Register(nm, commands.CommandSpec{
			Handler: commands.HandlerFunc(func(_ context.Context, _ string) error {
				executed <- nm
				return nil
			}),
		})
	}

	blocker, _ := h.svc.Push(t.Context(), "Blocker", `{}`, commands.PriorityNormal, commands.TriggerManual)
	<-gate // dispatcher is now stuck inside Blocker.Execute

	// Push three commands of varying priority; they queue while the
	// dispatcher is parked.
	_, _ = h.svc.Push(t.Context(), "Low", `{}`, commands.PriorityLow, commands.TriggerManual)
	_, _ = h.svc.Push(t.Context(), "High", `{}`, commands.PriorityHigh, commands.TriggerManual)
	_, _ = h.svc.Push(t.Context(), "Normal", `{}`, commands.PriorityNormal, commands.TriggerManual)

	// Release the blocker; dispatcher resumes.
	released <- struct{}{}
	waitForStatus(t, h.svc, blocker.ID, commands.StatusCompleted)

	got := []string{<-executed, <-executed, <-executed}
	want := []string{"High", "Normal", "Low"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("priority order: got %v, want %v", got, want)
			break
		}
	}
}

func TestDispatcher_BootRePush(t *testing.T) {
	t.Parallel()
	// Build a fresh DB, manually persist queued rows, then start the
	// dispatcher and feed HandleApplicationStarted's return into the
	// queue. Mirrors what cmd/sonarr does at boot.
	db := datastoretest.OpenSQLite(t)
	r, err := commands.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, _ := commands.NewService(r, bus)
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return t0 })

	// Pre-seed two queued rows + one orphan-Started before the
	// dispatcher exists. (Pretend the previous boot crashed.)
	row1 := &commands.Command{
		Name: "RefreshSeries", Body: `{"id":1}`, Status: int(commands.StatusQueued),
		QueuedAt: t0, Priority: int(commands.PriorityHigh), Trigger: int(commands.TriggerScheduled),
		Result: int(commands.ResultUnknown),
	}
	if _, err := r.Insert(t.Context(), row1); err != nil {
		t.Fatalf("seed Insert row1: %v", err)
	}
	row2 := &commands.Command{
		Name: "RefreshSeries", Body: `{"id":2}`, Status: int(commands.StatusQueued),
		QueuedAt: t0.Add(time.Second), Priority: int(commands.PriorityNormal),
		Trigger: int(commands.TriggerManual), Result: int(commands.ResultUnknown),
	}
	if _, err := r.Insert(t.Context(), row2); err != nil {
		t.Fatalf("seed Insert row2: %v", err)
	}
	startedAt := t0.Add(-time.Hour)
	orphan := &commands.Command{
		Name: "Backup", Body: `{}`, Status: int(commands.StatusStarted),
		QueuedAt: t0.Add(-2 * time.Hour), StartedAt: &startedAt,
		Trigger: int(commands.TriggerManual), Result: int(commands.ResultUnknown),
	}
	if _, err := r.Insert(t.Context(), orphan); err != nil {
		t.Fatalf("seed Insert orphan: %v", err)
	}

	// Now bring up queue + dispatcher.
	registry := commands.NewRegistry()
	queue := commands.NewQueue(registry.Properties)
	svc.SetQueue(queue)
	var executed sync.Map
	registry.Register("RefreshSeries", commands.CommandSpec{
		Handler: commands.HandlerFunc(func(_ context.Context, body string) error {
			executed.Store(body, true)
			return nil
		}),
	})
	disp, _ := commands.NewDispatcher(svc, queue, registry, slog.Default())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { _ = disp.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	// Boot recovery: orphan flips to Orphaned; queued rows feed into queue.
	orphaned, queued, err := svc.HandleApplicationStarted(t.Context())
	if err != nil {
		t.Fatalf("HandleApplicationStarted: %v", err)
	}
	if orphaned != 1 {
		t.Errorf("orphaned: got %d, want 1", orphaned)
	}
	if len(queued) != 2 {
		t.Errorf("queued: got %d, want 2", len(queued))
	}
	ptrs := make([]*commands.Command, len(queued))
	for i := range queued {
		row := queued[i]
		ptrs[i] = &row
	}
	queue.AddMany(ptrs)

	waitForStatus(t, svc, row1.ID, commands.StatusCompleted)
	waitForStatus(t, svc, row2.ID, commands.StatusCompleted)
	for _, body := range []string{`{"id":1}`, `{"id":2}`} {
		if _, ok := executed.Load(body); !ok {
			t.Errorf("handler did not see body %q", body)
		}
	}

	// Orphan row stays Orphaned and is not re-pushed.
	final, _ := svc.Get(t.Context(), orphan.ID)
	if commands.Status(final.Status) != commands.StatusOrphaned {
		t.Errorf("orphan status: %v, want Orphaned", commands.Status(final.Status))
	}
}

func TestDispatcher_CtxCancel_StopsCleanly(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	r, _ := commands.NewRepo(db)
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, _ := commands.NewService(r, bus)
	registry := commands.NewRegistry()
	queue := commands.NewQueue(registry.Properties)
	svc.SetQueue(queue)
	disp, _ := commands.NewDispatcher(svc, queue, registry, slog.Default())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run after cancel: got %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop on ctx cancel")
	}
}

func TestService_Cancel_RemovesFromQueue(t *testing.T) {
	t.Parallel()
	// Don't start a dispatcher — we want the row to stay in queue
	// long enough to call Cancel and verify removal.
	db := datastoretest.OpenSQLite(t)
	r, _ := commands.NewRepo(db)
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, _ := commands.NewService(r, bus)
	registry := commands.NewRegistry()
	queue := commands.NewQueue(registry.Properties)
	svc.SetQueue(queue)

	row, _ := svc.Push(t.Context(), "X", `{}`, commands.PriorityNormal, commands.TriggerManual)
	if queue.Len() != 1 {
		t.Fatalf("Push: queue.Len=%d, want 1", queue.Len())
	}
	if err := svc.Cancel(t.Context(), row.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if queue.Len() != 0 {
		t.Errorf("Cancel: queue.Len=%d, want 0", queue.Len())
	}
	got, _ := svc.Get(t.Context(), row.ID)
	if commands.Status(got.Status) != commands.StatusCancelled {
		t.Errorf("Cancel: db status=%v, want Cancelled", commands.Status(got.Status))
	}
}

func TestRegistry_PanicsOnBadInput(t *testing.T) {
	t.Parallel()
	r := commands.NewRegistry()
	defer func() {
		if recover() == nil {
			t.Errorf("Register(empty name): want panic")
		}
	}()
	r.Register("", commands.CommandSpec{Handler: commands.HandlerFunc(func(_ context.Context, _ string) error { return nil })})
}

func TestRegistry_PanicsOnNilHandler(t *testing.T) {
	t.Parallel()
	r := commands.NewRegistry()
	defer func() {
		if recover() == nil {
			t.Errorf("Register(nil handler): want panic")
		}
	}()
	r.Register("X", commands.CommandSpec{})
}

func TestNewDispatcher_NilGuards(t *testing.T) {
	t.Parallel()
	if _, err := commands.NewDispatcher(nil, nil, nil, nil); err == nil {
		t.Errorf("NewDispatcher(nil...): want error")
	}
}

// ---- J199 — ReportResult propagation tests ----

func TestDispatcher_HandlerReportsIndeterminate_PersistsResult(t *testing.T) {
	t.Parallel()
	h := newDispatchHarness(t)
	h.registry.Register("RefreshSeries", commands.CommandSpec{
		Handler: commands.HandlerFunc(func(ctx context.Context, _ string) error {
			// Handler partially succeeded (in this fake, no actual work);
			// stamp the result so the row reflects "couldn't finish
			// every row" rather than the default Successful.
			commands.ReportResult(ctx, commands.ResultIndeterminate)
			return nil
		}),
	})

	eventCh := make(chan *commands.Command, 1)
	messaging.Subscribe(h.bus, "test.J199Indeterminate", func(_ context.Context, e commands.CommandExecutedEvent) error {
		select {
		case eventCh <- e.Command:
		default:
		}
		return nil
	})

	row, err := h.svc.Push(t.Context(), "RefreshSeries", `{}`, commands.PriorityNormal, commands.TriggerScheduled)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	final := waitForStatus(t, h.svc, row.ID, commands.StatusCompleted)
	if commands.Result(final.Result) != commands.ResultIndeterminate {
		t.Errorf("persisted Result: got %v, want Indeterminate", commands.Result(final.Result))
	}

	select {
	case ev := <-eventCh:
		if commands.Result(ev.Result) != commands.ResultIndeterminate {
			t.Errorf("event Result: got %v, want Indeterminate", commands.Result(ev.Result))
		}
	case <-time.After(time.Second):
		t.Errorf("CommandExecutedEvent did not fire")
	}
}

func TestDispatcher_HandlerDoesNotReport_DefaultsToSuccessful(t *testing.T) {
	t.Parallel()
	h := newDispatchHarness(t)
	h.registry.Register("RefreshSeries", commands.CommandSpec{
		Handler: commands.HandlerFunc(func(_ context.Context, _ string) error {
			return nil
		}),
	})

	row, err := h.svc.Push(t.Context(), "RefreshSeries", `{}`, commands.PriorityNormal, commands.TriggerScheduled)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	final := waitForStatus(t, h.svc, row.ID, commands.StatusCompleted)
	if commands.Result(final.Result) != commands.ResultSuccessful {
		t.Errorf("default Result: got %v, want Successful", commands.Result(final.Result))
	}
}

func TestDispatcher_HandlerReportsThenErrors_FailPreservesReportedResult(t *testing.T) {
	t.Parallel()
	h := newDispatchHarness(t)
	h.registry.Register("Backup", commands.CommandSpec{
		Handler: commands.HandlerFunc(func(ctx context.Context, _ string) error {
			// Handler partially succeeded before hitting a fatal error.
			// The reporter's Indeterminate stamp should override Fail's
			// default-Unsuccessful arm (matches C# Complete/Fail's
			// "preserve non-Unknown Result" guard).
			commands.ReportResult(ctx, commands.ResultIndeterminate)
			return errors.New("partial-then-fatal")
		}),
	})

	row, err := h.svc.Push(t.Context(), "Backup", `{}`, commands.PriorityNormal, commands.TriggerScheduled)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	final := waitForStatus(t, h.svc, row.ID, commands.StatusFailed)
	if commands.Result(final.Result) != commands.ResultIndeterminate {
		t.Errorf("preserved Result: got %v, want Indeterminate (reporter overrides Fail default)", commands.Result(final.Result))
	}
}
