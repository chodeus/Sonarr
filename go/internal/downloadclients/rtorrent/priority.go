package rtorrent

// Priority ports the C# `RTorrentPriority` enum at
// `src/NzbDrone.Core/Download/Clients/RTorrent/RTorrentPriority.cs:3-9`.
// Values are stable across the JSON wire (persisted on the
// DownloadClient.Settings column) — renumbering breaks every
// existing rTorrent install.
type Priority int

const (
	// PriorityVeryLow → rTorrent priority 0 ("off" — paused).
	PriorityVeryLow Priority = 0

	// PriorityLow → rTorrent priority 1.
	PriorityLow Priority = 1

	// PriorityNormal → rTorrent priority 2 (default).
	PriorityNormal Priority = 2

	// PriorityHigh → rTorrent priority 3.
	PriorityHigh Priority = 3
)

// String returns the canonical name — useful for log lines.
func (p Priority) String() string {
	switch p {
	case PriorityVeryLow:
		return "VeryLow"
	case PriorityLow:
		return "Low"
	case PriorityNormal:
		return "Normal"
	case PriorityHigh:
		return "High"
	default:
		return "Unknown"
	}
}
