package decisionengine

import (
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// ConfigReader is the minimal config dependency UpgradableSpecification
// needs — exposes the user's `DownloadPropersAndRepacks` preference.
//
// The interface is deliberately tiny so the (deferred)
// `IConfigService` port doesn't have to land before this spec can be
// useful. Tests inject a struct stub; production wires the real
// config-service surface once it ships.
type ConfigReader interface {
	// DownloadPropersAndRepacks returns the user's proper/repack
	// preference. The C# default is PreferAndUpgrade (0).
	DownloadPropersAndRepacks() quality.ProperDownloadType
}

// ProfileView bundles a `*qualityprofiles.Profile` with its parsed
// embedded-JSON columns (Items, FormatItems). The C# `QualityProfile`
// class exposes those as typed `List<QualityProfileQualityItem>` /
// `List<ProfileFormatItem>` fields directly; the Go-port persisted
// shape stores them as JSON strings, so callers parse once then pass
// the view to whichever spec needs it.
//
// Future specs that need the same wrapper will reuse this type.
type ProfileView struct {
	// Profile is the persisted profile row.
	Profile *qualityprofiles.Profile
	// Items is the typed parsed Profile.Items list.
	Items []qualityprofiles.QualityItem
	// FormatItems is the typed parsed Profile.FormatItems list.
	FormatItems []qualityprofiles.FormatItem
}

// NewProfileView parses `profile.Items` + `profile.FormatItems` and
// bundles the result. Returns an error if either column is malformed.
//
// The view does NOT take ownership of the underlying profile —
// callers can mutate p.Profile after construction; the parsed slices
// are independent.
func NewProfileView(p *qualityprofiles.Profile) (*ProfileView, error) {
	if p == nil {
		return nil, errors.New("decisionengine: NewProfileView: nil profile")
	}
	items, err := p.ParsedItems()
	if err != nil {
		return nil, err
	}
	formatItems, err := p.ParsedFormatItems()
	if err != nil {
		return nil, err
	}
	return &ProfileView{Profile: p, Items: items, FormatItems: formatItems}, nil
}

// FirstAllowedQuality returns the first quality in the view whose
// `Allowed=true`. Mirrors the C# `QualityProfile.FirststAllowedQuality()`
// helper (note the typo in the C# tree's method name; the Go port
// uses the corrected spelling).
//
// Walks the typed Items in order. For a group entry, checks the group
// member that's marked allowed. Returns `quality.Unknown` and false if
// no allowed quality exists (the C# tree doesn't guard this case —
// allowing zero qualities is itself a config bug — but the Go-port
// signature gives callers an explicit miss signal).
func (v *ProfileView) FirstAllowedQuality() (quality.Quality, bool) {
	for _, item := range v.Items {
		if !item.IsGroup() {
			if item.Allowed {
				return item.Quality, true
			}
			continue
		}
		if !item.Allowed {
			continue
		}
		for _, child := range item.Items {
			if child.Allowed {
				return child.Quality, true
			}
		}
	}
	return quality.Unknown, false
}

// LastAllowedQuality returns the last (highest) quality in the view
// whose `Allowed=true`. Mirrors the C# `QualityProfile.LastAllowedQuality()`
// at `src/NzbDrone.Core/Profiles/Qualities/QualityProfile.cs` —
// `Items.Last(q => q.Allowed)`, returning the group's last member
// quality when the last-allowed item is a group (C# `lastAllowed.Items.Last().Quality`).
//
// Returns `quality.Unknown` and false when no allowed quality exists
// (the same explicit-miss signal as [ProfileView.FirstAllowedQuality]).
func (v *ProfileView) LastAllowedQuality() (quality.Quality, bool) {
	var found quality.Quality
	var ok bool
	for _, item := range v.Items {
		if !item.Allowed {
			continue
		}
		if !item.IsGroup() {
			found, ok = item.Quality, true
			continue
		}
		// Group: C# returns the LAST member's quality unconditionally
		// (`lastAllowed.Items.Last().Quality`).
		if n := len(item.Items); n > 0 {
			found, ok = item.Items[n-1].Quality, true
		}
	}
	return found, ok
}

// CompareQualityModels mirrors the C# `QualityModelComparer.Compare`
// at `QualityModelComparer.cs:45-55`. Returns -1/0/+1.
//
// Two-stage comparison:
//
//  1. Compare the profile-relative position of left.Quality vs
//     right.Quality (cheap qualities at lower indices "lose" to
//     pricier qualities further down the profile).
//  2. If positions match, fall back to Revision comparison.
//
// `respectGroupOrder` propagates to the underlying QualityIndex.CompareTo;
// usually false (a group is treated as a single tier) but the
// upgrade-grouping path passes true.
func CompareQualityModels(items []qualityprofiles.QualityItem, left, right quality.Model, respectGroupOrder bool) int {
	if r := compareQuality(items, left.Quality, right.Quality, respectGroupOrder); r != 0 {
		return r
	}
	return left.Revision.Compare(right.Revision)
}

// compareQuality is the single-Quality version of CompareQualityModels.
// Mirrors `QualityModelComparer.Compare(Quality, Quality, bool)` at
// `:32-38`.
func compareQuality(items []qualityprofiles.QualityItem, left, right quality.Quality, respectGroupOrder bool) int {
	leftIdx, _ := qualityprofiles.LookupIndex(items, left, respectGroupOrder)
	rightIdx, _ := qualityprofiles.LookupIndex(items, right, respectGroupOrder)
	return leftIdx.CompareTo(rightIdx, respectGroupOrder)
}

// UpgradableSpecification is the central upgrade-permitted reasoning
// unit. Mirrors `IUpgradableSpecification` at
// `src/NzbDrone.Core/DecisionEngine/Specifications/UpgradableSpecification.cs:11-18`
// + its `UpgradableSpecification` implementation at `:20-209`.
//
// Several other specs delegate to this — UpgradeDiskSpecification,
// HistorySpecification, QueueSpecification, plus the import-side
// custom-format gating. All five public methods are stateless beyond
// the injected ConfigReader + logger; safe to share one instance
// across goroutines.
//
// This type is NOT itself a `Specification` (the IDE-decision contract):
// the orchestrator runs Specifications against a Subject + info pair;
// UpgradableSpecification answers parameterised "is X an upgrade over
// Y?" questions and gets WRAPPED by the higher-level specs. Following
// the C# class lineage faithfully — UpgradableSpecification implements
// IUpgradableSpecification, not IDownloadDecisionEngineSpecification.
type UpgradableSpecification struct {
	cfg ConfigReader
	log *slog.Logger
}

// NewUpgradableSpecification builds the spec. cfg must be non-nil;
// log defaults to slog.Default when nil.
func NewUpgradableSpecification(cfg ConfigReader, log *slog.Logger) (*UpgradableSpecification, error) {
	if cfg == nil {
		return nil, errors.New("decisionengine: NewUpgradableSpecification: nil config reader")
	}
	if log == nil {
		log = slog.Default()
	}
	return &UpgradableSpecification{cfg: cfg, log: log}, nil
}

// IsUpgradable mirrors the C# method at `:31-122`. Walks the
// nine-branch decision tree comparing a candidate (newQuality +
// newCustomFormats) against the current on-disk version
// (currentQuality + currentCustomFormats), returning the
// UpgradeableRejectReason that fires.
//
// Returns UpgradeableNone for "candidate IS an upgrade".
//
// `newQuality` may be nil (the C# `QualityModel?` shape) — the comparer
// treats nil as "lower than anything", and the revision-compare
// branches are skipped.
func (u *UpgradableSpecification) IsUpgradable(
	view *ProfileView,
	currentQuality quality.Model,
	currentCustomFormats []customformats.CustomFormat,
	newQuality *quality.Model,
	newCustomFormats []customformats.CustomFormat,
) UpgradeableRejectReason {
	qualityCompare := compareNewVsCurrent(view.Items, newQuality, currentQuality.Quality)
	downloadPropers := u.cfg.DownloadPropersAndRepacks()

	// :37-41 — new quality is better AND cutoff not met → accept
	if qualityCompare > 0 && u.QualityCutoffNotMet(view, currentQuality, newQuality) {
		u.log.Debug("upgradable: new item has a better quality",
			"existing", currentQuality, "new", newQuality)
		return UpgradeableNone
	}

	// :43-47 — existing is better quality
	if qualityCompare < 0 {
		u.log.Debug("upgradable: existing item has better quality",
			"existing", currentQuality, "new", newQuality)
		return UpgradeableBetterQuality
	}

	// Revision-compare branches use newQuality?.Revision; nil-newQuality
	// produces a nil compare result and skips both branches (matching
	// the C# `int?` short-circuit).
	var qualityRevisionCompare int
	haveRevisionCompare := newQuality != nil
	if haveRevisionCompare {
		qualityRevisionCompare = newQuality.Revision.Compare(currentQuality.Revision)
	}

	// :51-58 — propers ON AND new revision is better → accept
	if downloadPropers != quality.ProperDoNotPrefer && haveRevisionCompare && qualityRevisionCompare > 0 {
		u.log.Debug("upgradable: new item has a better quality revision",
			"existing", currentQuality, "new", newQuality)
		return UpgradeableNone
	}

	// :60-65 — UpgradeAllowed=false short-circuits the rest
	if !view.Profile.UpgradeAllowed {
		u.log.Debug("upgradable: profile does not allow upgrading", "profile", view.Profile.Name)
		return UpgradeableUpgradesNotAllowed
	}

	// :68-73 — propers ON AND new revision is worse → reject
	if downloadPropers != quality.ProperDoNotPrefer && haveRevisionCompare && qualityRevisionCompare < 0 {
		u.log.Debug("upgradable: existing item has a better quality revision",
			"existing", currentQuality, "new", newQuality)
		return UpgradeableBetterRevision
	}

	// :75-81 — qualityCompare > 0 here is unreachable (the first
	// branch returned None). The C# code keeps the branch as a
	// belt-and-braces guard — same here.
	if qualityCompare > 0 {
		u.log.Debug("upgradable: existing item meets quality cutoff",
			"existing", currentQuality)
		return UpgradeableQualityCutoff
	}

	// Custom-format scoring branches.
	currentScore := u.scoreCustomFormats(view, currentCustomFormats)
	newScore := u.scoreCustomFormats(view, newCustomFormats)

	// :86-94 — new score doesn't improve on current
	if newScore <= currentScore {
		u.log.Debug("upgradable: new item's custom formats do not improve",
			"newScore", newScore, "currentScore", currentScore)
		return UpgradeableCustomFormatScore
	}

	// :96-103 — current already meets the cutoff format score
	if currentScore >= view.Profile.CutoffFormatScore {
		u.log.Debug("upgradable: existing item meets custom format cutoff",
			"currentScore", currentScore, "cutoff", view.Profile.CutoffFormatScore)
		return UpgradeableCustomFormatCutoff
	}

	// :105-114 — new score doesn't beat current by minimum increment
	if newScore < currentScore+view.Profile.MinUpgradeFormatScore {
		u.log.Debug("upgradable: new item does not meet minimum upgrade format score",
			"newScore", newScore, "currentScore", currentScore, "increment", view.Profile.MinUpgradeFormatScore)
		return UpgradeableMinCustomFormatScore
	}

	u.log.Debug("upgradable: new item is an upgrade",
		"newScore", newScore, "currentScore", currentScore)
	return UpgradeableNone
}

// QualityCutoffNotMet mirrors C# `:124-140`. True iff the current
// quality has not yet reached the profile's cutoff (where "cutoff" is
// either the explicit Cutoff column for upgrade-allowed profiles, or
// the FirstAllowedQuality otherwise).
//
// `newQuality` is optional — pass nil to skip the revision-upgrade
// secondary branch.
func (u *UpgradableSpecification) QualityCutoffNotMet(view *ProfileView, currentQuality quality.Model, newQuality *quality.Model) bool {
	cutoffID := u.cutoffID(view)
	cutoffIdx, _ := qualityprofiles.LookupIndexByID(view.Items, cutoffID, false)
	currentIdx, _ := qualityprofiles.LookupIndex(view.Items, currentQuality.Quality, false)
	if currentIdx.CompareTo(cutoffIdx, false) < 0 {
		return true
	}
	if newQuality != nil && u.IsRevisionUpgrade(currentQuality, *newQuality) {
		return true
	}
	return false
}

// CutoffNotMet mirrors C# `:150-168`. True iff EITHER the quality
// cutoff is unmet OR the custom-format cutoff is unmet.
func (u *UpgradableSpecification) CutoffNotMet(view *ProfileView, currentQuality quality.Model, currentCustomFormats []customformats.CustomFormat, newQuality *quality.Model) bool {
	if u.QualityCutoffNotMet(view, currentQuality, newQuality) {
		return true
	}
	if u.customFormatCutoffNotMet(view, currentCustomFormats) {
		return true
	}
	u.log.Debug("upgradable: existing item meets cutoff",
		"existing", currentQuality)
	return false
}

// customFormatCutoffNotMet is the private helper at C# `:142-148`.
// "Cutoff" depends on UpgradeAllowed: cutoff if upgrades allowed,
// MinFormatScore otherwise.
func (u *UpgradableSpecification) customFormatCutoffNotMet(view *ProfileView, currentFormats []customformats.CustomFormat) bool {
	score := u.scoreCustomFormats(view, currentFormats)
	cutoff := view.Profile.MinFormatScore
	if view.Profile.UpgradeAllowed {
		cutoff = view.Profile.CutoffFormatScore
	}
	return score < cutoff
}

// IsRevisionUpgrade mirrors C# `:170-182`. Same Quality + new
// Revision wins → true. Different qualities (e.g. WEBRip → WEBDL) is
// NEVER a revision upgrade by this method even if the revision
// compares higher.
func (u *UpgradableSpecification) IsRevisionUpgrade(currentQuality, newQuality quality.Model) bool {
	if currentQuality.Quality != newQuality.Quality {
		return false
	}
	if newQuality.Revision.Compare(currentQuality.Revision) > 0 {
		u.log.Debug("upgradable: new quality is a better revision",
			"existing", currentQuality, "new", newQuality)
		return true
	}
	return false
}

// IsUpgradeAllowed mirrors C# `:184-208`. Distinct from `IsUpgradable`
// — this answers "does the profile permit moving from current → new
// at all?" rather than "does new beat current?".
//
// Three-way decision:
//
//  1. Revision upgrade → always allowed (regardless of UpgradeAllowed).
//  2. Quality OR custom-format upgrade AND UpgradeAllowed → allowed.
//  3. Quality OR custom-format upgrade AND !UpgradeAllowed → REJECTED.
//  4. Otherwise (no upgrade, no revision change) → allowed (no-op).
func (u *UpgradableSpecification) IsUpgradeAllowed(
	view *ProfileView,
	currentQuality quality.Model,
	currentCustomFormats []customformats.CustomFormat,
	newQuality quality.Model,
	newCustomFormats []customformats.CustomFormat,
) bool {
	isQualityUpgrade := CompareQualityModels(view.Items, newQuality, currentQuality, false) > 0
	isCFUpgrade := u.scoreCustomFormats(view, newCustomFormats) > u.scoreCustomFormats(view, currentCustomFormats)

	if u.IsRevisionUpgrade(currentQuality, newQuality) {
		u.log.Debug("upgradable: revision upgrade", "existing", currentQuality, "new", newQuality)
		return true
	}

	if isQualityUpgrade || isCFUpgrade {
		if view.Profile.UpgradeAllowed {
			u.log.Debug("upgradable: profile allows upgrading")
			return true
		}
		u.log.Debug("upgradable: profile disallows upgrades")
		return false
	}

	return true
}

// cutoffID returns the effective profile cutoff. Mirrors C# `:126`:
// `qualityProfile.UpgradeAllowed ? profile.Cutoff : profile.FirststAllowedQuality().Id`.
func (u *UpgradableSpecification) cutoffID(view *ProfileView) int {
	if view.Profile.UpgradeAllowed {
		return view.Profile.Cutoff
	}
	first, _ := view.FirstAllowedQuality()
	return first.ID
}

// scoreCustomFormats translates a `[]customformats.CustomFormat`
// (the Subject-side shape) into a presence-set + delegates to
// `qualityprofiles.CalculateCustomFormatScore`. Mirrors the C#
// `qualityProfile.CalculateCustomFormatScore(formats)` pattern,
// adapted to the Go-port set-based helper signature.
func (u *UpgradableSpecification) scoreCustomFormats(view *ProfileView, formats []customformats.CustomFormat) int {
	if len(formats) == 0 {
		return qualityprofiles.CalculateCustomFormatScore(view.FormatItems, nil)
	}
	present := make(map[int]bool, len(formats))
	for _, f := range formats {
		present[f.ID] = true
	}
	return qualityprofiles.CalculateCustomFormatScore(view.FormatItems, present)
}

// compareNewVsCurrent mirrors the C# nullable-aware comparer call at
// `:34`: `qualityComparer.Compare(newQuality?.Quality, currentQuality.Quality)`.
// Treating nil-new as "below current" (the C# null-coalesce path
// returns 0 when the comparer's left arg is null because both sides
// resolve to GetIndex(null) → zero index — a quirk we preserve to
// match the C# behaviour).
func compareNewVsCurrent(items []qualityprofiles.QualityItem, newQuality *quality.Model, currentQuality quality.Quality) int {
	if newQuality == nil {
		// C# `Compare(null, current)` resolves both sides via
		// `_profile.GetIndex(...)`. GetIndex(null) returns a zero
		// QualityIndex (Index=0). So a non-null current at index 0
		// compares equal; otherwise current "wins". We replicate.
		currentIdx, _ := qualityprofiles.LookupIndex(items, currentQuality, false)
		zero := qualityprofiles.QualityIndex{}
		return zero.CompareTo(currentIdx, false)
	}
	return compareQuality(items, newQuality.Quality, currentQuality, false)
}
