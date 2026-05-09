package trackeddownloads

import (
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/quality"
)

// DownloadFailedEvent ports the C# `DownloadFailedEvent : IEvent` at
// `src/NzbDrone.Core/Download/DownloadFailedEvent.cs:1-31`. Published
// by `FailedDownloadService` whenever the download client reports a
// terminal failure (corrupt extract, retry-exhausted, removed-by-user,
// etc.) — subscribed-to by [history.Service] (this slice's
// HandleDownloadFailed handler) AND blocklist.Service (sub-arc 10b)
// AND the Phase-4 notification dispatcher.
//
// Co-located with [TrackedDownload] (its main payload reference)
// rather than the C# `Download/` namespace because the publisher
// `FailedDownloadService` is Phase 4 territory but the event payload
// type is shared infrastructure that Phase-3 subscribers need at
// boot time. Putting it in the existing trackeddownloads package
// avoids creating a new package for one event and keeps the
// import-graph symmetrical with slice 3.AT — that slice put
// [decisionengine.EpisodeGrabbedEvent] alongside the Subject type;
// this slice puts the failed/ignored events alongside TrackedDownload.
//
// Field-shape parity with the C# class:
//
//   - SeriesId / EpisodeIds: the failed download's matched series +
//     episode set. The handler walks EpisodeIds and writes one
//     history.Entry per id.
//   - Quality / Languages / SourceTitle: release-shape carry-over
//     from the originating grab so the audit-log row preserves the
//     metadata the Grabbed row had.
//   - DownloadClient / DownloadId: provider identity strings — the
//     C# class-identifier ("QBittorrent" etc.) + the per-grab token.
//   - Message / Source: failure-detail strings the
//     FailedDownloadService composes (e.g. "Removed from queue
//     because of low priority"; Source is "FailedDownloadService" /
//     "ManualOverride" / etc.).
//   - Data: additional release metadata the producer tags inline
//     when TrackedDownload is nil (release-group / size / indexer
//     strings). The handler coalesces — TrackedDownload-derived
//     value first, Data-derived fallback second. Keys are
//     lowercase-first per C# `EpisodeHistory.cs:15-17`
//     ("releaseGroup" / "size" / "indexer"). nil-safe; handler
//     reads via map index which returns "" for missing keys.
//   - TrackedDownload: the in-memory tracked-download cache record
//     for the same DownloadID. Optional — manual-failed pushes
//     leave it nil.
//   - SkipRedownload: producer hint that the redownload-on-failure
//     orchestrator should skip this failure (e.g. user explicitly
//     marked the grab failed). The history handler ignores; the
//     blocklist + redownload subscribers consume it.
//   - ReleaseSource: where in the pipeline the originating release
//     entered (RSS / Search / etc.). Persisted history payload via
//     downstream subscribers.
//
// Wire-stable: the field names + JSON-tag shapes match the C#
// `JsonPropertyName` defaults. The Go port's local subscribers
// don't serialize the event, but a future cross-process bus would.
type DownloadFailedEvent struct {
	// SeriesID — the matched series for the failed download.
	SeriesID int
	// EpisodeIDs — the matched episode-id set. Handler walks this
	// list and writes one history.Entry per id. Empty list → no-op.
	EpisodeIDs []int
	// Quality — release quality at the time of grab.
	Quality quality.Model
	// SourceTitle — the original release title / grabbed name.
	SourceTitle string
	// DownloadClient — provider class-identifier
	// ("Sabnzbd" / "QBittorrent" / etc.).
	DownloadClient string
	// DownloadID — the per-grab token. Empty for manual-failed
	// pushes that don't carry a download-client item.
	DownloadID string
	// Message — failure-detail string composed by the producer.
	Message string
	// Source — origin tag — "FailedDownloadService" / "ManualOverride" etc.
	Source string
	// Data — fallback metadata when TrackedDownload is nil. Keys
	// are lowercase-first per `EpisodeHistory.cs:15-17`. nil-safe.
	Data map[string]string
	// TrackedDownload — in-memory cache record for the same
	// DownloadID. Optional; nil when the producer didn't have a
	// tracked match (manual failure of a row that never went
	// through the queue, etc.).
	TrackedDownload *TrackedDownload
	// Languages — per-release language set, persisted into the
	// history `Languages` column.
	Languages []language.Language
	// SkipRedownload — producer hint to bypass the
	// redownload-on-failure orchestrator. Mirrors C# `:28`.
	SkipRedownload bool
	// ReleaseSource — pipeline entry-point of the originating release.
	ReleaseSource decisionengine.ReleaseSourceType
}

// DownloadIgnoredEvent ports the C# `DownloadIgnoredEvent : IEvent`
// at `src/NzbDrone.Core/Download/DownloadIgnoredEvent.cs:1-21`.
// Published when the user explicitly tells Sonarr to forget about a
// tracked download (e.g. the "remove from queue" UI action with the
// "do not redownload" toggle). Subscribed-to by [history.Service]
// (this slice's HandleDownloadIgnored handler) AND blocklist.Service
// (sub-arc 10b for blocklist's per-handler IHandle).
//
// Field-shape parity with the C# class:
//
//   - SeriesID / EpisodeIDs: the matched series + episodes set. Handler
//     walks EpisodeIDs and writes one history.Entry per id.
//   - Languages: per-release language list persisted into the history
//     Languages column.
//   - Quality / SourceTitle: release-shape carry-over.
//   - DownloadClientInfo: the Type/Name pair for the picked client.
//     The C# tree derefs this UNCONDITIONALLY in the handler at
//     `HistoryService.cs:354-355` — no `?.` guard — so the producer
//     is expected to populate this. The Go-port handler stays
//     defensive (`failedClientName`-style nil-tolerance) since
//     test fixtures and future producers may diverge.
//   - DownloadID: the per-grab token.
//   - TrackedDownload: in-memory cache record for the same
//     DownloadID. Optional; nil for manual-ignored pushes that
//     don't have a matching tracked row.
//   - Message: human-readable explanation surfaced to the audit log.
//
// Wire-stable: the field names match the C# JsonPropertyName
// defaults. Like DownloadFailedEvent the local subscribers don't
// serialize the event but a future cross-process bus would.
type DownloadIgnoredEvent struct {
	// SeriesID — the matched series for the ignored download.
	SeriesID int
	// EpisodeIDs — the matched episode-id set. Handler writes one
	// history.Entry per id; empty list → no-op.
	EpisodeIDs []int
	// Languages — per-release language set.
	Languages []language.Language
	// Quality — release quality.
	Quality quality.Model
	// SourceTitle — original release title / grabbed name.
	SourceTitle string
	// DownloadClientInfo — provider Type + Name pair. Optional; nil
	// for manual-ignored pushes without a download-client item.
	DownloadClientInfo *ItemClientInfo
	// DownloadID — the per-grab token.
	DownloadID string
	// TrackedDownload — in-memory cache record for the same
	// DownloadID. Optional.
	TrackedDownload *TrackedDownload
	// Message — human-readable explanation.
	Message string
}

// ItemClientInfo aliases [downloadclients.ItemClientInfo] so the
// DownloadIgnoredEvent payload can name the type without re-importing
// downloadclients at every consumer. Defined here in trackeddownloads
// so the event-package boundary stays self-contained.
type ItemClientInfo = downloadclients.ItemClientInfo
