// In-memory dispatcher. Ports
// `NzbDrone.Core.Messaging.Commands.CommandExecutor` at
// `src/NzbDrone.Core/Messaging/Commands/CommandExecutor.cs:1-143`.
//
// The C# CommandExecutor:
//
//   - Spawns 3 worker threads on ApplicationStartedEvent.
//   - Each thread loops over `_commandQueueManager.Queue(token)` —
//     the blocking-consumer enumerable backed by `CommandQueue`.
//   - For each command, calls IServiceFactory.Build(typeof(IExecute<T>))
//     to discover a per-command handler, invokes
//     CommandQueueManager.Start, runs handler.Execute, then either
//     CommandQueueManager.Complete or .Fail. Publishes
//     CommandExecutedEvent in a `finally` block.
//
// Phase 3.11 ships a single-worker dispatcher. Per-handler concurrency
// (the C# THREAD_LIMIT = 3) is deferred. Cancellation propagation from
// Service.Cancel into a running handler is also deferred — pick the
// Go pattern in 3.12.
//
// Reflection-based IExecute<T> dispatch is replaced with an explicit
// Name-keyed handler registry. The host registers per-command-type
// handlers + their behaviour flags at boot via [CommandSpec] entries
// passed to [NewQueue] and [NewDispatcher] (or shared through a
// single registry helper — see [CommandRegistry]).

package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
)

// Handler is the contract every IExecute<T>-equivalent satisfies.
// Body is the JSON-encoded command payload — the same string the
// host produced when calling [Service.Push]. Decoding is the
// handler's responsibility (so the registry doesn't need to know
// about every command's payload type).
//
// Returning a non-nil error transitions the row to StatusFailed and
// stamps the error string into Command.Exception via [Service.Fail].
// Panics inside Execute are recovered and treated as a Fail with the
// recovered value + stack trace.
type Handler interface {
	Execute(ctx context.Context, body string) error
}

// HandlerFunc is a func adapter for Handler.
type HandlerFunc func(ctx context.Context, body string) error

// Execute invokes the wrapped function.
func (f HandlerFunc) Execute(ctx context.Context, body string) error {
	return f(ctx, body)
}

// CommandSpec carries the per-command-type registration: the runtime
// handler and the three eligibility flags the queue's TryGet logic
// needs. One entry per command Name.
type CommandSpec struct {
	Handler            Handler
	RequiresDiskAccess bool
	IsExclusive        bool
	IsLongRunning      bool
}

// CommandRegistry is the host-owned map of Name → CommandSpec. The
// same registry feeds both [Queue.props] and [Dispatcher.specs] so
// the eligibility flags and the handler stay aligned. Nil values are
// rejected at registration.
type CommandRegistry struct {
	specs map[string]CommandSpec
}

// NewRegistry builds an empty registry.
func NewRegistry() *CommandRegistry {
	return &CommandRegistry{specs: map[string]CommandSpec{}}
}

// Register adds (or replaces) a spec. Panics on empty name or nil
// handler — registration is a boot-time setup error, not a runtime
// condition.
func (r *CommandRegistry) Register(name string, spec CommandSpec) {
	if name == "" {
		panic("commands: Register: empty name")
	}
	if spec.Handler == nil {
		panic("commands: Register: nil handler for " + name)
	}
	r.specs[name] = spec
}

// Properties returns the eligibility flags for a name; zero value
// when missing (the queue treats unregistered commands as not-disk /
// not-exclusive / not-long-running, the same as the C# default for
// any command that doesn't override the virtual properties).
func (r *CommandRegistry) Properties(name string) QueueProperties {
	spec, ok := r.specs[name]
	if !ok {
		return QueueProperties{}
	}
	return QueueProperties{
		RequiresDiskAccess: spec.RequiresDiskAccess,
		IsExclusive:        spec.IsExclusive,
		IsLongRunning:      spec.IsLongRunning,
	}
}

// Lookup returns the spec for a name; ok=false when missing. The
// dispatcher uses this to find the handler at execute time; a missing
// handler causes a Fail with ErrNoHandler so the row exits the queue
// instead of looping forever.
func (r *CommandRegistry) Lookup(name string) (CommandSpec, bool) {
	spec, ok := r.specs[name]
	return spec, ok
}

// CanonicalName looks up the registered name case-insensitively and
// returns the canonical casing. Used by the /api/v3/command POST
// handler to validate the inbound name. Mirrors the C# `KnownTypes`
// lookup at `CommandController.cs:56-59`:
//
//	_knownTypes.GetImplementations(typeof(Command))
//	    .Single(c => c.Name.Replace("Command", "")
//	                  .Equals(resource.Name, InvariantCultureIgnoreCase));
func (r *CommandRegistry) CanonicalName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	for k := range r.specs {
		if strings.EqualFold(k, name) {
			return k, true
		}
	}
	return "", false
}

// ErrNoHandler is recorded as Command.Exception when the dispatcher
// pulls a row whose Name is not in the registry.
var ErrNoHandler = errors.New("commands: no handler registered for command")

// Dispatcher is the single-worker drain loop. One Dispatcher per
// process; the host calls [Dispatcher.Run] on its own goroutine and
// cancels the ctx to shut down.
type Dispatcher struct {
	svc      *Service
	queue    *Queue
	registry *CommandRegistry
	log      *slog.Logger
}

// NewDispatcher builds a Dispatcher. svc, queue, and registry must
// all be non-nil; log defaults to slog.Default when nil.
func NewDispatcher(svc *Service, queue *Queue, registry *CommandRegistry, log *slog.Logger) (*Dispatcher, error) {
	if svc == nil {
		return nil, errors.New("commands: NewDispatcher: nil service")
	}
	if queue == nil {
		return nil, errors.New("commands: NewDispatcher: nil queue")
	}
	if registry == nil {
		return nil, errors.New("commands: NewDispatcher: nil registry")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{svc: svc, queue: queue, registry: registry, log: log}, nil
}

// Run drains the queue until ctx is cancelled or the queue is closed.
// Returns nil on clean shutdown (ctx.Err() or ErrQueueClosed); other
// queue errors propagate. Per-command errors are absorbed via
// Service.Fail and never cause Run to exit.
func (d *Dispatcher) Run(ctx context.Context) error {
	for {
		c, err := d.queue.Pull(ctx)
		if err != nil {
			if errors.Is(err, ErrQueueClosed) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return err
		}
		d.dispatch(ctx, c)
	}
}

// dispatch executes one pulled command end-to-end.
//
// Sequence mirrors `CommandExecutor.cs:65-116`:
//  1. Lookup handler. Missing → Fail(ErrNoHandler).
//  2. Service.Start (re-stamps StartedAt + persists; the queue
//     already set Status=Started in TryGet).
//  3. handler.Execute, panic-recovered.
//  4. Service.Complete on success / Service.Fail on error.
//  5. RemoveCompleted from the in-memory queue.
//
// Service.Complete/Fail publishes CommandExecutedEvent (already
// shipped in 3.9), so we don't publish here.
func (d *Dispatcher) dispatch(ctx context.Context, c *Command) {
	spec, ok := d.registry.Lookup(c.Name)
	if !ok {
		d.log.LogAttrs(ctx, slog.LevelError, "commands: no handler registered",
			slog.String("command", c.Name),
			slog.Int("id", c.ID))
		if err := d.svc.Fail(ctx, c, "Failed", fmt.Sprintf("%s: %s", ErrNoHandler.Error(), c.Name)); err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "commands: Fail after no-handler",
				slog.String("command", c.Name),
				slog.Int("id", c.ID),
				slog.Any("err", err))
		}
		d.queue.RemoveCompleted(c.ID)
		return
	}

	if err := d.svc.Start(ctx, c); err != nil {
		d.log.LogAttrs(ctx, slog.LevelError, "commands: Start failed",
			slog.String("command", c.Name),
			slog.Int("id", c.ID),
			slog.Any("err", err))
		d.queue.RemoveCompleted(c.ID)
		return
	}

	execErr := runHandler(ctx, spec.Handler, c.Body)

	if execErr != nil {
		d.log.LogAttrs(ctx, slog.LevelError, "commands: handler error",
			slog.String("command", c.Name),
			slog.Int("id", c.ID),
			slog.Any("err", execErr))
		if err := d.svc.Fail(ctx, c, "Failed", execErr.Error()); err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "commands: Fail persist",
				slog.String("command", c.Name),
				slog.Int("id", c.ID),
				slog.Any("err", err))
		}
	} else {
		if err := d.svc.Complete(ctx, c, ""); err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "commands: Complete persist",
				slog.String("command", c.Name),
				slog.Int("id", c.ID),
				slog.Any("err", err))
		}
	}
	d.queue.RemoveCompleted(c.ID)
}

// runHandler invokes the handler with panic recovery. A recovered
// panic is converted into an error carrying the panic value and the
// stack trace at the panic site.
func runHandler(ctx context.Context, h Handler, body string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("commands: handler panicked: %v\n%s", r, debug.Stack())
		}
	}()
	return h.Execute(ctx, body)
}
