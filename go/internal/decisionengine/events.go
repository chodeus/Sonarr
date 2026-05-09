package decisionengine

// EpisodeGrabbedEvent ports the C# event at
// `src/NzbDrone.Core/Download/EpisodeGrabbedEvent.cs:1-19`. Fired by
// the (Phase 4) download orchestrator after an approved release has
// been pushed to a download client. Subscribers — primarily
// [history.Service] — record the grab in the audit log.
//
// Co-located with [Subject] (the Go equivalent of C# `RemoteEpisode`)
// rather than the C# `Download/` namespace because the publisher will
// live in a Phase 4 download package and putting the event there
// would either need a duplicated narrow shape or would create an
// import edge that cycles through `decisionengine.Subject`. Keeping
// the event next to its main payload type avoids both problems and
// reads naturally — every consumer of the event needs to dereference
// `Episode` anyway.
//
// The Phase 4 publisher will fill the four download-client fields
// from the picked client; until then the event has no in-tree
// publisher (lit but no listeners on the publisher side, while
// subscribers on the consumer side wire up against this type so the
// dispatch path is exercised at boot).
type EpisodeGrabbedEvent struct {
	// Episode is the candidate Subject that was approved + grabbed.
	// Carries the full RemoteEpisode-equivalent context — Episodes,
	// Series, Release, ParsedEpisodeInfo, CustomFormats, etc.
	Episode *Subject
	// DownloadClientID is the Definitions row id of the picked
	// download client (e.g. the configured Sabnzbd / Transmission
	// instance). Used by listeners to attribute the grab to a
	// specific client config.
	DownloadClientID int
	// DownloadClient is the C# class-identifier of the picked client
	// (`"Sabnzbd"`, `"QBittorrent"`, etc.). Persisted to history
	// `Data["DownloadClient"]`.
	DownloadClient string
	// DownloadClientName is the user-configured display name of the
	// picked client. Persisted to history `Data["DownloadClientName"]`.
	DownloadClientName string
	// DownloadID is the per-grab token the download client returned
	// (Sabnzbd's nzo_id, Transmission's hash, etc.). Persisted on
	// the history Entry's DownloadID column so subsequent
	// import / failed / ignored events can be linked back to this
	// grab via the same download id.
	DownloadID string
}
