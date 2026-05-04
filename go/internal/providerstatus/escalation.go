// Package providerstatus holds the cross-cutting primitives shared by
// the four `*Status` services (IndexerStatus / DownloadClientStatus /
// NotificationStatus / ImportListStatus).
//
// What lives here vs. per-type packages:
//
//   - The EscalationBackOff schedule (this file) — a flat constant
//     table, no per-type tweaks.
//   - The shared ProviderStatusServiceBase port (in a follow-up
//     slice) — generic-over-status-type retry/back-off logic.
//
// Per-type repos and rows continue to live in their own packages
// (`internal/indexerstatus`, etc.) — they have type-specific columns
// (e.g. IndexerStatus carries LastRssSyncReleaseInfo) that don't
// belong in the shared layer.
package providerstatus

import "time"

// BackOffPeriods ports the escalation-tier table at
// `src/NzbDrone.Core/ThingiProvider/Status/EscalationBackOff.cs`.
// Each entry is the duration a provider stays disabled at that
// EscalationLevel after a failure. Index 0 (zero duration) is the
// fall-through level for callers that only want a "no back-off"
// classification; level 1 is a 60s timeout, growing geometrically
// up to 24h at level 9.
//
// The C# constant is `int[]` of seconds; the Go port exposes
// [time.Duration] so callers don't have to multiply manually. Both
// representations are exposed because the C# code subscripts the
// raw int form in a couple of places (e.g.
// `EscalationBackOff.Periods[2]`); preserving the int slice avoids
// re-deriving `int(d.Seconds())` at every call-site.
var (
	BackOffPeriods = []time.Duration{
		0,
		60 * time.Second,
		5 * time.Minute,
		15 * time.Minute,
		30 * time.Minute,
		1 * time.Hour,
		3 * time.Hour,
		6 * time.Hour,
		12 * time.Hour,
		24 * time.Hour,
	}

	// BackOffPeriodSeconds is the verbatim C# `Periods` table —
	// integer seconds, same indices. Keep both forms in sync; the
	// init-time check in [escalation_test.go] guards against drift.
	BackOffPeriodSeconds = []int{
		0,
		60,
		5 * 60,
		15 * 60,
		30 * 60,
		60 * 60,
		3 * 60 * 60,
		6 * 60 * 60,
		12 * 60 * 60,
		24 * 60 * 60,
	}
)

// MaxEscalationLevel is the highest valid index into [BackOffPeriods]
// (the C# default for `MaximumEscalationLevel` at
// `ProviderStatusServiceBase.cs:31`). Per-type services may lower
// this — the old code did so for some indexer error classes.
var MaxEscalationLevel = len(BackOffPeriods) - 1
