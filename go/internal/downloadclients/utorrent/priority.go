package utorrent

// Priority ports the C# `UTorrentPriority` enum at
// `src/NzbDrone.Core/Download/Clients/uTorrent/UTorrentPriority.cs`.
type Priority int

const (
	// PriorityLast is the uTorrent "last" priority (the new
	// torrent lands at the BOTTOM of the queue). Maps to the
	// `action=queuebottom` WebUI verb. Default.
	PriorityLast Priority = 0

	// PriorityFirst is the uTorrent "first" priority (the new
	// torrent lands at the TOP of the queue). Maps to the
	// `action=queuetop` WebUI verb.
	PriorityFirst Priority = 1
)

// String returns the canonical name — useful for log lines.
func (p Priority) String() string {
	switch p {
	case PriorityLast:
		return "Last"
	case PriorityFirst:
		return "First"
	default:
		return "Unknown"
	}
}

// State ports the C# `UTorrentState` enum at
// `src/NzbDrone.Core/Download/Clients/uTorrent/UtorrentState.cs`.
// Maps to the uTorrent WebUI's per-action verbs: Start →
// `action=start`, ForceStart → `action=forcestart`, Pause →
// `action=pause`, Stop → `action=stop`.
type State int

const (
	// StateStart leaves the torrent in the running state at add
	// time. Default.
	StateStart State = 0

	// StateForceStart forces the torrent to start regardless of
	// the global queue state.
	StateForceStart State = 1

	// StatePause leaves the torrent paused at add time.
	StatePause State = 2

	// StateStop leaves the torrent stopped at add time.
	StateStop State = 3
)

// String returns the canonical name — useful for log lines.
func (s State) String() string {
	switch s {
	case StateStart:
		return "Start"
	case StateForceStart:
		return "ForceStart"
	case StatePause:
		return "Pause"
	case StateStop:
		return "Stop"
	default:
		return "Unknown"
	}
}
