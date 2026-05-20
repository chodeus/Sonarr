package delayprofiles

// DelayProfileUpdatedEvent is a Go-port additive event fired by
// [Service.Add], [Service.Update], [Service.Delete], and
// [Service.Reorder] after a successful repo mutation.
//
// C# Sonarr's `DelayProfileService` (`src/NzbDrone.Core/Profiles/Delay/`)
// does NOT publish a typed lifecycle event on any of the four CUD
// paths — confirmed in the service-level doc-comment in service.go.
// Its SignalR `delayprofile` channel sync flows through the
// inherited generic `RestControllerWithSignalR.Handle(
// ModelEvent<DelayProfile>)` — fired implicitly by the Dapper
// Repository. The Go port doesn't have a ModelEvent analogue, so
// the Service publishes its own typed event.
//
// Consumers: the SignalR `delayprofile` channel Sync broadcaster
// (cmd/sonarr/signalr_wiring.go subscriber) — the React UI's
// delay-profile list refetches on Sync. The event is intentionally
// payload-less (no per-profile fields surfaced) because the React
// UI re-GETs the list anyway; carrying the row would be Sonarr-side
// state that diverges from C#'s ResourceChange<Empty> wire shape on
// Sync paths.
type DelayProfileUpdatedEvent struct{}
