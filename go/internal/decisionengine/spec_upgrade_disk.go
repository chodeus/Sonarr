package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// UpgradeDiskSpecificationName is the canonical Spec.Name() identifier.
const UpgradeDiskSpecificationName = "UpgradeDisk"

// UpgradeDiskProfileReader narrows the profile-resolver dep for
// [UpgradeDiskSpecification]. Same shape as the slice-3.65 / 3.AO /
// 3.AR sibling profile readers — defined separately so the
// compile-time interface satisfaction stays scoped to one spec (Go's
// structural typing lets the same backing implementation satisfy
// every shape-identical profile reader).
type UpgradeDiskProfileReader interface {
	LookupProfile(ctx context.Context, qualityProfileID int) (*ProfileView, bool, error)
}

// UpgradeDiskCutoffChecker narrows [UpgradableSpecification] to the
// two methods [UpgradeDiskSpecification] consumes — CutoffNotMet
// (whether the candidate would lift the existing file off the cutoff
// floor) + IsUpgradable (the typed UpgradeableRejectReason). Keeps
// the spec stub-able in tests without constructing a full
// *UpgradableSpecification.
//
// Production binding: [*UpgradableSpecification] satisfies this
// interface via Go's structural typing on both methods.
type UpgradeDiskCutoffChecker interface {
	CutoffNotMet(view *ProfileView, currentQuality quality.Model, currentCustomFormats []customformats.CustomFormat, newQuality *quality.Model) bool
	IsUpgradable(view *ProfileView, currentQuality quality.Model, currentCustomFormats []customformats.CustomFormat, newQuality *quality.Model, newCustomFormats []customformats.CustomFormat) UpgradeableRejectReason
}

// UpgradeDiskFormatCalculator narrows the by-EpisodeFile CustomFormat
// calculator dep — shape-identical to slice-3.AR
// [UpgradeAllowedFormatCalculator]. Defined per-spec so compile-time
// guards stay scoped.
type UpgradeDiskFormatCalculator interface {
	ParseCustomFormat(ctx context.Context, file *episodefiles.File, series *series.Series) ([]customformats.CustomFormat, error)
}

// UpgradeDiskSeasonReader narrows [config.Provider] to the two
// season-pack-mode getters this spec consumes:
// [SeasonPackUpgradeType] policy + numeric percent threshold.
//
// Production binding: a wiring adapter at the cmd/sonarr boot site
// translates *config.Provider's raw `string` SeasonPackUpgrade getter
// to a typed [SeasonPackUpgradeType] via [ParseSeasonPackUpgradeType]
// — keeps the config package free of the decisionengine import (same
// dep-shape dodge as the EpisodeTitleRequired / Languages getters).
type UpgradeDiskSeasonReader interface {
	SeasonPackUpgrade() SeasonPackUpgradeType
	SeasonPackUpgradeThreshold() float64
}

// UpgradeDiskSpecification rejects download candidates whose existing
// on-disk file makes the candidate redundant — either the cutoff is
// already met (no further upgrade available) or the existing file is
// strictly better on quality / revision / custom-format axes.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/UpgradeDiskSpecification.cs:1-186`.
//
// # Two evaluation paths
//
// **FullSeason path** (mirrors C# `:35-114`): when
// `Subject.ParsedEpisodeInfo.FullSeason` is true the spec walks every
// episode in the pack, counting (a) missing-on-disk episodes (always
// counted as upgradable, matching C# `:46-48`) and (b) existing files
// that pass both CutoffNotMet AND IsUpgradable=None. The pack is
// accepted iff the upgradable-percentage meets the user's
// SeasonPackUpgrade policy:
//
//   - SeasonPackUpgradeAny → ANY upgradable episode accepts the pack.
//   - SeasonPackUpgradeAll → 100% threshold (every episode must be
//     either missing or upgradable).
//   - SeasonPackUpgradeThresholdMode → user-configured threshold
//     percent floor.
//
// **Per-episode path** (mirrors C# `:116-184`): when not FullSeason,
// the spec walks each Episode-with-file and short-circuits on the
// FIRST file that fails CutoffNotMet OR IsUpgradable. The per-arm
// rejection reason mirrors the C# switch at `:156-180` exactly:
//
//   - CutoffNotMet=false → ReasonDiskCutoffMet
//   - IsUpgradable=BetterQuality → ReasonDiskHigherPreference
//   - IsUpgradable=BetterRevision → ReasonDiskHigherRevision
//   - IsUpgradable=QualityCutoff → ReasonDiskCutoffMet
//   - IsUpgradable=CustomFormatCutoff → ReasonDiskCustomFormatCutoffMet
//   - IsUpgradable=CustomFormatScore → ReasonDiskCustomFormatScore
//   - IsUpgradable=MinCustomFormatScore → ReasonDiskCustomFormatScoreIncrement
//   - IsUpgradable=UpgradesNotAllowed → ReasonDiskUpgradesNotAllowed
//
// # Defensive Accept arms
//
// Profile-resolver errors / nil-Subject / nil-Series / nil-Series.QualityProfileID
// / profile-not-found / nil-ParsedEpisodeInfo all Accept defensively.
// Per-file decode and calculator errors skip the file (match the
// surrounding decisionengine spec posture — a transient DB hiccup
// shouldn't reject every candidate). The C# tree dereferences
// `subject.Series.QualityProfile.Value` directly and would NPE on a
// missing profile; the Go port chooses the safer fallthrough.
//
// # Why Default priority + Permanent kind
//
// Mirrors C# `:28-29`. The cutoff/upgrade decision depends on per-file
// EpisodeFile + CustomFormat fetches but no filesystem reads, so
// Default priority is correct. Once an existing file is at-or-above
// the cutoff or is strictly better than the candidate, no future
// re-evaluation lifts the rejection — Permanent.
type UpgradeDiskSpecification struct {
	upgradable    UpgradeDiskCutoffChecker
	profileLookup UpgradeDiskProfileReader
	files         EpisodeFileResolver
	formats       UpgradeDiskFormatCalculator
	seasonPack    UpgradeDiskSeasonReader
	log           *slog.Logger
}

// NewUpgradeDiskSpecification builds the spec. log defaults to
// [slog.Default] when nil. upgradable / profileLookup / files /
// formats / seasonPack MUST be non-nil.
func NewUpgradeDiskSpecification(
	upgradable UpgradeDiskCutoffChecker,
	profileLookup UpgradeDiskProfileReader,
	files EpisodeFileResolver,
	formats UpgradeDiskFormatCalculator,
	seasonPack UpgradeDiskSeasonReader,
	log *slog.Logger,
) (*UpgradeDiskSpecification, error) {
	if upgradable == nil {
		return nil, fmt.Errorf("decisionengine: UpgradeDiskSpecification requires a non-nil UpgradeDiskCutoffChecker")
	}
	if profileLookup == nil {
		return nil, fmt.Errorf("decisionengine: UpgradeDiskSpecification requires a non-nil UpgradeDiskProfileReader")
	}
	if files == nil {
		return nil, fmt.Errorf("decisionengine: UpgradeDiskSpecification requires a non-nil EpisodeFileResolver")
	}
	if formats == nil {
		return nil, fmt.Errorf("decisionengine: UpgradeDiskSpecification requires a non-nil UpgradeDiskFormatCalculator")
	}
	if seasonPack == nil {
		return nil, fmt.Errorf("decisionengine: UpgradeDiskSpecification requires a non-nil UpgradeDiskSeasonReader")
	}
	if log == nil {
		log = slog.Default()
	}
	return &UpgradeDiskSpecification{
		upgradable:    upgradable,
		profileLookup: profileLookup,
		files:         files,
		formats:       formats,
		seasonPack:    seasonPack,
		log:           log,
	}, nil
}

// Name returns the canonical spec name.
func (*UpgradeDiskSpecification) Name() string { return UpgradeDiskSpecificationName }

// Kind reports Permanent — once an existing file at-or-above cutoff
// blocks an upgrade, future retries don't lift it.
func (*UpgradeDiskSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — no filesystem reads.
func (*UpgradeDiskSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:31-126`.
func (s *UpgradeDiskSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	if subject.Series.QualityProfileID == 0 {
		return Accept()
	}
	view, ok, err := s.profileLookup.LookupProfile(ctx, subject.Series.QualityProfileID)
	if err != nil {
		s.log.Debug("upgradeDisk: profile lookup failed; accepting (defensive)",
			"err", err, "seriesID", subject.Series.ID)
		return Accept()
	}
	if !ok || view == nil || view.Profile == nil {
		return Accept()
	}

	if subject.ParsedEpisodeInfo.FullSeason {
		return s.evaluateSeasonPack(ctx, subject, view)
	}
	return s.evaluatePerEpisode(ctx, subject, view)
}

// evaluateSeasonPack mirrors C# `:35-114`.
func (s *UpgradeDiskSpecification) evaluateSeasonPack(ctx context.Context, subject *Subject, view *ProfileView) SpecDecision {
	totalEpisodesInPack := len(subject.Episodes)
	if totalEpisodesInPack == 0 {
		// "Should not happen, but good to guard against it." (C# `:39-43`)
		return Accept()
	}

	// Count missing episodes as upgradable (C# `:46-48`).
	missingCount := 0
	existing := make([]int, 0, totalEpisodesInPack)
	for _, ep := range subject.Episodes {
		if ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
			missingCount++
			continue
		}
		existing = append(existing, *ep.EpisodeFileID)
	}
	upgradedCount := missingCount
	s.log.Debug("upgradeDisk: missing episodes considered upgradable", "count", upgradedCount)

	// All episodes missing → accept immediately (C# `:56-60`).
	if len(existing) == 0 {
		s.log.Debug("upgradeDisk: all episodes in season pack are missing, accepting")
		return Accept()
	}

	candidateQuality := subject.ParsedEpisodeInfo.Quality
	for _, fileID := range existing {
		file, err := s.files.GetFile(ctx, fileID)
		if err != nil {
			if errors.Is(err, episodefiles.ErrNotFound) {
				continue
			}
			s.log.Warn("upgradeDisk: episode-file lookup failed; skipping",
				"episodeFileId", fileID, "err", err)
			continue
		}
		if file == nil {
			continue
		}
		fileQuality, err := decodeFileQuality(file)
		if err != nil {
			s.log.Warn("upgradeDisk: decode existing-file quality; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}
		fileFormats, err := s.formats.ParseCustomFormat(ctx, file, subject.Series)
		if err != nil {
			s.log.Warn("upgradeDisk: per-file custom-format calc failed; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}
		if !s.upgradable.CutoffNotMet(view, fileQuality, fileFormats, &candidateQuality) {
			s.log.Debug("upgradeDisk: cutoff already met for existing file, not an upgrade",
				"episodeFileId", file.ID)
			continue
		}
		reason := s.upgradable.IsUpgradable(view, fileQuality, fileFormats, &candidateQuality, subject.CustomFormats)
		if reason == UpgradeableNone {
			s.log.Debug("upgradeDisk: existing episode is upgradable", "episodeFileId", file.ID)
			upgradedCount++
		}
	}

	mode := s.seasonPack.SeasonPackUpgrade()
	threshold := s.seasonPack.SeasonPackUpgradeThreshold()
	upgradablePercentage := float64(upgradedCount) / float64(totalEpisodesInPack) * 100.0
	s.log.Debug("upgradeDisk: season-pack tally",
		"upgraded", upgradedCount, "total", totalEpisodesInPack,
		"mode", mode, "threshold", threshold, "percentage", upgradablePercentage)

	switch mode {
	case SeasonPackUpgradeAny:
		if upgradedCount > 0 {
			return Accept()
		}
	default:
		// SeasonPackUpgradeAll → effective threshold 100; otherwise the
		// configured threshold (mirrors C# `:104-106`).
		effective := threshold
		if mode == SeasonPackUpgradeAll {
			effective = 100.0
		}
		if upgradablePercentage >= effective {
			return Accept()
		}
	}
	return Reject(ReasonDiskNotUpgrade,
		fmt.Sprintf("Season pack does not meet the upgrade criteria. Upgradable: %d/%d (%.2f%%), Mode: %s, Threshold: %g%%",
			upgradedCount, totalEpisodesInPack, upgradablePercentage, mode, threshold))
}

// evaluatePerEpisode mirrors C# `:116-184`.
func (s *UpgradeDiskSpecification) evaluatePerEpisode(ctx context.Context, subject *Subject, view *ProfileView) SpecDecision {
	candidateQuality := subject.ParsedEpisodeInfo.Quality
	for _, ep := range subject.Episodes {
		if ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
			continue
		}
		file, err := s.files.GetFile(ctx, *ep.EpisodeFileID)
		if err != nil {
			if errors.Is(err, episodefiles.ErrNotFound) {
				continue
			}
			s.log.Warn("upgradeDisk: episode-file lookup failed; skipping",
				"episodeFileId", *ep.EpisodeFileID, "err", err)
			continue
		}
		if file == nil {
			s.log.Debug("upgradeDisk: file is no longer available, skipping this file")
			continue
		}
		fileQuality, err := decodeFileQuality(file)
		if err != nil {
			s.log.Warn("upgradeDisk: decode existing-file quality; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}
		fileFormats, err := s.formats.ParseCustomFormat(ctx, file, subject.Series)
		if err != nil {
			s.log.Warn("upgradeDisk: per-file custom-format calc failed; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}

		if !s.upgradable.CutoffNotMet(view, fileQuality, fileFormats, &candidateQuality) {
			s.log.Debug("upgradeDisk: cutoff already met, rejecting", "episodeFileId", file.ID)
			return Reject(ReasonDiskCutoffMet,
				fmt.Sprintf("Existing file meets cutoff: %s", cutoffQualityName(view)))
		}

		reason := s.upgradable.IsUpgradable(view, fileQuality, fileFormats, &candidateQuality, subject.CustomFormats)
		switch reason {
		case UpgradeableNone:
			continue
		case UpgradeableBetterQuality:
			return Reject(ReasonDiskHigherPreference,
				fmt.Sprintf("Existing file on disk is of equal or higher preference: %s", fileQuality))
		case UpgradeableBetterRevision:
			return Reject(ReasonDiskHigherRevision,
				fmt.Sprintf("Existing file on disk is of equal or higher revision: %v", fileQuality.Revision))
		case UpgradeableQualityCutoff:
			return Reject(ReasonDiskCutoffMet,
				fmt.Sprintf("Existing file on disk meets quality cutoff: %s", explicitCutoffQualityName(view)))
		case UpgradeableCustomFormatCutoff:
			return Reject(ReasonDiskCustomFormatCutoffMet,
				fmt.Sprintf("Existing file on disk meets Custom Format cutoff: %d", view.Profile.CutoffFormatScore))
		case UpgradeableCustomFormatScore:
			return Reject(ReasonDiskCustomFormatScore,
				fmt.Sprintf("Existing file on disk has a equal or higher Custom Format score: %d",
					scoreCustomFormats(view, fileFormats)))
		case UpgradeableMinCustomFormatScore:
			return Reject(ReasonDiskCustomFormatScoreIncrement,
				fmt.Sprintf("Existing file on disk has Custom Format score within Custom Format score increment: %d",
					view.Profile.MinUpgradeFormatScore))
		case UpgradeableUpgradesNotAllowed:
			return Reject(ReasonDiskUpgradesNotAllowed,
				fmt.Sprintf("Existing file on disk and Quality Profile '%s' does not allow upgrades", view.Profile.Name))
		}
	}
	return Accept()
}

// cutoffQualityName mirrors the C# `:144-145` cutoff-resolution branch
// — the explicit profile cutoff when UpgradeAllowed, otherwise the
// first-allowed-quality. Returns the typed Items[index] name; falls
// back to a numeric form when the items slice can't resolve the
// index (defensive — should never happen for well-formed profiles).
func cutoffQualityName(view *ProfileView) string {
	cutoffID := view.Profile.Cutoff
	if !view.Profile.UpgradeAllowed {
		if first, ok := view.FirstAllowedQuality(); ok {
			cutoffID = first.ID
		}
	}
	return resolveCutoffName(view, cutoffID)
}

// explicitCutoffQualityName resolves the Profile.Cutoff column
// directly — used by the UpgradeableQualityCutoff arm, mirroring C#
// `:168` which always passes `qualityProfile.Cutoff` regardless of
// UpgradeAllowed.
func explicitCutoffQualityName(view *ProfileView) string {
	return resolveCutoffName(view, view.Profile.Cutoff)
}

func resolveCutoffName(view *ProfileView, cutoffID int) string {
	idx, ok := qualityprofiles.LookupIndexByID(view.Items, cutoffID, false)
	if !ok || idx.Index < 0 || idx.Index >= len(view.Items) {
		if q, ok := quality.FindByID(cutoffID); ok {
			return q.Name
		}
		return fmt.Sprintf("Quality(%d)", cutoffID)
	}
	item := view.Items[idx.Index]
	if item.IsGroup() {
		return item.Name
	}
	return item.Quality.Name
}

// scoreCustomFormats wraps [qualityprofiles.CalculateCustomFormatScore]
// with the presence-set conversion the Subject-side
// `[]customformats.CustomFormat` shape requires. Mirrors the
// helper at upgradable.go:373 — duplicated here so the spec doesn't
// reach into UpgradableSpecification's private surface.
func scoreCustomFormats(view *ProfileView, formats []customformats.CustomFormat) int {
	if len(formats) == 0 {
		return qualityprofiles.CalculateCustomFormatScore(view.FormatItems, nil)
	}
	present := make(map[int]bool, len(formats))
	for _, f := range formats {
		present[f.ID] = true
	}
	return qualityprofiles.CalculateCustomFormatScore(view.FormatItems, present)
}

// Compile-time interface satisfaction.
var _ Specification = (*UpgradeDiskSpecification)(nil)
