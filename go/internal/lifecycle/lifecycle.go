// Package lifecycle holds the in-process lifecycle event types Sonarr
// publishes during startup and shutdown. Mirrors
// NzbDrone.Core.Lifecycle:
//
//   - ApplicationStartingEvent — fired before any service has run.
//   - ApplicationStartedEvent — fired after every IHandle has been
//     resolved and the bus is hot. Many subscribers seed state here
//     (UserService loads XML user, ConfigFileProvider migrates +
//     ensures defaults, ProgressMessageTarget self-registers in the
//     NLog pipeline, etc.).
//   - ApplicationShutdownRequested — published by the host (or by
//     RestartCommand / ShutdownCommand) when termination begins.
//
// All three are tagged `[LifecycleEvent]` in the C# tree, which the
// EventAggregator uses to block re-publication once the bus has
// already begun shutdown. The Go port mirrors this via the
// `Lifecycle() bool` interface from `internal/messaging` — each event
// type below returns true so `messaging.Bus.BeginShutdown()` blocks
// further publishes of these specific events.
//
// The event types are deliberately tiny structs. Callers Subscribe
// against the concrete type:
//
//	messaging.Subscribe(bus, "myservice.start",
//	    func(ctx context.Context, _ lifecycle.ApplicationStartedEvent) error {
//	        return svc.OnAppStarted(ctx)
//	    })
package lifecycle

// ApplicationStartingEvent is published before any IHandle has been
// resolved. Subscribers run in the bus's bounded async pool. Sonarr's
// C# tree has no production subscribers for this event; it exists for
// hosts that want to gate work on "the bus exists" without waiting
// for `Started`.
type ApplicationStartingEvent struct{}

// Lifecycle marks ApplicationStartingEvent as a lifecycle event for
// the messaging bus's shutdown gate.
func (ApplicationStartingEvent) Lifecycle() bool { return true }

// ApplicationStartedEvent is published once startup is complete and
// the bus is hot. Use this to lazily migrate state, register NLog
// targets, populate caches, or run "first user" seeding.
type ApplicationStartedEvent struct{}

// Lifecycle marks ApplicationStartedEvent as a lifecycle event.
func (ApplicationStartedEvent) Lifecycle() bool { return true }

// ApplicationShutdownRequested signals that the host is shutting
// down. The Restarting flag distinguishes a restart (host will spawn
// a fresh process after this one exits) from a clean shutdown.
type ApplicationShutdownRequested struct {
	Restarting bool
}

// Lifecycle marks ApplicationShutdownRequested as a lifecycle event.
func (ApplicationShutdownRequested) Lifecycle() bool { return true }
