package remotepathmappings

// RemotePathMappingUpdatedEvent is a Go-port additive event fired by
// [Service.Add], [Service.Update], and [Service.Remove] after a
// successful repo mutation.
//
// C# Sonarr's `RemotePathMappingService` has no `_eventAggregator`
// and emits no IEvent on CUD — its SignalR sync flows through the
// inherited generic
// `RestControllerWithSignalR.Handle(ModelEvent<RemotePathMapping>)`
// fired implicitly by the Dapper Repository. The Go port doesn't
// have a ModelEvent analogue, so the Service publishes its own
// typed event.
//
// Consumers: the SignalR `remotepathmapping` channel Sync
// broadcaster (cmd/sonarr/signalr_wiring.go subscriber). Payload-
// less because the React UI re-GETs the mapping list on Sync.
type RemotePathMappingUpdatedEvent struct{}
