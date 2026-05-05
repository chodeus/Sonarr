package pendingreleases

// Reason mirrors the C# `PendingReleaseReason` enum at
// `src/NzbDrone.Core/Download/Pending/PendingReleaseReason.cs:1-9`.
// Tags why a candidate release was deferred to the PendingReleases
// table for retry. Persisted as the integer `Reason` column on the
// PendingReleases table (already shipped at v230); the on-disk shape
// is unchanged — this enum just gives existing int-typed code a
// named-constant surface.
//
// Order is wire-stable: column values land in user databases.
// Re-ordering means migrating every existing row.
type Reason int

// Reason enumeration. Values pinned against C# `:5-7`.
const (
	// ReasonDelay is the DelayProfile-driven defer (the "wait N
	// minutes after release before grabbing" path). The dominant
	// real-world case.
	ReasonDelay Reason = 0
	// ReasonDownloadClientUnavailable defers when every configured
	// download client is unreachable at decision time.
	ReasonDownloadClientUnavailable Reason = 1
	// ReasonFallback covers the catch-all defer cases — typically a
	// queue-state ambiguity that the orchestrator wants to re-evaluate
	// on the next tick.
	ReasonFallback Reason = 2
)

// String returns the lower-case wire form for log lines. Mirrors the
// project convention; the v3 API renders the PascalCase form via its
// own per-int lookup.
func (r Reason) String() string {
	switch r {
	case ReasonDelay:
		return "delay"
	case ReasonDownloadClientUnavailable:
		return "download-client-unavailable"
	case ReasonFallback:
		return "fallback"
	}
	return "unknown"
}
