package notifications

import (
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// Per-event payload types for the notification family.
//
// Sub-arc N2a of queue [N2] — the package-base slice ships the eight
// `*Message` records that the C# `INotification.OnX` hooks consume.
// Mirrors `src/NzbDrone.Core/Notifications/*Message.cs`. The Provider
// interface (queue [N2b], slice 4.AD) and the NotificationService
// fan-out (queue [N4]) both reach into these payloads at call sites.
//
// Cross-package coupling principles:
//
//   - Concrete Go-port types (series.Series, episodes.Episode,
//     episodefiles.File, episodefiles.DeletedFile, quality.Model,
//     healthcheck.HealthCheck) are imported directly. These have
//     all shipped pre-Phase-4 and aren't expected to change shape.
//   - Types that haven't ported yet (parser.RemoteEpisode,
//     parser.GrabbedReleaseInfo, MediaFiles.RenamedEpisodeFile,
//     Download.TrackedDownloads.TrackedDownload,
//     MediaFiles.LocalEpisode, Download.DownloadClientItemClientInfo)
//     are typed as `any` to keep the import graph clean. The
//     orchestrator (queue [N4]) builds these payloads at the
//     event-bus boundary; concrete providers in queue [N3] type-
//     assert when they reach for those fields.
//   - This mirrors the precedent set by
//     [downloadclients.GrabRequest.Subject] (`any` for the
//     decisionengine.Subject reference) at
//     `internal/downloadclients/provider.go:190-200`.

// GrabMessage is the per-event payload for the `OnGrab` hook —
// fired after the grab pipeline (queue [15] —
// ProcessDownloadDecisions) successfully hands a release to a
// download client. Mirrors C# `Notifications/GrabMessage.cs:7-21`.
type GrabMessage struct {
	// Message is the human-readable description the C# code at
	// `NotificationService.cs:84` builds via
	// `GetMessage(grabbedEvent.Episode.Series, grabbedEvent.Episode.Episodes, grabbedEvent.Episode.ParsedEpisodeInfo.Quality)`.
	// Most providers render this as the alert body.
	Message string

	// Series is the series the grabbed release belongs to.
	Series *series.Series

	// Episode is the post-parse remote-episode record (C# parity:
	// `Parser/Model/RemoteEpisode`). Typed as `any` until the
	// parser.RemoteEpisode struct ports — concrete providers type-
	// assert to reach Release/ParsedEpisodeInfo/Episodes.
	Episode any

	// Quality is the resolved post-parse quality model.
	Quality *quality.Model

	// DownloadClientType is the concrete client class label
	// (Implementation column on the DownloadClient row — e.g.
	// "Sabnzbd", "Transmission"). Used by some providers for
	// per-client log lines.
	DownloadClientType string

	// DownloadClientName is the user-facing display name of the
	// client (Name column on the DownloadClient row).
	DownloadClientName string

	// DownloadID is the client-side ID the grab pipeline received
	// from [downloadclients.Provider.Download] (NZB hash / torrent
	// info-hash).
	DownloadID string
}

// DownloadMessage is the per-event payload for the `OnDownload` /
// `OnUpgrade` hook — fired after the import pipeline finishes
// moving a downloaded file into the series library. Mirrors C#
// `Notifications/DownloadMessage.cs:9-25`.
type DownloadMessage struct {
	// Message is the human-readable description (C# parity: filled
	// at `NotificationService.cs:120`).
	Message string

	// Series is the series the imported file belongs to.
	Series *series.Series

	// EpisodeInfo is the post-import LocalEpisode record (C# parity:
	// `MediaFiles.LocalEpisode`). Typed as `any` until that struct
	// ports.
	EpisodeInfo any

	// EpisodeFile is the persisted EpisodeFiles row representing
	// the just-imported file.
	EpisodeFile *episodefiles.File

	// OldFiles are the prior EpisodeFile rows the import either
	// replaced (upgrade path) or merged with. May be empty.
	OldFiles []*episodefiles.DeletedFile

	// SourcePath is the on-disk path the file was imported FROM —
	// either the download client's output folder or a manual-import
	// drop. Used by providers that surface the source location in
	// their alert body (Notifiarr, Webhook).
	SourcePath string

	// DownloadClientInfo is the per-grab client snapshot the
	// orchestrator records on the queue Item. Typed as `any` until
	// the Download.DownloadClientItemClientInfo type ports — at the
	// Go-port boundary this is structurally the
	// downloadclients.ClientInfo shape plus the per-item Implementation
	// + ID pair.
	DownloadClientInfo any

	// DownloadID is the client-side ID from the grab event that
	// preceded this import. Empty for manual-import drops.
	DownloadID string

	// Release is the GrabbedReleaseInfo carried forward from the
	// matching grab history row. Typed as `any` until the
	// parser.GrabbedReleaseInfo struct ports.
	Release any
}

// ImportCompleteMessage is the per-event payload for the
// `OnImportComplete` hook — fired ONCE per import session after the
// per-file `OnDownload` hooks complete, summarising the batch.
// Mirrors C# `Notifications/ImportCompleteMessage.cs:11-28`.
type ImportCompleteMessage struct {
	// Message is the human-readable description.
	Message string

	// Series is the series the batch belongs to.
	Series *series.Series

	// Episodes is the set of episode rows the batch imported.
	Episodes []*episodes.Episode

	// EpisodeFiles is the set of persisted EpisodeFiles rows
	// created by the batch.
	EpisodeFiles []*episodefiles.File

	// SourcePath is the root path the batch was imported FROM.
	SourcePath string

	// SourceTitle is the human-readable label for the source —
	// typically the download-client item title.
	SourceTitle string

	// DownloadClientInfo — see [DownloadMessage.DownloadClientInfo].
	DownloadClientInfo any

	// DownloadID is the client-side ID.
	DownloadID string

	// Release — see [DownloadMessage.Release].
	Release any

	// DestinationPath is the final on-disk location the batch
	// imported INTO (typically the series root folder + season
	// subfolder).
	DestinationPath string

	// ReleaseGroup is the parsed release-group string (extracted by
	// parsingservice during the grab pipeline).
	ReleaseGroup string

	// ReleaseQuality is the quality the original release was
	// grabbed at. May differ from the per-file quality (e.g. a
	// repack of one episode in a season pack).
	ReleaseQuality *quality.Model
}

// EpisodeDeleteMessage is the per-event payload for the
// `OnEpisodeFileDelete` hook — fired when an EpisodeFile row is
// deleted, whether by manual user action or by an upgrade replacing
// it. Mirrors C# `Notifications/EpisodeDeleteMessage.cs:6-18`.
type EpisodeDeleteMessage struct {
	// Message is the human-readable description.
	Message string

	// Series is the series the deleted file belonged to.
	Series *series.Series

	// EpisodeFile is the deleted EpisodeFile row (carried forward
	// AFTER deletion so the payload still has the metadata).
	EpisodeFile *episodefiles.File

	// Episodes are the episodes that were linked to the deleted
	// EpisodeFile. Populated from `EpisodeFileDeletedEvent.
	// LinkedEpisodes` — the orchestrator looks them up once at
	// event-build time so per-provider handlers don't each query.
	// Used by providers that emit per-episode payloads (Trakt
	// AddEpisodeToCollection-style flows).
	Episodes []*episodes.Episode

	// Reason is the trigger that caused the deletion (manual,
	// upgrade, missing-from-disk, etc.). Mirrors the C# enum
	// `MediaFiles.DeleteMediaFileReason`.
	Reason episodefiles.DeleteMediaFileReason
}

// SeriesAddMessage is the per-event payload for the `OnSeriesAdd`
// hook — fired after a series is added to the library. Mirrors C#
// `Notifications/SeriesAddMessage.cs:5-13`.
type SeriesAddMessage struct {
	// Message is the human-readable description.
	Message string

	// Series is the just-added series.
	Series *series.Series
}

// SeriesDeleteMessage is the per-event payload for the
// `OnSeriesDelete` hook — fired after a series is removed from the
// library. Mirrors C# `Notifications/SeriesDeleteMessage.cs:5-25`.
//
// The C# class has a 2-arg constructor that derives Message and
// DeletedFilesMessage from (series, deleteFiles); the Go-port
// helper [NewSeriesDeleteMessage] mirrors that derivation so the
// orchestrator at queue [N4] can build the payload with one call.
type SeriesDeleteMessage struct {
	// Message is the human-readable description — typically
	// "<Title> - <DeletedFilesMessage>".
	Message string

	// Series is the just-removed series.
	Series *series.Series

	// DeletedFiles is true when the deletion was "delete files
	// from disk too" rather than "library remove only".
	DeletedFiles bool

	// DeletedFilesMessage is the human-readable sentence describing
	// whether files were deleted — either "Series removed and all
	// files were deleted" or "Series removed, files were not
	// deleted". Pre-derived by C# constructor at
	// `SeriesDeleteMessage.cs:18-22`.
	DeletedFilesMessage string
}

// NewSeriesDeleteMessage mirrors the C# constructor at
// `Notifications/SeriesDeleteMessage.cs:14-23`.
func NewSeriesDeleteMessage(s *series.Series, deleteFiles bool) SeriesDeleteMessage {
	var deletedFilesMessage string
	if deleteFiles {
		deletedFilesMessage = "Series removed and all files were deleted"
	} else {
		deletedFilesMessage = "Series removed, files were not deleted"
	}
	var title string
	if s != nil {
		title = s.Title
	}
	return SeriesDeleteMessage{
		Series:              s,
		DeletedFiles:        deleteFiles,
		DeletedFilesMessage: deletedFilesMessage,
		Message:             title + " - " + deletedFilesMessage,
	}
}

// ApplicationUpdateMessage is the per-event payload for the
// `OnApplicationUpdate` hook — fired after the in-process updater
// installs a new version. Mirrors C#
// `Notifications/ApplicationUpdateMessage.cs:5-13`.
//
// C# uses `System.Version` for the version fields; the Go port
// uses plain strings since version comparison happens upstream of
// this struct and the only consumer is alert-text rendering.
type ApplicationUpdateMessage struct {
	// Message is the human-readable description.
	Message string

	// PreviousVersion is the version that was running before the
	// update.
	PreviousVersion string

	// NewVersion is the version now installed.
	NewVersion string
}

// ManualInteractionRequiredMessage is the per-event payload for the
// `OnManualInteractionRequired` hook — fired when the download/
// import pipeline can't auto-import a download and needs the user
// to intervene. Mirrors C#
// `Notifications/ManualInteractionRequiredMessage.cs:8-23`.
type ManualInteractionRequiredMessage struct {
	// Message is the human-readable description.
	Message string

	// Series is the series the stalled download targets.
	Series *series.Series

	// Episode is the post-parse remote-episode record. Typed as
	// `any` — see [GrabMessage.Episode].
	Episode any

	// TrackedDownload is the per-download state record the import
	// pipeline maintains. Typed as `any` until the
	// Download.TrackedDownloads.TrackedDownload struct ports.
	TrackedDownload any

	// Quality is the resolved post-parse quality model.
	Quality *quality.Model

	// DownloadClientInfo — see [DownloadMessage.DownloadClientInfo].
	DownloadClientInfo any

	// DownloadID is the client-side ID.
	DownloadID string

	// Release — see [DownloadMessage.Release].
	Release any
}

// HealthCheckMessage carries a single [healthcheck.HealthCheck]
// result through the `OnHealthIssue` / `OnHealthRestored` hooks.
// The C# code passes the [HealthCheck.HealthCheck] value directly
// to the hook (not wrapped) — the Go port wraps it in this thin
// shell so the hook signature stays parallel to the other OnX
// payloads (one struct argument per hook).
//
// Mirrors the C# hook signatures at
// `Notifications/INotification.cs:19-20`:
//
//	void OnHealthIssue(HealthCheck.HealthCheck healthCheck);
//	void OnHealthRestored(HealthCheck.HealthCheck previousCheck);
type HealthCheckMessage struct {
	// Check is the health-check result the hook fires for. For
	// OnHealthRestored this is the PRIOR check (the one that just
	// flipped from failing to ok) per the C# `previousCheck` arg
	// name.
	Check healthcheck.HealthCheck
}
