package updatehistory

import "context"

// UpdateInstalledEvent ports C# `Update.History.Events.
// UpdateInstalledEvent` (`Update/Events/UpdateInstalledEvent.cs`). It
// is published once on application start when the running version
// differs from the previously-recorded install — i.e. the app has just
// been updated. Consumed by notifications.Service to fire the
// OnApplicationUpdate hook.
//
// C# uses `System.Version` for the version fields; the Go port carries
// the version strings verbatim (the only consumer renders alert text).
// Note the C# property name `PreviousVerison` is a long-standing typo
// in the source; the Go field uses the correct spelling.
type UpdateInstalledEvent struct {
	// PreviousVersion is the version that was running before the update.
	PreviousVersion string
	// NewVersion is the version now installed.
	NewVersion string
}

// Publisher publishes an UpdateInstalledEvent. Production binds a
// closure over messaging.Publish + the bus; tests can capture. Optional
// — when unset (or nil) no event is published (faithful to C#
// HandleAsync only publishing when `_prevVersion != null`, which in the
// Go port additionally requires the publisher to be wired).
type Publisher func(ctx context.Context, ev UpdateInstalledEvent) error
