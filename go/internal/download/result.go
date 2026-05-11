// Package download ports `NzbDrone.Core.Download.ProcessDownloadDecisions`
// + the recurring `CheckForFinishedDownloadCommand` handler. Slice 4.Z
// (queue [15]). The grab pipeline that takes approved decisions and
// dispatches them to the configured download clients.
//
// Scope at queue [15] minimum-viable:
//   - [ProcessDownloadDecisions] iterates approved [decisionengine.Decision]
//     values, picks a Provider per the release's protocol, and calls
//     [downloadclients.Provider.Download].
//   - Per-protocol short-circuit (Usenet failure → mark remaining
//     Usenet decisions as Pending; same for Torrent) mirrors C#
//     `ProcessDownloadDecisions.cs:67-72,96-110`.
//   - Episode-already-grabbed dedup skips lower-priority duplicates
//     within the same batch (C# `:55-58,167-176`).
//   - [PendingReleaseService] writes are DEFERRED — the Go port
//     produces the Pending list but doesn't persist it. The pending-
//     release table + delayed-grab retry loop are own-slice work.
//   - [PrioritizeDownloadDecision] sort is DEFERRED — the Go port
//     processes decisions in input order. Decision quality sort lives
//     in the broader DecisionEngine surface area which queue [15]
//     doesn't unblock.
//
// C# cites lead each type. Slice 4.Z wraps the result types here;
// [ProcessDownloadDecisions] body lives in process_decisions.go.
package download

import "sonarr.tv/sonarr/internal/decisionengine"

// ProcessedDecisionResult is the per-decision outcome the
// orchestrator's grab loop emits. Mirrors C#
// `ProcessedDecisionResult.cs:3-10`.
type ProcessedDecisionResult int

const (
	// ResultGrabbed — the release was successfully dispatched to a
	// download client. The client returned a DownloadID Sonarr can
	// track against. Mirrors C# `Grabbed`.
	ResultGrabbed ProcessedDecisionResult = iota
	// ResultPending — the release wasn't dispatched yet but should
	// be retried (delayed by a TimeDelay spec, or because the
	// matching protocol's clients all failed). The Go port produces
	// this result but the PendingReleaseService persistence is
	// deferred. Mirrors C# `Pending`.
	ResultPending
	// ResultRejected — the decision had rejections (didn't pass the
	// spec gate) or wasn't qualified (no Episodes resolved).
	// Mirrors C# `Rejected`.
	ResultRejected
	// ResultFailed — dispatch to the chosen download client failed
	// in a way that suggests the client is unavailable (transport
	// error, auth failure). Triggers the per-protocol short-circuit.
	// Mirrors C# `Failed`.
	ResultFailed
	// ResultSkipped — the decision was skipped without a retry path
	// (e.g. release URL no longer reachable). Permanent skip.
	// Mirrors C# `Skipped`.
	ResultSkipped
)

// String returns the human-readable name for log lines + test assertions.
func (r ProcessedDecisionResult) String() string {
	switch r {
	case ResultGrabbed:
		return "Grabbed"
	case ResultPending:
		return "Pending"
	case ResultRejected:
		return "Rejected"
	case ResultFailed:
		return "Failed"
	case ResultSkipped:
		return "Skipped"
	default:
		return "Unknown"
	}
}

// ProcessedDecisions is the batch-result envelope ProcessDecisions
// returns. Mirrors C# `ProcessedDecisions.cs:1-19`.
//
// Every Decision passed in lands in exactly one of these three
// buckets — Grabbed / Pending / Rejected. Failed and Skipped from
// the per-decision result enum are not separately surfaced at the
// batch level: Failed becomes Pending (with the
// DownloadClientUnavailable reason in C#); Skipped is dropped (no
// Decision returned). Matches C# `ProcessDownloadDecisions.cs:42-122`.
type ProcessedDecisions struct {
	// Grabbed lists every Decision the orchestrator successfully
	// dispatched to a download client.
	Grabbed []decisionengine.Decision
	// Pending lists every Decision that didn't grab but should be
	// retried (TimeDelay rejection or download-client-unavailable).
	Pending []decisionengine.Decision
	// Rejected lists every Decision with permanent rejections (or
	// no resolvable Episodes — see C# `IsQualifiedReport` at `:161-165`).
	Rejected []decisionengine.Decision
}

// NewProcessedDecisions returns an empty zero-state envelope with
// non-nil slices. Matches the C# constructor contract at `:12-17` —
// callers iterate without nil-checks.
func NewProcessedDecisions() *ProcessedDecisions {
	return &ProcessedDecisions{
		Grabbed:  []decisionengine.Decision{},
		Pending:  []decisionengine.Decision{},
		Rejected: []decisionengine.Decision{},
	}
}
