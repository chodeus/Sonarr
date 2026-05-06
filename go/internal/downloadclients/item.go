package downloadclients

import (
	"time"

	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// DownloadItemStatus is the runtime status of one in-flight download
// item reported by a download client. Mirrors
// `src/NzbDrone.Core/Download/DownloadItemStatus.cs:1-12`. Numeric
// order is wire-stable — the v3 React frontend serialises this as an
// int over SignalR.
type DownloadItemStatus int

// DownloadItemStatus values. Numeric order matches the C# enum so
// rows / SignalR payloads round-trip identically.
const (
	// DownloadItemStatusQueued — the download is sitting in the
	// client's queue but has not yet started.
	DownloadItemStatusQueued DownloadItemStatus = 0
	// DownloadItemStatusPaused — the download is started but
	// paused (user, scheduler, or out-of-band paused state).
	DownloadItemStatusPaused DownloadItemStatus = 1
	// DownloadItemStatusDownloading — actively transferring.
	DownloadItemStatusDownloading DownloadItemStatus = 2
	// DownloadItemStatusCompleted — fully downloaded; the
	// download client may still be seeding (torrent) but Sonarr
	// considers it ready for import.
	DownloadItemStatusCompleted DownloadItemStatus = 3
	// DownloadItemStatusFailed — the client surfaced a permanent
	// failure (corrupt download, par2 verification failed, etc.).
	DownloadItemStatusFailed DownloadItemStatus = 4
	// DownloadItemStatusWarning — the client surfaced a
	// non-fatal warning. Imports stall until the user clears it.
	DownloadItemStatusWarning DownloadItemStatus = 5
)

// ItemClientInfo carries the per-client provenance Sonarr attaches to
// every Item. Mirrors
// `src/NzbDrone.Core/Download/DownloadClientItem.cs:33-56`'s
// DownloadClientItemClientInfo type.
//
// The C# tree exposes a static FromDownloadClient<TSettings> factory
// over DownloadClientBase; the Go port's download-client subsystem
// (Phase 4 territory) will populate ItemClientInfo at construction
// time. Until that lands, EpisodeImport reads only the fields it
// needs (Protocol for spec gates, Name for log lines,
// RemoveCompletedDownloads for the cleanup branch).
type ItemClientInfo struct {
	// Protocol is the C# `DownloadProtocol` field — Usenet vs
	// Torrent vs Unknown.
	Protocol indexers.DownloadProtocol

	// Type is the Implementation class name (the C# concrete class —
	// e.g. "Sabnzbd", "QBittorrent"). Used for log lines and the v3
	// API's "type" field.
	Type string

	// ID is the persisted DownloadClient row's primary key.
	ID int

	// Name is the user-supplied display name for this client.
	Name string

	// RemoveCompletedDownloads mirrors the boolean from
	// DownloadClientDefinition.cs — gates the post-import cleanup
	// at the queue layer.
	RemoveCompletedDownloads bool

	// HasPostImportCategory mirrors the C# field of the same name
	// — true when the client is configured with a separate "moved
	// after import" category. Affects the import-mode resolution
	// in the writer.
	HasPostImportCategory bool
}

// Item is one in-flight download as reported by a configured
// download client. Mirrors
// `src/NzbDrone.Core/Download/DownloadClientItem.cs:9-31` —
// renamed in the Go port from `DownloadClientItem` to `Item` because
// the package name already carries the `downloadclients.` qualifier
// and `downloadclients.Item` reads better at every call site than
// `downloadclients.DownloadClientItem`.
//
// Pointer semantics: every consumer (specs, writer, queue UI, history
// tracker) holds the same `*Item` and may read fields concurrently.
// The download-client polling loop (Phase 4) treats Item snapshots as
// immutable per emit; updates produce a fresh Item value rather than
// mutating in place. Tests that need to mutate should use [Item.Clone].
type Item struct {
	// DownloadClientInfo carries the source-of-truth provenance —
	// which DownloadClient row this item came from, and what
	// runtime protocol/type. Pointer so a nil DownloadClientInfo
	// signals "synthetic / not from a real client" (used by the
	// manual-import flow at C# `ManualImportService.cs`).
	DownloadClientInfo *ItemClientInfo

	// DownloadID is the client-side identifier (NZB hash, torrent
	// info-hash, SAB nzo_id). Sonarr uses this as the join key
	// for the Tracked Download Service.
	DownloadID string

	// Category is the per-item category string the client reports
	// (sabnzbd "tv-sonarr", qbittorrent "sonarr"). Empty when the
	// client doesn't support categories.
	Category string

	// Title is the human-readable title the client surfaces (for
	// most clients this is the original NZB / torrent name).
	Title string

	// TotalSize is the post-extraction byte size the client
	// reported. Some clients only know the on-wire size for a
	// torrent and surface that here — close enough for the import
	// path which uses Size from the on-disk file anyway.
	TotalSize int64

	// RemainingSize is bytes-still-to-download. Zero on completed
	// items but the import path doesn't read this — it's a queue UI
	// concern.
	RemainingSize int64

	// RemainingTime is the client's ETA for completion. nil when
	// the client doesn't report an ETA (queued items, sabnzbd
	// "PAUSED", etc.). Mirrors the C# `TimeSpan?` nullable.
	RemainingTime *time.Duration

	// SeedRatio is the post-completion seed ratio for torrent
	// clients. nil for usenet items / pre-completion items;
	// non-nil for completed torrents being seeded.
	SeedRatio *float64

	// OutputPath is the on-disk directory where the client placed
	// (or is placing) the download. Empty zero-value when the
	// client hasn't created the directory yet.
	OutputPath ospath.OsPath

	// Message carries any warning / error / status string the
	// client surfaced. Free-form; not meant to be parsed.
	Message string

	// Status is the runtime state — Queued / Downloading /
	// Completed / Failed / Warning / Paused.
	Status DownloadItemStatus

	// IsEncrypted is true when the post-extracted archive looked
	// encrypted. The C# tree treats this as a hard-fail signal
	// and marks the item as a download-client-level failure.
	IsEncrypted bool

	// CanMoveFiles is the per-client capability flag: true means
	// the writer is allowed to move files (default), false means
	// the writer must copy (e.g. the client is still actively
	// using the file as a torrent seed source).
	CanMoveFiles bool

	// CanBeRemoved is the per-client capability flag: true means
	// the post-import cleanup may DELETE this item from the
	// client's queue.
	CanBeRemoved bool
}

// Clone returns a shallow copy of i. Mirrors the C# `MemberwiseClone`
// at `DownloadClientItem.cs:27-30`. ItemClientInfo is shared by
// pointer; callers that mutate the clone's ItemClientInfo must clone
// that nested struct themselves. The shallow shape is intentional —
// the only mutable fields downstream callers touch (Status, Message,
// RemainingSize, RemainingTime) are value types.
func (i *Item) Clone() *Item {
	if i == nil {
		return nil
	}
	clone := *i
	return &clone
}
