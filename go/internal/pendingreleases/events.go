package pendingreleases

// PendingReleasesUpdatedEvent fires after a Service write batch
// (Add/AddMany, plus the C7b Remove paths once they land) mutates
// the PendingReleases table. Mirrors C# `PendingReleasesUpdatedEvent`
// at `src/NzbDrone.Core/Download/Pending/PendingReleasesUpdatedEvent.cs:1-8`
// — a marker event with no payload.
//
// Subscribers historically include the SignalR queue projection
// (UI auto-refresh) and the Indexer-status check; in the Go port,
// dispatcher fan-out lands alongside the C6 IHandle wiring.
type PendingReleasesUpdatedEvent struct{}
