package trackeddownloads

import (
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
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

// DownloadCompletedEvent ports the C# `DownloadCompletedEvent : IEvent`
// at `src/NzbDrone.Core/Download/DownloadCompletedEvent.cs:1-24`.
// Published by `CompletedDownloadService` (Phase 4) after the
// download-client reports a completed payload that's been handed off
// to the import pipeline. Subscribed-to by [downloadhistory.Service]
// (slice 5.J140's HandleDownloadCompleted handler) — the audit log
// captures the lifecycle DownloadImported step against the same
// DownloadID the Grabbed row recorded.
//
// Field-shape parity with the C# class:
//
//   - TrackedDownload: the in-memory tracked-download cache record
//     for the completed item. The DownloadHistory handler reads
//     Protocol + DownloadClient (the int row id) + DownloadItem
//     (DownloadID, Title, DownloadClientInfo).
//   - SeriesID: the matched series.
//   - EpisodeFiles: the freshly-written EpisodeFile rows the import
//     pipeline produced. Not read by the DownloadHistory handler
//     but published for downstream subscribers (notifications,
//     metadata writers) that need the per-file context.
//   - Release: the originating GrabbedReleaseInfo so subscribers
//     can correlate completion with the grab event without a
//     history-lookup round-trip.
//
// Wire-stable: field names match the C# JsonPropertyName defaults.
// Like the sister Failed/Ignored events the local subscribers
// don't serialize today but a future cross-process bus would.
type DownloadCompletedEvent struct {
	// TrackedDownload — required. Producers that don't have a tracked
	// record should not publish (the C# tree never publishes without
	// one — CompletedDownloadService.cs:226+271 always pass a non-nil
	// TrackedDownload to the ctor).
	TrackedDownload *TrackedDownload

	// SeriesID — the completed download's matched series.
	SeriesID int

	// EpisodeFiles — freshly-imported file rows. Optional / nil for
	// pre-import dispatches; the DownloadHistory handler ignores.
	EpisodeFiles []episodefiles.File

	// Release — originating GrabbedReleaseInfo. Optional / nil when
	// the producer couldn't recover the originating grab metadata.
	Release *episodeimport.GrabbedReleaseInfo
}

// UntrackedDownloadCompletedEvent ports the C#
// `UntrackedDownloadCompletedEvent : IEvent` at
// `src/NzbDrone.Core/Download/UntrackedDownloadCompletedEvent.cs:9-25`.
// Published by `ManualImportService.Execute` (`ManualImportService.cs:580-595`)
// for files the user manually imported that had NO tracked download —
// loose files the download clients don't know about — grouped by the
// imported EpisodeFile's {SeriesId, SeasonNumber}. The tracked sibling
// is [DownloadCompletedEvent]; this carries the same per-completion
// context (matched series + episodes + freshly-written files + parsed
// info + the longest-common source path) so notification / metadata
// subscribers can react to a manual import the same way they react to a
// download-client completion.
//
// Field-shape parity with the C# class (`:11-15`):
//
//   - Series: the matched series.
//   - Episodes: the matched episodes across the group's files.
//   - EpisodeFiles: the freshly-written EpisodeFile rows.
//   - ParsedEpisodeInfo: the first file's FolderEpisodeInfo, falling
//     back to its FileEpisodeInfo (C# `:589`).
//   - SourcePath: the longest common path of the group's source files
//     (C# `GetLongestCommonPath`).
type UntrackedDownloadCompletedEvent struct {
	// Series — the matched series for the group. Required.
	Series *series.Series

	// Episodes — the matched episodes across the group's files.
	Episodes []episodes.Episode

	// EpisodeFiles — freshly-imported file rows for the group.
	EpisodeFiles []episodefiles.File

	// ParsedEpisodeInfo — folder parse (fallback file parse) of the
	// first file. Optional / nil when neither parse succeeded.
	ParsedEpisodeInfo *parser.ParsedEpisodeInfo

	// SourcePath — longest common source path of the group's files.
	SourcePath string
}

// DownloadCanBeRemovedEvent ports the C# `DownloadCanBeRemovedEvent : IEvent`
// at `src/NzbDrone.Core/Download/DownloadCanBeRemovedEvent.cs:1-15`.
// Published by `DownloadProcessingService.RemoveCompletedDownloads`
// (`:35-45`) once per process pass for every tracked download that is
// Imported AND the client now reports removable — the deferred-removal
// path for items that weren't removable at completion time (e.g. a
// torrent still seeding). The DownloadEventHub consumer removes it from
// the client when RemoveCompletedDownloads is set.
type DownloadCanBeRemovedEvent struct {
	// TrackedDownload — the Imported, now-removable download. Required.
	TrackedDownload *TrackedDownload
}

// DownloadsProcessedEvent ports the C# `DownloadsProcessedEvent : IEvent`
// at `src/NzbDrone.Core/Download/DownloadsProcessedEvent.cs:1-8` — a
// payloadless marker published by `DownloadProcessingService` at the end
// of every process pass (`DownloadProcessingService.cs:80`, after the
// per-tracked-download import/fail loop). Subscribed-to by the
// metadata/extras writer (`ExtraService.Handle(DownloadsProcessedEvent)`,
// `ExtraService.cs:173-191`): it drains the set of series that had a file
// imported since the previous pass and runs `CreateAfterEpisodesImported`
// on each (the once-per-batch series-level metadata refresh).
type DownloadsProcessedEvent struct{}

// TrackedDownloadRefreshedEvent ports the C#
// `TrackedDownloadRefreshedEvent : IEvent` at
// `src/NzbDrone.Core/Download/TrackedDownloads/TrackedDownloadRefreshedEvent.cs:1-15`.
// Published by `TrackedDownloadService.Refresh` on every poll cycle —
// carries the full current set of TrackedDownload rows so subscribers
// can re-project (queue.Service.HandleTrackedDownloadRefreshed in
// slice 3.BE) or recompute (notification dispatch) against the
// freshest snapshot.
//
// The C# tree carries TrackedDownloads as `List<TrackedDownload>`;
// the Go-port mirrors with a slice-of-pointers so subscribers can
// call IndexerFlags() and other receiver methods without a copy.
//
// Wire-stable: a single-field marker. Future Phase-4 publishers
// fill it from the Refresh poll output.
type TrackedDownloadRefreshedEvent struct {
	// TrackedDownloads — current snapshot of every tracked download
	// the service is aware of. May be empty (no active downloads).
	TrackedDownloads []*TrackedDownload
}
