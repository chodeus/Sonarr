package blocklist

// BlocklistChangedEvent is a Go-port additive event fired by
// [Service.Delete], [Service.DeleteByIDs], and
// [Service.DeleteForSeries] after a successful repo removal.
//
// C# Sonarr's `BlocklistService.Delete` (`:165-173`) does NOT
// publish a typed lifecycle event. Its SignalR `blocklist` channel
// sync flows through the inherited generic
// `RestControllerWithSignalR.Handle(ModelEvent<Blocklist>)` — fired
// implicitly by the Dapper Repository on every delete. The Go port
// doesn't have a ModelEvent analogue, so the Service publishes its
// own typed event.
//
// Consumers: the SignalR `blocklist` channel Sync broadcaster
// (cmd/sonarr/signalr_wiring.go subscriber) — the React UI's
// blocklist list listens on Sync to refetch after a row is deleted.
//
// `IDs` carries the removed row IDs when the producer knows them
// (Delete → single, DeleteByIDs → input slice). DeleteForSeries
// can't easily enumerate without a pre-fetch (the repo issues a
// bulk WHERE seriesId IN (...) without returning affected IDs); it
// publishes with `IDs = nil` and a non-zero `Count`. Subscribers
// must not assume non-nil IDs.
//
// `Count` is the number of rows actually removed — useful for log
// lines and to gate the Sync broadcast (no rows removed → no event
// emitted by the producer, so subscribers see only meaningful
// transitions).
type BlocklistChangedEvent struct {
	IDs   []int
	Count int64
}
