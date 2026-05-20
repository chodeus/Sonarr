package customfilters

// CustomFilterUpdatedEvent is a Go-port additive event fired by
// [Service.Add], [Service.Update], and [Service.Delete] after a
// successful repo mutation.
//
// C# Sonarr's `CustomFilterService` (`CustomFilters/CustomFilterService.cs:1-49`)
// has no `_eventAggregator` and emits no IEvent — its SignalR sync
// flows through the inherited generic
// `RestControllerWithSignalR.Handle(ModelEvent<CustomFilter>)`
// fired implicitly by the Dapper Repository. The Go port doesn't
// have a ModelEvent analogue, so the Service publishes its own
// typed event.
//
// Consumers: the SignalR `customfilter` channel Sync broadcaster
// (cmd/sonarr/signalr_wiring.go subscriber). Payload-less because
// the React UI re-GETs the customfilter list on Sync; carrying the
// row would diverge from C#'s ResourceChange<Empty> Sync wire shape.
type CustomFilterUpdatedEvent struct{}
