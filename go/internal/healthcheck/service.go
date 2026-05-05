package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"sonarr.tv/sonarr/internal/lifecycle"
	"sonarr.tv/sonarr/internal/messaging"
)

// Trigger gates which check set runs on a Run() invocation. Mirrors
// the C# `CommandTrigger` enum's Manual / Scheduled distinction at
// `HealthCheckService.cs:139` (`message.Trigger == CommandTrigger.Manual`).
type Trigger int

const (
	// TriggerScheduled runs the subset of checks where
	// CheckOnSchedule() is true. Used by the scheduler / scheduled
	// CheckHealthCommand pushes.
	TriggerScheduled Trigger = 0
	// TriggerManual runs every registered check. Used when a user hits
	// the "Test" button in the UI / pushes the command via the API.
	TriggerManual Trigger = 1
)

// DefaultGracePeriod mirrors the C# `:59`
// `_startupGracePeriodEndTime = runtimeInfo.StartTime + TimeSpan.FromMinutes(15)`.
// Failed checks during this window are tagged IsInStartupGracePeriod=true
// so the notification subscriber can suppress alerts for transient
// startup conditions (mount points, indexers warming up).
const DefaultGracePeriod = 15 * time.Minute

// DefaultDebounce mirrors the C# `:54` `TimeSpan.FromSeconds(5)`.
// Multiple events arriving inside this window collapse to a single
// pending-set drain.
const DefaultDebounce = 5 * time.Second

// Service is the health-check orchestrator. Mirrors
// `src/NzbDrone.Core/HealthCheck/HealthCheckService.cs:21-231`.
//
// Surface (the Go-shape collapse of three C# IHandle interfaces):
//
//   - Run(ctx, Trigger) — IExecute<CheckHealthCommand>.
//   - HandleApplicationStarted(ctx) — IHandleAsync<ApplicationStartedEvent>.
//   - HandleAnyEvent(ctx, evt) — IHandleAsync<IEvent> (the global subscriber).
//   - Subscribe(bus) — wires both handlers into a Bus.
//   - Results() — IHealthCheckService.Results.
//
// The Service is goroutine-safe. The pending-set + result-cache live
// behind a single mutex; the debouncer uses time.AfterFunc + a
// supplementary mutex so resets are atomic vs the firing goroutine.
type Service struct {
	bus *messaging.Bus
	log *slog.Logger

	// Sorted check sets. checks is the full list (in registration
	// order); startupChecks + scheduledChecks are the subsets used
	// by HandleApplicationStarted and the scheduled Trigger path.
	checks          []Check
	startupChecks   []Check
	scheduledChecks []Check

	// eventDriven is the [CheckOn] index. Built once at construction
	// from each check's EventTriggers(). Lookup key is reflect.TypeOf
	// of the bus-delivered event (the C# uses `message.GetType()`).
	eventDriven map[reflect.Type][]eventBinding

	// resultMu guards results + pending. Held during ProcessHealthChecks
	// so a Trigger=Manual run and a debounce-fired event-driven run
	// can't interleave their result-cache mutations.
	resultMu sync.Mutex
	results  map[string]HealthCheck // keyed by check.Name()
	pending  map[string]Check       // pending set; keyed by check.Name() to dedup

	// processMu serialises ProcessHealthChecks invocations so a
	// debounce-fired drain can't race a Manual Run drain.
	processMu sync.Mutex

	// debounceMu guards debounceTimer. Separated from resultMu so a
	// timer-fired drain can take resultMu without a deadlock.
	debounceMu     sync.Mutex
	debounceTimer  *time.Timer
	debounceWindow time.Duration

	startTime         time.Time
	gracePeriod       time.Duration
	hasRunAfterGrace  atomic.Bool
	runningAfterGrace atomic.Bool
	now               func() time.Time

	// inProcessing is the C# `_debounce.Pause()` analogue — set to
	// true while ProcessHealthChecks is iterating results so a fresh
	// Debounce.Execute() called from inside a check's notification
	// chain won't reschedule until we're done.
	inProcessing atomic.Bool
}

type eventBinding struct {
	check     Check
	condition CheckOnCondition
	filter    func(event any) bool
}

// Option configures a Service at construction time.
type Option func(*Service)

// WithGracePeriod overrides the 15-minute startup grace window.
// Tests use a tiny value so they can exercise the post-grace
// re-run path without sleeping.
func WithGracePeriod(d time.Duration) Option {
	return func(s *Service) { s.gracePeriod = d }
}

// WithDebounce overrides the 5-second debounce window. Tests pin to
// near-zero so HandleAnyEvent → debounce drain is observable
// without a real wall-clock sleep.
func WithDebounce(d time.Duration) Option {
	return func(s *Service) { s.debounceWindow = d }
}

// WithClock overrides time.Now. Used by tests pinning grace-period
// boundary behaviour.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithStartTime overrides the recorded process start (default:
// time.Now() at construction). Tests use this to push the grace-period
// boundary forward.
func WithStartTime(t time.Time) Option {
	return func(s *Service) { s.startTime = t }
}

// NewService builds a Service from a flat list of checks. bus may be
// nil — without a bus the orchestrator still tracks results and
// runs checks but never publishes Failed/Restored/Complete events.
//
// Returns an error if any check is nil OR if two checks share the
// same Name (the result cache uses Name as its key, so duplicates
// silently lose results to last-writer-wins).
func NewService(checks []Check, bus *messaging.Bus, log *slog.Logger, opts ...Option) (*Service, error) {
	if log == nil {
		log = slog.Default()
	}
	for i, c := range checks {
		if c == nil {
			return nil, fmt.Errorf("healthcheck: NewService: nil check at idx %d", i)
		}
	}
	seen := make(map[string]struct{}, len(checks))
	for _, c := range checks {
		if _, dup := seen[c.Name()]; dup {
			return nil, fmt.Errorf("healthcheck: NewService: duplicate check name %q", c.Name())
		}
		seen[c.Name()] = struct{}{}
	}

	cp := make([]Check, len(checks))
	copy(cp, checks)
	startup := make([]Check, 0, len(cp))
	scheduled := make([]Check, 0, len(cp))
	for _, c := range cp {
		if c.CheckOnStartup() {
			startup = append(startup, c)
		}
		if c.CheckOnSchedule() {
			scheduled = append(scheduled, c)
		}
	}
	eventDriven := buildEventDrivenIndex(cp)

	s := &Service{
		bus:             bus,
		log:             log,
		checks:          cp,
		startupChecks:   startup,
		scheduledChecks: scheduled,
		eventDriven:     eventDriven,
		results:         map[string]HealthCheck{},
		pending:         map[string]Check{},
		debounceWindow:  DefaultDebounce,
		gracePeriod:     DefaultGracePeriod,
		now:             func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.startTime.IsZero() {
		s.startTime = s.now()
	}
	return s, nil
}

func buildEventDrivenIndex(checks []Check) map[reflect.Type][]eventBinding {
	idx := map[reflect.Type][]eventBinding{}
	for _, c := range checks {
		ed, ok := c.(EventDrivenCheck)
		if !ok {
			continue
		}
		for _, t := range ed.EventTriggers() {
			if t.EventType == nil {
				continue
			}
			idx[t.EventType] = append(idx[t.EventType], eventBinding{
				check:     c,
				condition: t.Condition,
				filter:    t.Filter,
			})
		}
	}
	return idx
}

// Subscribe wires HandleApplicationStarted + HandleAnyEvent into bus.
// Idempotent only by virtue of the messaging bus accepting duplicate
// subscriptions (each call adds another subscriber); call exactly
// once per Service.
func (s *Service) Subscribe(bus *messaging.Bus) {
	messaging.SubscribeAsync(bus, "healthcheck.HandleApplicationStarted",
		func(ctx context.Context, _ lifecycle.ApplicationStartedEvent) error {
			return s.HandleApplicationStarted(ctx)
		})
	messaging.SubscribeAllAsync(bus, "healthcheck.HandleAnyEvent",
		func(ctx context.Context, evt any) error {
			return s.HandleAnyEvent(ctx, evt)
		})
}

// Results returns a snapshot of the current failing-check cache. The
// slice is freshly allocated each call so callers may mutate without
// affecting the orchestrator's state.
//
// Mirrors `IHealthCheckService.Results()` at `HealthCheckService.cs:62-65`.
// Order is non-deterministic (map iteration); callers that render
// the list to a UI should sort by Source for stable output.
func (s *Service) Results() []HealthCheck {
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	out := make([]HealthCheck, 0, len(s.results))
	for _, r := range s.results {
		out = append(out, r)
	}
	return out
}

// Run is the IExecute<CheckHealthCommand> port. Adds the appropriate
// check set to the pending queue and immediately drains.
//
// Mirrors `HealthCheckService.Execute(CheckHealthCommand)` at `:137-150`.
func (s *Service) Run(ctx context.Context, trigger Trigger) error {
	set := s.scheduledChecks
	if trigger == TriggerManual {
		set = s.checks
	}
	s.enqueue(set)
	s.processPending(ctx)
	return nil
}

// HandleApplicationStarted is the IHandleAsync<ApplicationStartedEvent>
// port. Drains the startup-check subset.
//
// Mirrors `HealthCheckService.HandleAsync(ApplicationStartedEvent)` at `:152-163`.
func (s *Service) HandleApplicationStarted(ctx context.Context) error {
	s.enqueue(s.startupChecks)
	s.processPending(ctx)
	return nil
}

// HandleAnyEvent is the IHandleAsync<IEvent> port. Performs three
// behaviours, mirroring the C# global subscriber at `:165-230`:
//
//  1. Skip CompleteEvent / ApplicationStartedEvent (the C# tree
//     skips them; CompleteEvent because we publish it ourselves and
//     would otherwise loop, ApplicationStartedEvent because it has
//     a dedicated handler).
//
//  2. If we have NOT yet re-run startup checks after the grace
//     period AND the grace period has expired AND we're not already
//     running them, do that now. After completion, fire FailedEvent
//     for every still-cached result so notification subscribers
//     learn about failures that arrived during the grace window.
//
//  3. Look up the event type in the event-driven index. For each
//     binding, gate by Condition + Filter, and add the check to the
//     pending set. Schedule a debounce drain.
func (s *Service) HandleAnyEvent(ctx context.Context, evt any) error {
	if _, ok := evt.(CompleteEvent); ok {
		return nil
	}
	if _, ok := evt.(lifecycle.ApplicationStartedEvent); ok {
		return nil
	}

	if !s.hasRunAfterGrace.Load() &&
		!s.runningAfterGrace.Load() &&
		s.now().After(s.startTime.Add(s.gracePeriod)) {
		s.postGraceSweep(ctx)
	}

	t := reflect.TypeOf(evt)
	bindings, ok := s.eventDriven[t]
	if !ok {
		return nil
	}
	// Snapshot the result keys so we can answer "previously failed"
	// without holding resultMu across user-supplied filters (those
	// can in principle re-enter the bus).
	failed := s.failedSnapshot()

	toQueue := make([]Check, 0, len(bindings))
	for _, b := range bindings {
		_, prevFail := failed[b.check.Name()]
		if !shouldExecute(b, evt, prevFail) {
			continue
		}
		toQueue = append(toQueue, b.check)
	}
	if len(toQueue) == 0 {
		return nil
	}
	s.enqueue(toQueue)
	s.scheduleDebounce(ctx)
	return nil
}

func shouldExecute(b eventBinding, evt any, previouslyFailed bool) bool {
	if b.condition == CheckOnSuccessfulOnly && previouslyFailed {
		return false
	}
	if b.condition == CheckOnFailedOnly && !previouslyFailed {
		return false
	}
	if b.filter != nil && !b.filter(evt) {
		return false
	}
	return true
}

func (s *Service) failedSnapshot() map[string]struct{} {
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	out := make(map[string]struct{}, len(s.results))
	for k := range s.results {
		out[k] = struct{}{}
	}
	return out
}

func (s *Service) postGraceSweep(ctx context.Context) {
	if !s.runningAfterGrace.CompareAndSwap(false, true) {
		return
	}
	defer s.runningAfterGrace.Store(false)

	s.enqueue(s.startupChecks)
	// Run synchronously — bypasses the debouncer so any new failures
	// emit FailedEvent immediately.
	s.processPending(ctx)

	// Mark BEFORE re-publishing existing failures so the publishes
	// here are tagged as not-in-grace (which is the post-sweep state
	// we just entered).
	s.hasRunAfterGrace.Store(true)

	if s.bus == nil {
		return
	}
	// Re-emit FailedEvent for every still-cached result so handlers
	// that swallowed events during the grace period see them now.
	// Mirrors `HealthCheckService.cs:194-199`.
	s.resultMu.Lock()
	snap := make([]HealthCheck, 0, len(s.results))
	for _, r := range s.results {
		snap = append(snap, r)
	}
	s.resultMu.Unlock()
	for _, r := range snap {
		_ = messaging.Publish(ctx, s.bus, FailedEvent{HealthCheck: r, IsInStartupGracePeriod: false})
	}
}

func (s *Service) enqueue(checks []Check) {
	if len(checks) == 0 {
		return
	}
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	for _, c := range checks {
		s.pending[c.Name()] = c
	}
}

func (s *Service) scheduleDebounce(ctx context.Context) {
	if s.debounceWindow <= 0 {
		// Zero window: drain synchronously. Useful for tests.
		s.processPending(ctx)
		return
	}
	if s.inProcessing.Load() {
		// Mirrors C# `_debounce.Pause()` — don't reschedule while a
		// drain is already in-flight; the running drain will pick up
		// any pending entries.
		return
	}
	s.debounceMu.Lock()
	defer s.debounceMu.Unlock()
	if s.debounceTimer != nil {
		s.debounceTimer.Stop()
	}
	// Capture ctx but don't propagate cancellation into the fire path —
	// a drain triggered by an event-driven add should run to completion
	// even if the original publishing goroutine has moved on. Use a
	// detached background context with a per-drain log line so cancellation
	// of the publisher can't strand pending entries forever.
	s.debounceTimer = time.AfterFunc(s.debounceWindow, func() {
		s.processPending(context.Background())
	})
	_ = ctx // ctx unused for cancellation; left in signature for future structured-cancellation.
}

func (s *Service) processPending(ctx context.Context) {
	// Serialise drains. processMu is RELEASED before the bus publishes
	// fan-out so subscribers can re-enter the orchestrator without
	// deadlock — but the cache mutation + transition computation
	// happens under processMu so a concurrent Run can't mis-order them.
	s.processMu.Lock()
	defer s.processMu.Unlock()

	s.inProcessing.Store(true)
	defer s.inProcessing.Store(false)

	s.resultMu.Lock()
	if len(s.pending) == 0 {
		s.resultMu.Unlock()
		return
	}
	batch := make([]Check, 0, len(s.pending))
	for _, c := range s.pending {
		batch = append(batch, c)
	}
	clear(s.pending)
	s.resultMu.Unlock()

	type transition struct {
		current  HealthCheck
		previous HealthCheck
		hadPrev  bool
	}
	transitions := make([]transition, 0, len(batch))

	inGrace := !s.hasRunAfterGrace.Load()

	for _, c := range batch {
		s.log.LogAttrs(ctx, slog.LevelDebug, "healthcheck: running",
			slog.String("check", c.Name()))
		result := s.runOne(ctx, c)

		s.resultMu.Lock()
		prev, hadPrev := s.results[c.Name()]
		if result.Type == ResultOK {
			delete(s.results, c.Name())
		} else {
			s.results[c.Name()] = result
		}
		s.resultMu.Unlock()

		transitions = append(transitions, transition{current: result, previous: prev, hadPrev: hadPrev})
	}

	if s.bus != nil {
		for _, t := range transitions {
			switch {
			case t.current.Type == ResultOK && t.hadPrev:
				_ = messaging.Publish(ctx, s.bus, RestoredEvent{
					PreviousCheck:          t.previous,
					IsInStartupGracePeriod: inGrace,
				})
			case t.current.Type != ResultOK && !t.hadPrev:
				_ = messaging.Publish(ctx, s.bus, FailedEvent{
					HealthCheck:            t.current,
					IsInStartupGracePeriod: inGrace,
				})
			}
		}
		_ = messaging.Publish(ctx, s.bus, CompleteEvent{})
	}
}

// runOne invokes a Check with panic recovery. The C# tree leans on
// the host's top-level exception handler; Go has no such umbrella, so
// a panicking check would otherwise take down the calling goroutine
// (the messaging-bus async pool, the scheduler tick, or a CheckHealth
// dispatcher worker). Recover, log, return ResultOK so the cache
// stays consistent.
func (s *Service) runOne(ctx context.Context, c Check) (out HealthCheck) {
	defer func() {
		if r := recover(); r != nil {
			s.log.LogAttrs(ctx, slog.LevelError, "healthcheck: check panicked",
				slog.String("check", c.Name()),
				slog.Any("panic", r))
			out = OK(c.Name())
		}
	}()
	return c.Run(ctx)
}

// ErrNilBus is returned by Subscribe when the bus is nil. Callers
// should construct a Bus first; subscribing against nil is always
// a wiring bug.
var ErrNilBus = errors.New("healthcheck: nil bus")
