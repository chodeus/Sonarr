package commands

// Status mirrors `NzbDrone.Core.Messaging.Commands.CommandStatus`. The
// underlying `int` value is the persisted form on the
// `Commands.Status` column. Values match C# verbatim — the on-disk
// numbering is wire contract.
type Status int

// Status enumeration. The Go port uses the literal C# values so
// pre-existing rows in user databases round-trip without remapping.
const (
	StatusQueued    Status = 0
	StatusStarted   Status = 1
	StatusCompleted Status = 2
	StatusFailed    Status = 3
	StatusAborted   Status = 4
	StatusCancelled Status = 5
	StatusOrphaned  Status = 6
)

// String returns the canonical name. Used for log messages and any
// future event-bus envelope.
func (s Status) String() string {
	switch s {
	case StatusQueued:
		return "Queued"
	case StatusStarted:
		return "Started"
	case StatusCompleted:
		return "Completed"
	case StatusFailed:
		return "Failed"
	case StatusAborted:
		return "Aborted"
	case StatusCancelled:
		return "Cancelled"
	case StatusOrphaned:
		return "Orphaned"
	default:
		return "Unknown"
	}
}

// Result mirrors `CommandResult`. Persisted on `Commands.Result`. The
// schema declares this column NOT NULL DEFAULT 1, so the wire-default
// is `Successful`.
type Result int

// Result enumeration.
const (
	ResultUnknown       Result = 0
	ResultSuccessful    Result = 1
	ResultUnsuccessful  Result = 2
	ResultIndeterminate Result = 3
)

// String returns the canonical name.
func (r Result) String() string {
	switch r {
	case ResultUnknown:
		return "Unknown"
	case ResultSuccessful:
		return "Successful"
	case ResultUnsuccessful:
		return "Unsuccessful"
	case ResultIndeterminate:
		return "Indeterminate"
	default:
		return "Unknown"
	}
}

// Trigger mirrors `CommandTrigger`. Persisted on `Commands.Trigger`.
type Trigger int

// Trigger enumeration.
const (
	TriggerUnspecified Trigger = 0
	TriggerManual      Trigger = 1
	TriggerScheduled   Trigger = 2
)

// String returns the canonical name.
func (t Trigger) String() string {
	switch t {
	case TriggerUnspecified:
		return "Unspecified"
	case TriggerManual:
		return "Manual"
	case TriggerScheduled:
		return "Scheduled"
	default:
		return "Unknown"
	}
}

// Priority mirrors `CommandPriority`. Persisted on `Commands.Priority`.
// Note the negative value for Low — the C# enum starts at -1 to give
// the dispatcher a natural ordering (high → normal → low) when sorted
// descending. The Go port preserves the numbering.
type Priority int

// Priority enumeration.
const (
	PriorityLow    Priority = -1
	PriorityNormal Priority = 0
	PriorityHigh   Priority = 1
)

// String returns the canonical name.
func (p Priority) String() string {
	switch p {
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
