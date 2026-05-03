// Package messaging is the Go port of NzbDrone.Core.Messaging.Events —
// the in-process event bus that fans out every domain event Sonarr
// raises (60+ types per analysis/subsystems/messaging.md) to all
// subscribed handlers.
//
// # Behavioural parity vs C#
//
// The C# bus (`EventAggregator`) discovers handlers by reflection over
// the DryIoc DI container — every concrete class implementing
// `IHandle<TEvent>` is automatically subscribed. Go has no equivalent
// auto-scan; subscription is explicit. Beyond that registration shift,
// the publish-time semantics match the C# behaviour the messaging
// subsystem card describes:
//
//   - Sync handlers run on the publishing goroutine, ordered
//     First → Any → Last (C# `EventHandleOrder`).
//   - Async handlers run on a bounded worker pool. C# uses
//     unbounded `Task.Factory.StartNew`; per the MSG-Q9 resolution
//     (analysis/questions.md) we cap at `runtime.NumCPU()*2`.
//   - A "global" subscriber kind, registered via SubscribeAll, fires
//     for every event. Mirrors the `IHandleAsync<IEvent>` shape; the
//     sole production user is `HealthCheckService`.
//   - Per-handler panics are recovered, logged via slog, and do not
//     stop subsequent handlers — matches the C# per-handler try/catch.
//   - Lifecycle events (those implementing `Lifecycle() bool` returning
//     true) are blocked once BeginShutdown is called. C# uses the
//     `[LifecycleEvent]` attribute; the Go port uses an interface
//     check, which is the closest idiomatic equivalent.
//   - Re-entrant publish from inside a handler is allowed. The bus
//     adds a recursion-depth guard via context (MSG-Q10 resolution)
//     so a runaway cycle blows up with a clear error instead of an
//     unbounded stack.
//
// # Generics shape
//
// Go cannot place type-parameters on interface methods, so Subscribe /
// SubscribeAsync / Publish are top-level generic functions taking the
// Bus as their first argument. Subscribers are stored internally
// keyed by `reflect.Type`; the generic helpers wrap the typed handler
// in a `func(ctx, any)` adapter that does a single type assertion at
// dispatch time.
//
// # What this package does NOT do
//
// No persistence. No outbox. No cross-process delivery. The
// SignalR fan-out lives downstream in Phase 5; ProgressMessaging
// (`CommandUpdatedEvent` from inside an NLog target) is owned by the
// command-bus port in Phase 3 once Commands has a real executor.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// Order controls the relative position of a sync handler within its
// per-event subscriber list. Sync handlers execute First → Any → Last;
// within an Order tier, registration order wins.
type Order int

const (
	// OrderFirst makes the handler run before all OrderAny handlers.
	// Use sparingly — it represents a hard cross-handler dependency.
	OrderFirst Order = 0
	// OrderAny is the default. Handlers in this tier run in
	// registration order.
	OrderAny Order = 1
	// OrderLast makes the handler run after all OrderAny handlers.
	// Used in the C# tree by RedownloadFailedDownloadService and
	// MediaFileDeletionService so they observe handlers that record
	// state-of-the-world before they act.
	OrderLast Order = 2
)

// Lifecycle is satisfied by event types that should be blocked from
// publishing once BeginShutdown was called. Mirrors the C# tree's
// `[LifecycleEvent]` attribute. Examples in the C# code:
// ApplicationStartedEvent, ApplicationStartingEvent,
// ApplicationShutdownRequested.
type Lifecycle interface {
	Lifecycle() bool
}

// Bus is one in-process event bus. Construct via New. Goroutine-safe.
//
// A typical process owns exactly one Bus, wired into every service
// constructor. Tests should construct their own Bus to avoid leaking
// subscribers across test cases.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[reflect.Type]*subscriberSet
	global      []handlerEntry

	exiting atomic.Bool

	// pending tracks in-flight async dispatches so Wait can drain at
	// shutdown without dropping work.
	pending sync.WaitGroup

	// asyncSlots is a counting semaphore that caps concurrent async
	// handler invocations at runtime.NumCPU()*2 (MSG-Q9 resolution).
	// A goroutine acquires a slot before invoking the handler and
	// releases on completion or panic.
	asyncSlots chan struct{}

	log *slog.Logger
}

// subscriberSet carries the per-event-type subscriber lists. Stored
// by Bus.subscribers under reflect.TypeOf(event). Sync handlers are
// pre-sorted by Order at registration time so the publish path
// doesn't need to sort.
type subscriberSet struct {
	sync  []handlerEntry
	async []handlerEntry
}

type handlerEntry struct {
	name  string // "package.func" or caller-supplied; used in logs.
	order Order
	fn    func(context.Context, any) error
}

// New constructs an empty Bus. log is used to emit one warning per
// recovered handler panic and one error per unhandled handler error;
// pass slog.Default if you don't have a per-process logger yet.
func New(log *slog.Logger) *Bus {
	if log == nil {
		log = slog.Default()
	}
	return &Bus{
		subscribers: map[reflect.Type]*subscriberSet{},
		asyncSlots:  make(chan struct{}, asyncCap()),
		log:         log,
	}
}

func asyncCap() int {
	n := runtime.NumCPU() * 2
	if n < 2 {
		return 2
	}
	return n
}

// BeginShutdown marks the bus as exiting. Subsequent Publish calls for
// event types satisfying Lifecycle will return ErrShutdown without
// fanning out. Non-lifecycle events continue to publish normally
// during shutdown so consumers (e.g. HistoryService writing the last
// few events) can finish their work.
func (b *Bus) BeginShutdown() {
	b.exiting.Store(true)
}

// Wait blocks until every async handler currently in flight has
// returned. Call after BeginShutdown to drain the bus before
// terminating the process. New async handlers spawned after Wait
// returns are not tracked (don't publish from a shutdown hook unless
// you want races with Wait).
func (b *Bus) Wait() {
	b.pending.Wait()
}

// IsExiting reports whether BeginShutdown has been called. Exposed for
// tests and for handlers that want to short-circuit slow work during
// shutdown.
func (b *Bus) IsExiting() bool { return b.exiting.Load() }

// ErrShutdown is returned from Publish when the bus has begun
// shutdown and the event type is a Lifecycle event.
var ErrShutdown = errors.New("messaging: bus is shutting down (lifecycle event blocked)")

// ErrRecursionLimit is returned from Publish if the publish-from-handler
// chain exceeds the per-call recursion budget. The C# bus has no
// such guard and will overflow the stack on a runaway cycle; the Go
// port adds a depth limit (MSG-Q10 resolution) so accidents fail
// loudly instead of silently.
var ErrRecursionLimit = errors.New("messaging: publish recursion exceeded limit")

// recursionLimit caps publish-from-handler depth. 32 levels is far
// above any legitimate use (the deepest production cycle in the C#
// tree is the ProgressMessageTarget self-publish, which is exactly
// 1 level deep with its own reentrancy lock).
const recursionLimit = 32

type recursionKey struct{}

// Subscribe registers a sync handler for events of type E. Handler
// returns are logged (via slog) but not propagated; callers that need
// "cancel further handlers on error" semantics should use a
// per-handler context cancellation instead.
//
// name is shown in logs and is optional (empty falls back to the
// handler's reflect-derived function name).
func Subscribe[E any](b *Bus, name string, h func(context.Context, E) error) {
	subscribeOrdered(b, name, OrderAny, h, false)
}

// SubscribeOrdered is Subscribe with an explicit ordering tier.
func SubscribeOrdered[E any](b *Bus, name string, order Order, h func(context.Context, E) error) {
	subscribeOrdered(b, name, order, h, false)
}

// SubscribeAsync registers a fire-and-forget handler. On Publish, the
// handler is dispatched onto the bus's bounded worker pool and the
// publishing goroutine returns immediately (after sync handlers
// complete).
func SubscribeAsync[E any](b *Bus, name string, h func(context.Context, E) error) {
	subscribeOrdered(b, name, OrderAny, h, true)
}

// SubscribeAllAsync registers a global async handler that fires for
// every event. Mirrors the C# `IHandleAsync<IEvent>` shape.
// HealthCheckService is the canonical user (one global handler taps
// every event to drive `[CheckOn(...)]` checks).
func SubscribeAllAsync(b *Bus, name string, h func(context.Context, any) error) {
	if name == "" {
		name = "<anonymous>"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.global = append(b.global, handlerEntry{name: name, order: OrderAny, fn: h})
}

func subscribeOrdered[E any](b *Bus, name string, order Order, h func(context.Context, E) error, isAsync bool) {
	if h == nil {
		panic("messaging: nil handler")
	}
	if name == "" {
		name = handlerNameFromFunc(h)
	}
	t := reflect.TypeOf((*E)(nil)).Elem()
	entry := handlerEntry{
		name:  name,
		order: order,
		fn: func(ctx context.Context, e any) error {
			ev, ok := e.(E)
			if !ok {
				return fmt.Errorf("messaging: type assertion failed for %T → %s", e, t)
			}
			return h(ctx, ev)
		},
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	set, ok := b.subscribers[t]
	if !ok {
		set = &subscriberSet{}
		b.subscribers[t] = set
	}
	if isAsync {
		set.async = append(set.async, entry)
		sortByOrder(set.async)
	} else {
		set.sync = append(set.sync, entry)
		sortByOrder(set.sync)
	}
}

// sortByOrder sorts entries by Order ascending, stable so registration
// order wins within a tier.
func sortByOrder(es []handlerEntry) {
	sort.SliceStable(es, func(i, j int) bool { return es[i].order < es[j].order })
}

// Publish fans an event out to all sync, async, and global subscribers
// for its type. Returns ErrShutdown for Lifecycle events when the bus
// is exiting; ErrRecursionLimit if the publish chain exceeds the
// guard depth; otherwise nil. Handler errors are logged but never
// returned — sync handlers proceed even if an earlier one failed,
// matching the C# per-handler try/catch.
func Publish[E any](ctx context.Context, b *Bus, event E) error {
	if l, ok := any(event).(Lifecycle); ok && l.Lifecycle() && b.exiting.Load() {
		return ErrShutdown
	}
	depth, _ := ctx.Value(recursionKey{}).(int)
	if depth >= recursionLimit {
		return fmt.Errorf("%w: depth=%d type=%T", ErrRecursionLimit, depth, event)
	}
	ctx = context.WithValue(ctx, recursionKey{}, depth+1)

	t := reflect.TypeOf((*E)(nil)).Elem()
	b.mu.RLock()
	set := b.subscribers[t]
	global := b.global
	b.mu.RUnlock()

	// Sync handlers — run on the calling goroutine, in order, with
	// per-handler panic recovery. Errors are logged and dispatch
	// continues.
	if set != nil {
		for _, h := range set.sync {
			b.invokeSync(ctx, h, event)
		}
	}

	// Async + global — spawn one goroutine per handler, bounded by
	// asyncSlots. Track via pending for shutdown drain.
	if set != nil {
		for _, h := range set.async {
			b.dispatchAsync(ctx, h, event)
		}
	}
	for _, h := range global {
		b.dispatchAsync(ctx, h, event)
	}
	return nil
}

func (b *Bus) invokeSync(ctx context.Context, h handlerEntry, event any) {
	defer func() {
		if r := recover(); r != nil {
			b.log.LogAttrs(ctx, slog.LevelError, "messaging: sync handler panicked",
				slog.String("handler", h.name),
				slog.String("event", fmt.Sprintf("%T", event)),
				slog.Any("panic", r))
		}
	}()
	if err := h.fn(ctx, event); err != nil {
		b.log.LogAttrs(ctx, slog.LevelWarn, "messaging: sync handler returned error",
			slog.String("handler", h.name),
			slog.String("event", fmt.Sprintf("%T", event)),
			slog.Any("err", err))
	}
}

func (b *Bus) dispatchAsync(ctx context.Context, h handlerEntry, event any) {
	b.pending.Add(1)
	go func() {
		defer b.pending.Done()
		// Acquire a slot. Capacity is bounded; this blocks if the
		// pool is saturated, which is the desired back-pressure
		// behaviour at MSG-Q9.
		b.asyncSlots <- struct{}{}
		defer func() { <-b.asyncSlots }()

		defer func() {
			if r := recover(); r != nil {
				b.log.LogAttrs(ctx, slog.LevelError, "messaging: async handler panicked",
					slog.String("handler", h.name),
					slog.String("event", fmt.Sprintf("%T", event)),
					slog.Any("panic", r))
			}
		}()
		if err := h.fn(ctx, event); err != nil {
			b.log.LogAttrs(ctx, slog.LevelWarn, "messaging: async handler returned error",
				slog.String("handler", h.name),
				slog.String("event", fmt.Sprintf("%T", event)),
				slog.Any("err", err))
		}
	}()
}

// handlerNameFromFunc derives a human-readable name from a func value
// using runtime introspection. Falls back to "<anonymous>" if the
// runtime can't resolve a name (rare — usually only happens for
// closures synthesised by go test -cover).
func handlerNameFromFunc(fn any) string {
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func {
		return "<anonymous>"
	}
	pc := v.Pointer()
	if pc == 0 {
		return "<anonymous>"
	}
	if rfn := runtime.FuncForPC(pc); rfn != nil {
		return rfn.Name()
	}
	return "<anonymous>"
}
