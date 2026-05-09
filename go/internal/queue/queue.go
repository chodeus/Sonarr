// Package queue is the Go port of NzbDrone.Core.Queue — the in-memory
// projection of the active download queue. The TrackedDownloadService
// publishes a TrackedDownloadRefreshedEvent on every poll cycle; the
// queue.Service consumes it and re-projects the live download client
// items into the user-visible queue rows the v3 API + frontend
// surface.
//
// Schema NOTE: queue rows are NOT persisted. They live in an
// RWMutex-protected in-memory cache keyed by Queue.ID. The Id is
// derived from the trackeddownload metadata (a stable hash) so the
// same physical download keeps its queue id across refresh cycles.
//
// Sub-arc 11a (this slice) ships the [Queue] data type + the
// [QueueStatus] enum + the read-only [Service] surface
// (GetQueue / Find). Subsequent sub-arcs add the write surface (11b
// — Remove + the cache-set used by the IHandle), the
// IHandle<TrackedDownloadRefreshedEvent> port (11c), and the host
// wire + decisionengine.QueueSpec port (11d).
package queue

import (
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// QueueStatus mirrors `Queue/QueueStatus.cs:1-15`. Tracks the
// user-visible status of a queue row independent of the
// TrackedDownload state machine — surfaces "Queued / Paused /
// Downloading / Completed / Failed / Warning / Delay /
// DownloadClientUnavailable / Fallback" to the v3 frontend.
//
// Persisted: NO — queue rows are in-memory only. But the int
// ordinals are wire-stable through the v3 API JSON envelope, so
// adding new entries APPENDS at the end and never reorders.
type QueueStatus int

// QueueStatus enumeration. Values pinned against C#
// `QueueStatus.cs:5-13` ordinal order (Unknown=0 first).
const (
	// QueueStatusUnknown — zero value; the parser failed to map the
	// download-client status to a known QueueStatus name.
	QueueStatusUnknown QueueStatus = 0
	// QueueStatusQueued — the download is queued at the client but
	// not yet started.
	QueueStatusQueued QueueStatus = 1
	// QueueStatusPaused — the download is paused.
	QueueStatusPaused QueueStatus = 2
	// QueueStatusDownloading — the download is in progress.
	QueueStatusDownloading QueueStatus = 3
	// QueueStatusCompleted — the download finished at the client
	// but hasn't been imported yet.
	QueueStatusCompleted QueueStatus = 4
	// QueueStatusFailed — the download client reported a failure.
	QueueStatusFailed QueueStatus = 5
	// QueueStatusWarning — a non-terminal warning is set.
	QueueStatusWarning QueueStatus = 6
	// QueueStatusDelay — the download is held back by the
	// DelayProfile (release date too recent / preferred quality
	// not yet available).
	QueueStatusDelay QueueStatus = 7
	// QueueStatusDownloadClientUnavailable — the configured client
	// is unreachable.
	QueueStatusDownloadClientUnavailable QueueStatus = 8
	// QueueStatusFallback — fallback bucket for unknown / unmapped
	// download-client states.
	QueueStatusFallback QueueStatus = 9
)

// String returns the C# enum-identifier form for log lines. Matches
// the C# `Enum.ToString()` output at `QueueStatus.cs:5-13`. Out-of-
// range values fall through to `QueueStatus(N)` so log lines never
// silently swap an unrecognised value for "Unknown".
func (q QueueStatus) String() string {
	switch q {
	case QueueStatusUnknown:
		return "Unknown"
	case QueueStatusQueued:
		return "Queued"
	case QueueStatusPaused:
		return "Paused"
	case QueueStatusDownloading:
		return "Downloading"
	case QueueStatusCompleted:
		return "Completed"
	case QueueStatusFailed:
		return "Failed"
	case QueueStatusWarning:
		return "Warning"
	case QueueStatusDelay:
		return "Delay"
	case QueueStatusDownloadClientUnavailable:
		return "DownloadClientUnavailable"
	case QueueStatusFallback:
		return "Fallback"
	default:
		return fmt.Sprintf("QueueStatus(%d)", int(q))
	}
}

// Queue mirrors `Queue/Queue.cs:13-43` — one row in the in-memory
// queue projection. Each row corresponds to a single tracked
// download.
//
// # Field shape vs C#
//
//   - Series / Episodes / Languages: typed value-pointer references
//     to the matched parent rows (mirroring C# nullable
//     `Series` / non-null List<Episode> / List<Language>). The C#
//     code allows nil-Series (unparsed download); the Go port
//     mirrors with a *series.Series.
//   - Quality: quality.Model — the slice-2 typed wrapper. C# uses
//     QualityModel.
//   - SeasonNumber: *int matching the C# nullable. nil when the
//     RemoteEpisode lacked a mapped season.
//   - Size / SizeLeft: int64 (the C# tree uses `decimal` for these,
//     but the underlying download-client values are byte counts so
//     int64 is faithful and avoids the decimal-import). The
//     `decimal` divergence is a documented Go-port simplification.
//   - TimeLeft / EstimatedCompletionTime / Added: pointer-to-time
//     for the C# `TimeSpan?` / `DateTime?` shape.
//   - Status / TrackedDownloadStatus / TrackedDownloadState — the
//     QueueStatus + trackeddownloads.Status / .State enums. The
//     latter two are *trackeddownloads.{Status,State} for the
//     C# nullable shape (rows that lack a tracked match leave them
//     nil).
//   - StatusMessages: []string — placeholder for the typed
//     TrackedDownloadStatusMessage struct; matches the
//     trackeddownloads-side simplification.
//   - RemoteEpisode: *decisionengine.Subject — the Go-port name
//     for C# `RemoteEpisode`.
//   - Protocol: indexers.DownloadProtocol typed enum.
//   - Episode: DEFERRED — the C# tree carries an `[Obsolete] Episode`
//     field for v3 backwards compat (single-episode rows pre-multi-
//     episode era). The Go port omits it; v3 serializer can derive
//     from Episodes[0] if a frontend migration ever needs it.
type Queue struct {
	// ID is the stable derived hash from
	// trackedDownload-{DownloadClient}-{DownloadID}. Set by the
	// MapQueueItem helper in sub-arc 11c; passed verbatim through
	// the read-only surface this slice ships.
	ID int
	// Series is the matched series row. nil when the underlying
	// RemoteEpisode hasn't been parsed.
	Series *series.Series
	// SeasonNumber is the mapped (post-scene-translation) season
	// number from the parser. nil when no RemoteEpisode is attached.
	SeasonNumber *int
	// Episodes is the matched-episode list. Always non-nil — empty
	// slice when the RemoteEpisode lacks an episode mapping (the
	// MapQueue arm at C# `:54` falls through to an empty list).
	Episodes []episodes.Episode
	// Languages is the per-release language list. Always non-nil —
	// MapQueueItem defaults to [Language.Unknown] when the
	// RemoteEpisode is nil per C# `:65`.
	Languages []language.Language
	// Quality is the parsed release quality. Defaults to
	// quality.Model{Quality: Quality.Unknown} when no RemoteEpisode
	// is attached per C# `:66`.
	Quality quality.Model
	// Size is the total reported size in bytes. C# uses `decimal`;
	// Go-port uses int64 (byte count is integer).
	Size int64
	// Title is the user-visible release title with file extension
	// stripped (mirrors `FileExtensions.RemoveFileExtension(...)` at
	// C# `:67`).
	Title string
	// SizeLeft is the remaining bytes-to-download.
	SizeLeft int64
	// TimeLeft is the estimated remaining duration; nil when the
	// client doesn't provide it.
	TimeLeft *time.Duration
	// EstimatedCompletionTime — derived from now+TimeLeft when
	// TimeLeft is non-nil per C# `:88-91`.
	EstimatedCompletionTime *time.Time
	// Added is when the TrackedDownload was first seen.
	Added *time.Time
	// Status is the user-visible queue status enum.
	Status QueueStatus
	// TrackedDownloadStatus / TrackedDownloadState — the per-download
	// lifecycle tags (set when MapQueue projects from a
	// TrackedDownload; nil when the row is sourced from elsewhere).
	TrackedDownloadStatus *trackeddownloads.Status
	TrackedDownloadState  *trackeddownloads.State
	// StatusMessages — per-row warning strings. Always non-nil
	// (empty slice when no warnings).
	StatusMessages []string
	// DownloadID — the per-grab token from the download client.
	DownloadID string
	// RemoteEpisode — the parsed release Subject; nil when not
	// parsed.
	RemoteEpisode *decisionengine.Subject
	// Protocol — Usenet or Torrent.
	Protocol indexers.DownloadProtocol
	// DownloadClient — the user-supplied display name for the
	// configured download client.
	DownloadClient string
	// DownloadClientHasPostImportCategory — mirrors C# `:39`. UI
	// hint: when true, the client has a post-import category set
	// so the import flow can rely on the client to flip the cat
	// rather than recategorising itself.
	DownloadClientHasPostImportCategory bool
	// Indexer — name of the indexer the release came from.
	Indexer string
	// OutputPath — final on-disk path (the download client's
	// reported output path; not the imported library path).
	OutputPath string
	// ErrorMessage — non-empty when the download client reports a
	// failure; surfaces in the UI's queue row tooltip.
	ErrorMessage string
}
