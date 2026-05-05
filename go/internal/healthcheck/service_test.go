package healthcheck_test

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/lifecycle"
	"sonarr.tv/sonarr/internal/messaging"
)

// fakeCheck is the table-driven fixture for orchestrator tests. Each
// instance carries its own Run-result generator so a single test can
// transition the same check across pass/fail states by mutating result.
type fakeCheck struct {
	name     string
	startup  bool
	schedule bool
	triggers []healthcheck.EventTrigger

	mu     sync.Mutex
	calls  int
	result func(call int) healthcheck.HealthCheck
}

func (f *fakeCheck) Name() string          { return f.name }
func (f *fakeCheck) CheckOnStartup() bool  { return f.startup }
func (f *fakeCheck) CheckOnSchedule() bool { return f.schedule }
func (f *fakeCheck) EventTriggers() []healthcheck.EventTrigger {
	return f.triggers
}
func (f *fakeCheck) Run(_ context.Context) healthcheck.HealthCheck {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.result == nil {
		return healthcheck.OK(f.name)
	}
	return f.result(f.calls)
}

func (f *fakeCheck) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func failing(name string, reason healthcheck.Reason) func(int) healthcheck.HealthCheck {
	return func(_ int) healthcheck.HealthCheck {
		return healthcheck.New(name, healthcheck.ResultWarning, reason, "boom", "")
	}
}

func newQuietService(t *testing.T, checks []healthcheck.Check, bus *messaging.Bus, opts ...healthcheck.Option) *healthcheck.Service {
	t.Helper()
	log := slog.New(slog.DiscardHandler)
	// Default: zero debounce so event-driven enqueues drain synchronously.
	opts = append([]healthcheck.Option{healthcheck.WithDebounce(0)}, opts...)
	svc, err := healthcheck.NewService(checks, bus, log, opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// TestRun_ManualRunsAllChecks pins the IExecute<CheckHealthCommand>
// behaviour at C# `:139`: TriggerManual runs every check regardless
// of CheckOnSchedule.
func TestRun_ManualRunsAllChecks(t *testing.T) {
	t.Parallel()
	all := &fakeCheck{name: "All", startup: true, schedule: true}
	scheduledOnly := &fakeCheck{name: "Sched", startup: false, schedule: true}
	startupOnly := &fakeCheck{name: "Start", startup: true, schedule: false}
	svc := newQuietService(t, []healthcheck.Check{all, scheduledOnly, startupOnly}, nil)

	if err := svc.Run(t.Context(), healthcheck.TriggerManual); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if all.Calls() != 1 || scheduledOnly.Calls() != 1 || startupOnly.Calls() != 1 {
		t.Fatalf("Manual run: calls all=%d sched=%d startup=%d, want 1/1/1",
			all.Calls(), scheduledOnly.Calls(), startupOnly.Calls())
	}
}

// TestRun_ScheduledRunsScheduledOnly pins the C# `:139` Manual-vs-not
// branch's else side: scheduled trigger runs only the scheduled subset.
func TestRun_ScheduledRunsScheduledOnly(t *testing.T) {
	t.Parallel()
	scheduledOnly := &fakeCheck{name: "Sched", schedule: true}
	startupOnly := &fakeCheck{name: "Start", startup: true}
	svc := newQuietService(t, []healthcheck.Check{scheduledOnly, startupOnly}, nil)

	if err := svc.Run(t.Context(), healthcheck.TriggerScheduled); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if scheduledOnly.Calls() != 1 {
		t.Errorf("scheduled: calls=%d, want 1", scheduledOnly.Calls())
	}
	if startupOnly.Calls() != 0 {
		t.Errorf("startup-only: calls=%d, want 0", startupOnly.Calls())
	}
}

// TestRun_StoresFailingResult pins the cache-set side of the
// transition computation at C# `:120-126`.
func TestRun_StoresFailingResult(t *testing.T) {
	t.Parallel()
	c := &fakeCheck{
		name:     "Boom",
		startup:  true,
		schedule: true,
		result:   failing("Boom", healthcheck.ReasonServerNotification),
	}
	svc := newQuietService(t, []healthcheck.Check{c}, nil)
	_ = svc.Run(t.Context(), healthcheck.TriggerManual)
	results := svc.Results()
	if len(results) != 1 {
		t.Fatalf("Results len = %d, want 1; got %+v", len(results), results)
	}
	if results[0].Source != "Boom" || results[0].Type != healthcheck.ResultWarning {
		t.Errorf("Results[0] = %+v", results[0])
	}
}

// TestRun_ClearsFailingResultOnPass pins the cache-clear path at C#
// `:107-117`.
func TestRun_ClearsFailingResultOnPass(t *testing.T) {
	t.Parallel()
	state := atomic.Int32{}
	c := &fakeCheck{
		name:     "Flaky",
		startup:  true,
		schedule: true,
		result: func(_ int) healthcheck.HealthCheck {
			if state.Load() == 0 {
				return healthcheck.New("Flaky", healthcheck.ResultError,
					healthcheck.ReasonRootFolderMissing, "down", "")
			}
			return healthcheck.OK("Flaky")
		},
	}
	svc := newQuietService(t, []healthcheck.Check{c}, nil)

	_ = svc.Run(t.Context(), healthcheck.TriggerManual)
	if got := len(svc.Results()); got != 1 {
		t.Fatalf("after first run: len=%d, want 1", got)
	}

	state.Store(1)
	_ = svc.Run(t.Context(), healthcheck.TriggerManual)
	if got := len(svc.Results()); got != 0 {
		t.Errorf("after recovery: len=%d, want 0", got)
	}
}

// TestRun_PublishesFailedAndRestoredAndComplete pins the bus fan-out
// at C# `:113`, `:122`, and `:134`.
func TestRun_PublishesFailedAndRestoredAndComplete(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.New(slog.DiscardHandler))
	state := atomic.Int32{}
	c := &fakeCheck{
		name:     "Switcheroo",
		startup:  true,
		schedule: true,
		result: func(_ int) healthcheck.HealthCheck {
			if state.Load() == 0 {
				return healthcheck.New("Switcheroo", healthcheck.ResultWarning,
					healthcheck.ReasonServerNotification, "boom", "")
			}
			return healthcheck.OK("Switcheroo")
		},
	}

	var failedCount, restoredCount, completeCount int64
	messaging.SubscribeAsync(bus, "test.failed",
		func(_ context.Context, _ healthcheck.FailedEvent) error {
			atomic.AddInt64(&failedCount, 1)
			return nil
		})
	messaging.SubscribeAsync(bus, "test.restored",
		func(_ context.Context, _ healthcheck.RestoredEvent) error {
			atomic.AddInt64(&restoredCount, 1)
			return nil
		})
	messaging.SubscribeAsync(bus, "test.complete",
		func(_ context.Context, _ healthcheck.CompleteEvent) error {
			atomic.AddInt64(&completeCount, 1)
			return nil
		})

	svc := newQuietService(t, []healthcheck.Check{c}, bus)

	_ = svc.Run(t.Context(), healthcheck.TriggerManual)
	state.Store(1)
	_ = svc.Run(t.Context(), healthcheck.TriggerManual)

	bus.Wait()
	if got := atomic.LoadInt64(&failedCount); got != 1 {
		t.Errorf("failed publishes = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&restoredCount); got != 1 {
		t.Errorf("restored publishes = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&completeCount); got != 2 {
		t.Errorf("complete publishes = %d, want 2 (one per drain)", got)
	}
}

// TestHandleApplicationStarted_RunsStartupChecksOnly pins the
// IHandleAsync<ApplicationStartedEvent> path at C# `:152-163`.
func TestHandleApplicationStarted_RunsStartupChecksOnly(t *testing.T) {
	t.Parallel()
	startup := &fakeCheck{name: "S", startup: true}
	notStartup := &fakeCheck{name: "N", startup: false, schedule: true}
	svc := newQuietService(t, []healthcheck.Check{startup, notStartup}, nil)

	if err := svc.HandleApplicationStarted(t.Context()); err != nil {
		t.Fatalf("HandleApplicationStarted: %v", err)
	}
	if startup.Calls() != 1 {
		t.Errorf("startup calls = %d, want 1", startup.Calls())
	}
	if notStartup.Calls() != 0 {
		t.Errorf("notStartup calls = %d, want 0", notStartup.Calls())
	}
}

// fakeEvent + fakeEvent2 are the table-driven event types for the
// event-driven path. Mirrors the C# `HealthCheckServiceFixture.cs`
// fakes.
type fakeEvent struct {
	ShouldExecute bool
}
type fakeEvent2 struct{}

// TestHandleAnyEvent_FiresEventDrivenChecks pins the C# `:204-227`
// dispatch logic — bind by event type, gate by Condition, fire.
func TestHandleAnyEvent_FiresEventDrivenChecks(t *testing.T) {
	t.Parallel()
	c := &fakeCheck{
		name: "EventDriven",
		triggers: []healthcheck.EventTrigger{
			{EventType: reflect.TypeOf(fakeEvent{}), Condition: healthcheck.CheckOnAlways},
		},
	}
	svc := newQuietService(t, []healthcheck.Check{c}, nil)
	_ = svc.HandleAnyEvent(t.Context(), fakeEvent{})
	if c.Calls() != 1 {
		t.Errorf("event-driven calls = %d, want 1", c.Calls())
	}
}

// TestHandleAnyEvent_RespectsFilterPredicate pins the
// `EventDrivenHealthCheck<TEvent>.EventFilter` branch at C# `:36-39`.
func TestHandleAnyEvent_RespectsFilterPredicate(t *testing.T) {
	t.Parallel()
	c := &fakeCheck{
		name: "Filtered",
		triggers: []healthcheck.EventTrigger{
			{
				EventType: reflect.TypeOf(fakeEvent{}),
				Filter: func(evt any) bool {
					fe, _ := evt.(fakeEvent)
					return fe.ShouldExecute
				},
			},
		},
	}
	svc := newQuietService(t, []healthcheck.Check{c}, nil)
	_ = svc.HandleAnyEvent(t.Context(), fakeEvent{ShouldExecute: false})
	if c.Calls() != 0 {
		t.Errorf("filter false: calls = %d, want 0", c.Calls())
	}
	_ = svc.HandleAnyEvent(t.Context(), fakeEvent{ShouldExecute: true})
	if c.Calls() != 1 {
		t.Errorf("filter true: calls = %d, want 1", c.Calls())
	}
}

// TestHandleAnyEvent_RespectsConditionFailedOnly pins the
// CheckOnFailedOnly gate at C# `:30-32`.
func TestHandleAnyEvent_RespectsConditionFailedOnly(t *testing.T) {
	t.Parallel()
	state := atomic.Int32{}
	c := &fakeCheck{
		name:    "ConditionFailedOnly",
		startup: true,
		result: func(_ int) healthcheck.HealthCheck {
			if state.Load() == 0 {
				return healthcheck.OK("ConditionFailedOnly")
			}
			return healthcheck.New("ConditionFailedOnly", healthcheck.ResultWarning,
				healthcheck.ReasonServerNotification, "down", "")
		},
		triggers: []healthcheck.EventTrigger{
			{EventType: reflect.TypeOf(fakeEvent2{}), Condition: healthcheck.CheckOnFailedOnly},
		},
	}
	svc := newQuietService(t, []healthcheck.Check{c}, nil)

	// Not yet failing: FailedOnly should suppress.
	_ = svc.HandleAnyEvent(t.Context(), fakeEvent2{})
	if c.Calls() != 0 {
		t.Errorf("not-yet-failing: calls = %d, want 0", c.Calls())
	}
	// Force a failure via Run, then event should fire.
	state.Store(1)
	_ = svc.Run(t.Context(), healthcheck.TriggerManual)
	prevCalls := c.Calls()
	_ = svc.HandleAnyEvent(t.Context(), fakeEvent2{})
	if c.Calls() != prevCalls+1 {
		t.Errorf("now-failing: calls = %d, want %d", c.Calls(), prevCalls+1)
	}
}

// TestHandleAnyEvent_SkipsApplicationStartedAndComplete pins the C#
// `:167` short-circuit.
func TestHandleAnyEvent_SkipsApplicationStartedAndComplete(t *testing.T) {
	t.Parallel()
	c := &fakeCheck{
		name: "ShouldNotFire",
		triggers: []healthcheck.EventTrigger{
			{EventType: reflect.TypeOf(lifecycle.ApplicationStartedEvent{})},
			{EventType: reflect.TypeOf(healthcheck.CompleteEvent{})},
		},
	}
	svc := newQuietService(t, []healthcheck.Check{c}, nil)
	_ = svc.HandleAnyEvent(t.Context(), lifecycle.ApplicationStartedEvent{})
	_ = svc.HandleAnyEvent(t.Context(), healthcheck.CompleteEvent{})
	if c.Calls() != 0 {
		t.Errorf("calls = %d, want 0 (start/complete are short-circuited)", c.Calls())
	}
}

// TestPostGraceSweep_RerunsStartupChecks pins the C# `:175-202` sweep.
// We push the start time into the past so the next event after the
// grace window triggers the sweep.
func TestPostGraceSweep_RerunsStartupChecks(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.New(slog.DiscardHandler))
	c := &fakeCheck{
		name:    "StartupOnly",
		startup: true,
		result:  failing("StartupOnly", healthcheck.ReasonRootFolderMissing),
	}
	svc := newQuietService(t, []healthcheck.Check{c}, bus,
		healthcheck.WithGracePeriod(1*time.Millisecond),
		healthcheck.WithStartTime(time.Now().Add(-time.Hour)),
	)

	// First, populate the result cache so the post-grace re-fire has
	// something to re-publish.
	_ = svc.Run(t.Context(), healthcheck.TriggerManual)
	preCalls := c.Calls()

	var failedAfterGrace int64
	messaging.SubscribeAsync(bus, "test.failed-after-grace",
		func(_ context.Context, evt healthcheck.FailedEvent) error {
			if !evt.IsInStartupGracePeriod {
				atomic.AddInt64(&failedAfterGrace, 1)
			}
			return nil
		})

	// Any non-Started/non-Complete event triggers the sweep.
	_ = svc.HandleAnyEvent(t.Context(), fakeEvent{})

	// Sweep re-runs startup checks AT LEAST once (one for the sweep,
	// possibly one for the event-driven binding — `c` has no binding
	// for fakeEvent, so just the sweep).
	if c.Calls() <= preCalls {
		t.Errorf("post-grace calls = %d, expected > %d", c.Calls(), preCalls)
	}
	bus.Wait()
	// At least one FailedEvent should have been published with
	// IsInStartupGracePeriod=false. The sweep re-publishes for the
	// existing failure even if its result hasn't changed.
	if got := atomic.LoadInt64(&failedAfterGrace); got < 1 {
		t.Errorf("post-grace FailedEvent count = %d, want >= 1", got)
	}
}

// TestRun_DuplicateNameRejected — the orchestrator key is the
// check's Name; two checks under the same name silently lose results
// to last-writer-wins, so construction MUST refuse them.
func TestRun_DuplicateNameRejected(t *testing.T) {
	t.Parallel()
	a := &fakeCheck{name: "Same"}
	b := &fakeCheck{name: "Same"}
	_, err := healthcheck.NewService([]healthcheck.Check{a, b}, nil, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatalf("NewService: expected duplicate-name error, got nil")
	}
}

// TestRun_NilCheckRejected — nil entry is a wiring bug; refuse early.
func TestRun_NilCheckRejected(t *testing.T) {
	t.Parallel()
	_, err := healthcheck.NewService([]healthcheck.Check{nil}, nil, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatalf("NewService: expected nil-check error, got nil")
	}
}

// TestRun_PanicInCheckRecovered — a panicking check must NOT take
// down the dispatcher worker. Recover, log, treat as ResultOK.
func TestRun_PanicInCheckRecovered(t *testing.T) {
	t.Parallel()
	c := &fakeCheck{
		name:     "Panicky",
		startup:  true,
		schedule: true,
		result: func(_ int) healthcheck.HealthCheck {
			panic("boom")
		},
	}
	svc := newQuietService(t, []healthcheck.Check{c}, nil)
	if err := svc.Run(t.Context(), healthcheck.TriggerManual); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(svc.Results()); got != 0 {
		t.Errorf("Results after panic: %d, want 0 (panic treated as OK)", got)
	}
}

// TestSubscribe_WiresApplicationStartedAndAnyEvent confirms the bus
// integration: publishing ApplicationStartedEvent runs startup checks;
// publishing a fakeEvent triggers event-driven checks via the global
// subscriber.
func TestSubscribe_WiresApplicationStartedAndAnyEvent(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.New(slog.DiscardHandler))
	startup := &fakeCheck{name: "S", startup: true}
	driven := &fakeCheck{
		name: "D",
		triggers: []healthcheck.EventTrigger{
			{EventType: reflect.TypeOf(fakeEvent{})},
		},
	}
	svc := newQuietService(t, []healthcheck.Check{startup, driven}, bus)
	svc.Subscribe(bus)

	if err := messaging.Publish(t.Context(), bus, lifecycle.ApplicationStartedEvent{}); err != nil {
		t.Fatalf("Publish ApplicationStartedEvent: %v", err)
	}
	if err := messaging.Publish(t.Context(), bus, fakeEvent{}); err != nil {
		t.Fatalf("Publish fakeEvent: %v", err)
	}
	bus.Wait()

	if startup.Calls() < 1 {
		t.Errorf("startup calls = %d, want >= 1", startup.Calls())
	}
	if driven.Calls() < 1 {
		t.Errorf("driven calls = %d, want >= 1", driven.Calls())
	}
}
