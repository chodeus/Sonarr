package deluge

// DelugeTorrent is the per-item shape /api/v2's web.update_ui RPC
// surfaces inside the response envelope. Mirrors C# `DelugeTorrent`
// at `src/NzbDrone.Core/Download/Clients/Deluge/DelugeTorrent.cs:6-55`.
//
// JSON tags follow the deluge-web wire format — most fields use
// snake_case (save_path, total_size, is_finished, etc.), so the Go
// port carries explicit json tags rather than relying on the
// JSON-name convention.
type DelugeTorrent struct {
	// Hash is the torrent's info-hash (lowercase hex). Becomes
	// the Sonarr-side DownloadID upper-cased per
	// C# `Deluge.cs:141`.
	Hash string `json:"hash"`

	// Name is the human-readable torrent name.
	Name string `json:"name"`

	// State is the deluge-internal state string. See [DelugeTorrentStatus*].
	State string `json:"state"`

	// Progress is a 0-100 percentage (NOT 0-1 fraction). The C#
	// type uses `double` at `:11`.
	Progress float64 `json:"progress"`

	// Eta is seconds-to-completion. C# uses double at `:12`; the
	// Go port keeps the double-precision since deluge sometimes
	// emits fractional ETAs.
	Eta float64 `json:"eta"`

	// Message is any deluge-side status / warning message.
	Message string `json:"message"`

	// IsFinished is true when the torrent has completed
	// downloading. Per C# `:15`.
	IsFinished bool `json:"is_finished"`

	// DownloadPath is the configured save directory. Mirrors C#
	// `:27` (which renames from save_path → DownloadPath in the C#
	// surface; we keep the Go-port C# convention).
	DownloadPath string `json:"save_path"`

	// Size is the total torrent size in bytes. Mirrors C# `:30`
	// (renamed from total_size).
	Size int64 `json:"total_size"`

	// BytesDownloaded is the count of bytes already on disk.
	// Mirrors C# `:33` (renamed from total_done).
	BytesDownloaded int64 `json:"total_done"`

	// DateAdded is the torrent-added Unix timestamp (seconds).
	// Mirrors C# `:36` (renamed from time_added) — type `double`
	// because deluge emits fractional seconds.
	DateAdded float64 `json:"time_added"`

	// SecondsDownloading is the cumulative time-spent-active in
	// seconds. Mirrors C# `:39` (renamed from active_time).
	SecondsDownloading int `json:"active_time"`

	// Ratio is the current share ratio.
	Ratio float64 `json:"ratio"`

	// IsAutoManaged is true when deluge's queue manager is in
	// control of the torrent. CanBeRemoved gating reads this AND
	// StopAtRatio AND Ratio >= StopRatio.
	IsAutoManaged bool `json:"is_auto_managed"`

	// StopAtRatio is true when the torrent has a per-torrent
	// stop-at-share-ratio cap.
	StopAtRatio bool `json:"stop_at_ratio"`

	// RemoveAtRatio is true when the per-torrent cap is configured
	// to remove (vs pause). Currently informational — Sonarr's
	// CanBeRemoved gating uses State+StopAtRatio+Ratio>=StopRatio
	// to decide whether to call core.remove_torrent itself.
	RemoveAtRatio bool `json:"remove_at_ratio"`

	// StopRatio is the per-torrent ratio cap (when StopAtRatio is
	// true).
	StopRatio float64 `json:"stop_ratio"`
}

// DelugeUpdateUIResult is the envelope deluge-web's web.update_ui
// returns. Mirrors C# `DelugeUpdateUIResult` at
// `DelugeUpdateUIResult.cs:5-9`.
//
// Stats is left as a generic map because deluge varies its shape
// across releases and the Sonarr consumer doesn't read it.
type DelugeUpdateUIResult struct {
	Stats     map[string]any           `json:"stats,omitempty"`
	Connected bool                     `json:"connected"`
	Torrents  map[string]DelugeTorrent `json:"torrents"`
}

// Deluge state strings. Ports C# `DelugeTorrentStatus` at
// `DelugeTorrentStatus.cs:3-10`.
const (
	StatusPaused      = "Paused"
	StatusQueued      = "Queued"
	StatusDownloading = "Downloading"
	StatusSeeding     = "Seeding"
	StatusChecking    = "Checking"
	StatusError       = "Error"
)

// requiredProperties is the field-list deluge-web's web.update_ui
// returns for each torrent. Mirrors C# `RequiredProperties` at
// `DelugeProxy.cs:32-33` exactly.
var requiredProperties = []string{
	"hash", "name", "state", "progress", "eta", "message",
	"is_finished", "save_path", "total_size", "total_done",
	"time_added", "active_time", "ratio", "is_auto_managed",
	"stop_at_ratio", "remove_at_ratio", "stop_ratio",
}
