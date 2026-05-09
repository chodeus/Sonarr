package decisionengine

import "strings"

// RejectionKind ports the C# `RejectionType` enum at
// `src/NzbDrone.Core/DecisionEngine/RejectionType.cs:1-9`. Renamed
// to avoid collision with Go's pervasive `reflect.Type` /
// `error.Type` idiom.
//
// Permanent rejections drop the candidate from the queue; Temporary
// rejections re-queue it for later (e.g. "minimum age not yet
// satisfied"). Persisted as the integer value in the v3 API
// payload, so order is wire-stable.
type RejectionKind int

// RejectionKind enumeration. Order matches C# `:5-7` and is wire-stable.
const (
	// RejectionPermanent is the default — candidate is dropped.
	RejectionPermanent RejectionKind = 0
	// RejectionTemporary is a deferred rejection — the pending-releases
	// table re-tries the candidate when the temporary condition (age,
	// queue contention, indexer cool-down) is expected to clear.
	RejectionTemporary RejectionKind = 1
)

// String returns the lower-case name used in log lines. The C# JSON
// converter emits the original PascalCase tokens; downstream tests
// pin against the lower-case form so a future tweak to the API
// shape is a single-call grep.
func (r RejectionKind) String() string {
	switch r {
	case RejectionPermanent:
		return "permanent"
	case RejectionTemporary:
		return "temporary"
	}
	return "unknown"
}

// SpecPriority ports the C# `SpecificationPriority` enum at
// `SpecificationPriority.cs:1-9`. The value gates spec-list ordering
// in the orchestrator: cheap specs (Default = Parsing = Database = 0)
// run before expensive ones (Disk = 1) so a permanent rejection
// short-circuits before reading the filesystem.
//
// All four legal values are exported as named constants — even the
// three aliases that share value 0, since downstream specs declare
// their priority by constant name and shouldn't have to know which
// alias landed at value 0.
type SpecPriority int

// SpecPriority enumeration.
const (
	// SpecPriorityDefault is the cheapest tier. Most specs declare this.
	SpecPriorityDefault SpecPriority = 0
	// SpecPriorityParsing is the parsing-only tier. Aliased to Default
	// in the C# tree.
	SpecPriorityParsing SpecPriority = 0
	// SpecPriorityDatabase is the database-read tier. Aliased to Default
	// in the C# tree.
	SpecPriorityDatabase SpecPriority = 0
	// SpecPriorityDisk is the disk-read tier — runs after every Default
	// spec has had its turn so a Permanent rejection short-circuits the
	// (expensive) filesystem reads.
	SpecPriorityDisk SpecPriority = 1
)

// UpgradeableRejectReason ports the C# enum at
// `UpgradeableRejectReason.cs:1-12`. Returned by UpgradableSpecification
// (deferred slice) to communicate why a candidate is NOT considered an
// upgrade over the current on-disk file. Used by both the
// download-decision pipeline (UpgradeDiskSpecification) and the import
// pipeline (decision-side custom-format gating).
//
// Order matches C# `:5-12` exactly — wire-stable through the v3 API
// envelope on rejection messages.
type UpgradeableRejectReason int

// UpgradeableRejectReason enumeration.
const (
	// UpgradeableNone signals "candidate IS an upgrade" — the success
	// case. Mirrors C# `None` at `:6`.
	UpgradeableNone UpgradeableRejectReason = iota
	// UpgradeableBetterQuality — current file is already at a higher
	// quality than the candidate.
	UpgradeableBetterQuality
	// UpgradeableBetterRevision — current file is a same-quality but
	// higher-revision (proper / repack) than the candidate.
	UpgradeableBetterRevision
	// UpgradeableQualityCutoff — current file already meets the quality
	// profile's cutoff; further upgrades are disallowed.
	UpgradeableQualityCutoff
	// UpgradeableCustomFormatScore — candidate's custom-format score
	// does not improve on the current file's score.
	UpgradeableCustomFormatScore
	// UpgradeableCustomFormatCutoff — current file already meets the
	// quality profile's custom-format cutoff score.
	UpgradeableCustomFormatCutoff
	// UpgradeableMinCustomFormatScore — candidate's score improves on
	// the current but doesn't meet the profile's minimum-upgrade
	// increment.
	UpgradeableMinCustomFormatScore
	// UpgradeableUpgradesNotAllowed — quality profile explicitly
	// disables upgrades (`UpgradeAllowed = false`).
	UpgradeableUpgradesNotAllowed
)

// ReleaseSourceType ports the C# enum at
// `Parser/Model/RemoteEpisode.cs:47-55`. Tracks where in the pipeline
// a candidate release entered Sonarr. Persisted in the History payload
// (so users can see "this came from RSS" vs "this came from a manual
// search"); order is wire-stable.
//
// Lives in the decisionengine package rather than internal/parser
// because the decision pipeline is the sole consumer — the parser
// doesn't branch on it. Future specs that need `subject.ReleaseSource`
// import from here.
type ReleaseSourceType int

// ReleaseSourceType enumeration. Matches C# `:49-54` order.
const (
	// ReleaseSourceUnknown is the zero value — the C# tree uses it as
	// the default for releases pushed via API where the caller didn't
	// tag the source.
	ReleaseSourceUnknown ReleaseSourceType = 0
	// ReleaseSourceRSS — picked up from a scheduled RSS scan.
	ReleaseSourceRSS ReleaseSourceType = 1
	// ReleaseSourceSearch — automatic missing-episode search.
	ReleaseSourceSearch ReleaseSourceType = 2
	// ReleaseSourceUserInvokedSearch — user clicked a "search now"
	// button on the missing-episodes page.
	ReleaseSourceUserInvokedSearch ReleaseSourceType = 3
	// ReleaseSourceInteractiveSearch — user picked a specific release
	// from the manual-search results.
	ReleaseSourceInteractiveSearch ReleaseSourceType = 4
	// ReleaseSourceReleasePush — POST /api/v3/release/push from an
	// external automation tool.
	ReleaseSourceReleasePush ReleaseSourceType = 5
)

// SeasonPackUpgradeType ports the C# enum at
// `src/NzbDrone.Core/MediaFiles/SeasonPackUpgradeType.cs:1-9`. Drives
// the season-pack upgrade-acceptance policy in
// [UpgradeDiskSpecification] when the candidate release is a
// FullSeason — controls how much of the season pack must be an
// upgrade-or-missing for the pack to be accepted.
//
// Order matches C# `:5-7` exactly — wire-stable through the persisted
// `Config.SeasonPackUpgrade` value identifier and the v3 API envelope.
type SeasonPackUpgradeType int

// SeasonPackUpgradeType enumeration.
const (
	// SeasonPackUpgradeAll — every episode in the pack must be either
	// missing or strictly upgradable. Equivalent to a 100% threshold.
	// Matches C# `:5`.
	SeasonPackUpgradeAll SeasonPackUpgradeType = 0
	// SeasonPackUpgradeThresholdMode — the percentage of upgradable
	// episodes (counting missing-on-disk as upgradable) must meet or
	// exceed the user-configured `SeasonPackUpgradeThreshold` floor.
	// Renamed from C# `Threshold` to avoid stuttering with the
	// independently-spelled threshold-percent config getter. Matches
	// C# `:6`.
	SeasonPackUpgradeThresholdMode SeasonPackUpgradeType = 1
	// SeasonPackUpgradeAny — any episode in the pack being upgradable
	// or missing accepts the pack. Most permissive. Matches C# `:7`.
	SeasonPackUpgradeAny SeasonPackUpgradeType = 2
)

// String returns the C# enum-identifier form (`"All"`, `"Threshold"`,
// `"Any"`) — the persistence layer round-trips this token, so a Go
// caller writing the config value via `config.Provider.SetSeasonPackUpgrade`
// AFTER reading via `config.Provider.SeasonPackUpgrade` produces a
// byte-equivalent row.
func (t SeasonPackUpgradeType) String() string {
	switch t {
	case SeasonPackUpgradeAll:
		return "All"
	case SeasonPackUpgradeThresholdMode:
		return "Threshold"
	case SeasonPackUpgradeAny:
		return "Any"
	}
	return "All"
}

// ParseSeasonPackUpgradeType parses the persisted enum identifier
// (case-insensitive) into a typed [SeasonPackUpgradeType]. Unknown /
// empty input falls back to [SeasonPackUpgradeAll] — matches C#
// `GetValueEnum(..., All)` at `ConfigService.cs:271-275`.
func ParseSeasonPackUpgradeType(s string) SeasonPackUpgradeType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "threshold":
		return SeasonPackUpgradeThresholdMode
	case "any":
		return SeasonPackUpgradeAny
	default:
		return SeasonPackUpgradeAll
	}
}
