package healthcheck

// FailedEvent is published when a check's result transitions from
// passing to failing. Mirrors
// `src/NzbDrone.Core/HealthCheck/HealthCheckFailedEvent.cs`. The C#
// tree has subscribers in NotificationService and the SignalR hub.
//
// IsInStartupGracePeriod is true when the bus is still in the 15-min
// post-startup window — notification subscribers use this to suppress
// alerts that would otherwise spam users on every restart while
// transient services (mounts, indexers) come online.
type FailedEvent struct {
	HealthCheck            HealthCheck
	IsInStartupGracePeriod bool
}

// RestoredEvent is published when a previously-failing check returns
// ResultOK. PreviousCheck carries the last-known failing result so
// notification handlers can render "X is back online" messages with
// the original failure context.
//
// Mirrors `HealthCheckRestoredEvent.cs:1-16`.
type RestoredEvent struct {
	PreviousCheck          HealthCheck
	IsInStartupGracePeriod bool
}

// CompleteEvent is published once after every batch of checks has
// finished. Marker event with no payload — the SignalR hub uses it to
// trigger a cache invalidation on connected clients without having to
// inspect every Failed / Restored fan-out.
//
// Mirrors `HealthCheckCompleteEvent.cs:1-8`.
//
// CompleteEvent is intentionally NOT tagged Lifecycle — it's published
// during normal operation, not just startup/shutdown.
type CompleteEvent struct{}
