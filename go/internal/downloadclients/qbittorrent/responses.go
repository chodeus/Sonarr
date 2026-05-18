package qbittorrent

// QBittorrentTorrent is the per-item shape /api/v2/torrents/info
// returns. Mirrors C# `QBittorrentTorrent` at
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrentTorrent.cs:6-43`.
//
// Field tagging follows the wire-format JSON keys — qBittorrent
// uses snake_case for the storage-path family ("save_path",
// "content_path", "ratio_limit", "seeding_time_limit", etc.) and
// camelCase for the rest. The Go port uses explicit json tags
// rather than the JSON-name convention because qBittorrent's API is
// not uniform.
//
// Eta is int64 rather than uint64 / big.Int (the C# type — see
// `QBittorrentTorrent.cs:14` and the comment about ulong-bug
// overflow) because Go's int64 is a superset of every realistic
// ETA + we clamp explicitly during projection in [QBittorrent.GetItems].
type QBittorrentTorrent struct {
	// Hash is the torrent's info-hash (lowercase hex). Becomes
	// the Sonarr-side DownloadID upper-cased per
	// `QBittorrent.cs:258`.
	Hash string `json:"hash"`

	// Name is the human-readable torrent name (also the queue-UI
	// display title).
	Name string `json:"name"`

	// Size is the total torrent size in bytes.
	Size int64 `json:"size"`

	// Progress is a 0.0–1.0 fraction (NOT a percentage). C#
	// `QBittorrentTorrent.cs:12` carries it as double.
	Progress float64 `json:"progress"`

	// Eta is seconds-to-completion. qBittorrent returns 8640000
	// (100 days) for completed torrents — the projection layer
	// clamps that to zero per C# `QBittorrent.cs:301`.
	Eta int64 `json:"eta"`

	// State is the qBittorrent-internal state string ("downloading",
	// "pausedUP", "stoppedUP", "uploading", "queuedDL", "error",
	// etc.). See the canonical mapping in C# `:275-340`.
	State string `json:"state"`

	// Label is the pre-3.3.5 label field. Modern qBittorrent uses
	// Category but keeps Label populated for back-compat. The
	// projection layer at `QBittorrent.cs:259` falls back to Label
	// when Category is empty.
	Label string `json:"label"`

	// Category is the post-3.3.5 category string.
	Category string `json:"category"`

	// SavePath is the configured save directory (the "download"
	// folder root). qBittorrent always uses '/' separators here,
	// even on Windows (per `QBittorrent.cs:386` comment).
	SavePath string `json:"save_path"`

	// ContentPath is the path to the actual on-disk file or
	// folder. Only meaningful at WebUI API >= 2.6.1 (qBittorrent
	// >= 4.2.0). When equal to SavePath it indicates qBittorrent
	// couldn't determine the content path — surface as a Warning
	// per C# `:349-352`.
	ContentPath string `json:"content_path"`

	// Ratio is the current share ratio.
	Ratio float64 `json:"ratio"`

	// RatioLimit is the per-torrent override (-2 = use global,
	// -1 = unlimited, else the limit).
	RatioLimit float64 `json:"ratio_limit"`

	// SeedingTime is seconds-spent-seeding. Not always populated
	// by /api/v2/torrents/info — sometimes only the properties
	// endpoint surfaces it. Pointer to distinguish "unknown" from
	// "zero".
	SeedingTime *int64 `json:"seeding_time,omitempty"`

	// SeedingTimeLimit is the per-torrent seeding-time override
	// (-2 = use global, -1 = unlimited, else minutes).
	SeedingTimeLimit int64 `json:"seeding_time_limit"`

	// InactiveSeedingTimeLimit is the per-torrent inactive-seeding-
	// time override (-2 = use global, -1 = unlimited, else
	// minutes). qBittorrent 4.6+.
	InactiveSeedingTimeLimit int64 `json:"inactive_seeding_time_limit"`

	// LastActivity is the unix timestamp (seconds) of the last
	// chunk transfer. Zero when no chunks have been transferred
	// since add.
	LastActivity int64 `json:"last_activity"`
}

// QBittorrentMaxRatioAction enumerates the action qBittorrent
// performs when a torrent reaches its global max ratio. Mirrors C#
// `QBittorrentMaxRatioAction` at `QBittorrentPreferences.cs:5-12`.
type QBittorrentMaxRatioAction int

// Max-ratio actions. The numeric values match the qBittorrent wire
// format (and the C# enum) — do not reorder.
const (
	MaxRatioActionPause              QBittorrentMaxRatioAction = 0
	MaxRatioActionRemove             QBittorrentMaxRatioAction = 1
	MaxRatioActionEnableSuperSeeding QBittorrentMaxRatioAction = 2
	MaxRatioActionDeleteFiles        QBittorrentMaxRatioAction = 3
)

// QBittorrentPreferences is the global qBittorrent config returned
// by GET /api/v2/app/preferences. Mirrors C# `QBittorrentPreferences`
// at `QBittorrentPreferences.cs:14-46`.
//
// 5.G1g ships only the fields GetStatus consumes; the
// addShareLimits-inline / DhtEnabled-metaDL-warning / QueueingEnabled
// branches in GetItems + Download will reach for additional fields
// at later sub-arcs.
type QBittorrentPreferences struct {
	// SavePath is the global default download directory. Used by
	// [QBittorrent.GetStatus] to surface the root path the queue
	// UI's path-mapping helper translates against.
	SavePath string `json:"save_path"`

	// MaxRatioEnabled is true when the global share-ratio limit is
	// turned on. RemovesCompletedDownloads gating reads this AND
	// MaxRatioAction.
	MaxRatioEnabled bool `json:"max_ratio_enabled"`

	// MaxRatio is the global share-ratio cap.
	MaxRatio float64 `json:"max_ratio"`

	// MaxSeedingTimeEnabled is true when the global share-time
	// limit is turned on.
	MaxSeedingTimeEnabled bool `json:"max_seeding_time_enabled"`

	// MaxSeedingTime is the global share-time cap, in minutes.
	MaxSeedingTime int64 `json:"max_seeding_time"`

	// MaxInactiveSeedingTimeEnabled is true when the global
	// inactive-share-time limit is turned on (qBittorrent 4.6+).
	MaxInactiveSeedingTimeEnabled bool `json:"max_inactive_seeding_time_enabled"`

	// MaxInactiveSeedingTime is the global inactive-share-time
	// cap, in minutes (qBittorrent 4.6+).
	MaxInactiveSeedingTime int64 `json:"max_inactive_seeding_time"`

	// MaxRatioAction is the post-limit action qBittorrent performs
	// (Pause / Remove / EnableSuperSeeding / DeleteFiles). The
	// Sonarr-side RemovesCompletedDownloads computation surfaces
	// true only when the action is Remove or DeleteFiles.
	MaxRatioAction QBittorrentMaxRatioAction `json:"max_ratio_act"`

	// QueueingEnabled is true when qBittorrent's torrent-queueing
	// feature is on — required by the topPrio / bottomPrio RPCs.
	// Defaulted to true on the C# side (see `:42`); the Go port
	// preserves the default during JSON unmarshal via the explicit
	// pointer-default helper at parse time.
	QueueingEnabled bool `json:"queueing_enabled"`

	// DhtEnabled mirrors qBittorrent's "DHT" toggle. Consumed by
	// GetItems' metaDL warning arm at a later wiring slice; ported
	// now so the GetConfig deser path is complete.
	DhtEnabled bool `json:"dht"`
}

// QBittorrentTorrentProperties is the per-torrent detail returned
// by /api/v2/torrents/properties. Slice 5.G1d ships the struct skeleton
// only — the consumers (GetImportItem post-2.6.1 fallback, the C# tree
// at `:381`) land at later slices.
//
// Mirrors C# `QBittorrentTorrentProperties` at
// `QBittorrentTorrent.cs:45-54`.
type QBittorrentTorrentProperties struct {
	Hash        string `json:"hash"`
	SavePath    string `json:"save_path"`
	SeedingTime int64  `json:"seeding_time"`
}
