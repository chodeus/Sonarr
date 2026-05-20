package releaseprofiles

// ReleaseProfileUpdatedEvent is a Go-port additive event fired by
// [Service.Add], [Service.Update], and [Service.Delete] after a
// successful repo mutation.
//
// C# Sonarr's `ReleaseProfileService` (`Profiles/Releases/ReleaseProfileService.cs:1-79`)
// has no `_eventAggregator` and emits no IEvent — its SignalR sync
// flows through the inherited generic
// `RestControllerWithSignalR.Handle(ModelEvent<ReleaseProfile>)`
// fired implicitly by the Dapper Repository. The Go port doesn't
// have a ModelEvent analogue, so the Service publishes its own
// typed event.
//
// Consumers: the SignalR `releaseprofile` channel Sync broadcaster
// (cmd/sonarr/signalr_wiring.go subscriber). Payload-less because
// the React UI re-GETs the releaseprofile list on Sync.
type ReleaseProfileUpdatedEvent struct{}
