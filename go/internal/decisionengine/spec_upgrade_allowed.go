package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// UpgradeAllowedSpecificationName is the canonical Spec.Name()
// identifier.
const UpgradeAllowedSpecificationName = "UpgradeAllowed"

// UpgradeAllowedProfileReader narrows the profile-resolver dep for
// [UpgradeAllowedSpecification]. Same shape as the slice-3.65
// [AcceptableSizeProfileReader] / [QualityAllowedByProfileProfileReader]
// / slice-3.AO [CustomFormatAllowedByProfileProfileReader] — defined
// separately so the compile-time interface satisfaction stays scoped
// to one spec (Go's structural typing lets the same backing
// implementation satisfy all of them).
//
// Returns the parsed [*ProfileView] for a given `qualityProfileID`,
// `ok=false` when the profile doesn't exist, or `(nil, false, err)`
// when the lookup fails outright.
type UpgradeAllowedProfileReader interface {
	LookupProfile(ctx context.Context, qualityProfileID int) (*ProfileView, bool, error)
}

// UpgradeAllowedChecker narrows [UpgradableSpecification.IsUpgradeAllowed]
// (one method). Keeps the spec stub-able in tests without constructing
// a full *UpgradableSpecification (which itself requires a ConfigReader
// that the spec body never touches).
//
// Production binding: [*UpgradableSpecification].IsUpgradeAllowed
// satisfies this interface via Go's structural typing.
type UpgradeAllowedChecker interface {
	IsUpgradeAllowed(
		view *ProfileView,
		currentQuality quality.Model,
		currentCustomFormats []customformats.CustomFormat,
		newQuality quality.Model,
		newCustomFormats []customformats.CustomFormat,
	) bool
}

// UpgradeAllowedFormatCalculator narrows the by-EpisodeFile
// CustomFormat calculator dep — the C# `ICustomFormatCalculationService.ParseCustomFormat(EpisodeFile, Series)`
// overload at `CustomFormats/CustomFormatCalculationService.cs:170-211`.
//
// The Go-port package-level free fn lives at
// [customformats.ParseCustomFormatForFile] (slice 3.AP) and walks the
// full *Format catalogue against an EpisodeFile-derived
// CustomFormatInput. The narrow interface bundles the catalogue
// fetch + free-fn invocation behind one method so the spec doesn't
// have to import customformats.Service directly.
//
// Returning an error lets the production adapter surface a transient
// catalogue-fetch failure; the spec swallows the error defensively
// (per the rest of the decisionengine surface — DB hiccup shouldn't
// reject every candidate).
type UpgradeAllowedFormatCalculator interface {
	ParseCustomFormat(ctx context.Context, file *episodefiles.File, series *series.Series) ([]customformats.CustomFormat, error)
}

// UpgradeAllowedSpecification rejects upgrade-bound releases whose
// destination profile's UpgradeAllowed policy + per-file custom-format
// score forbid the swap.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/UpgradeAllowedSpecification.cs:1-57`.
//
// # Algorithm (mirrors C# `:26-55`)
//
//  1. Resolve the series' QualityProfile via the narrow reader.
//     Profile-resolver errors / nil-Series / nil-Series-QualityProfileID
//     / profile-not-found → defensive Accept (matches the rest of the
//     decisionengine surface — a quality-profile DB hiccup shouldn't
//     reject every candidate; the C# tree dereferences
//     `subject.Series.QualityProfile.Value` directly and would NPE on
//     a missing profile).
//  2. For each Subject.Episode whose EpisodeFileID > 0:
//     a. Resolve the existing EpisodeFile via [EpisodeFileResolver].
//     Lookup miss / nil → continue (matches C# `:30-36`'s
//     `file == null → continue`).
//     b. Decode the file's persisted Quality column. Decode error →
//     skip the file defensively (the C# tree relies on EF lazy-load
//     to deliver a typed Quality; a malformed JSON column shouldn't
//     reject every candidate).
//     c. Compute the file's per-CustomFormat list via the narrow
//     [UpgradeAllowedFormatCalculator]. Calculator error → continue
//     (defensive — same posture as the file-quality decode).
//     d. Call [UpgradableSpecification.IsUpgradeAllowed] with the
//     decoded file quality + per-file CFs vs the candidate's
//     [parser.ParsedEpisodeInfo.Quality] + [Subject.CustomFormats].
//     False → reject [ReasonQualityUpgradesDisabled].
//  3. All files pass → accept.
//
// # Why Default priority + Permanent kind
//
// Mirrors C# `:23-24`. The profile's UpgradeAllowed flag is a static
// per-profile config — no DB hit per candidate beyond the per-file
// EpisodeFile + CustomFormat fetches the spec already does. Default
// priority is correct. Once UpgradeAllowed=false, no future
// re-evaluation lifts the rejection — Permanent.
type UpgradeAllowedSpecification struct {
	upgradable    UpgradeAllowedChecker
	profileLookup UpgradeAllowedProfileReader
	files         EpisodeFileResolver
	formats       UpgradeAllowedFormatCalculator
	log           *slog.Logger
}

// NewUpgradeAllowedSpecification builds the spec. log defaults to
// [slog.Default] when nil. upgradable / profileLookup / files /
// formats MUST be non-nil.
func NewUpgradeAllowedSpecification(
	upgradable UpgradeAllowedChecker,
	profileLookup UpgradeAllowedProfileReader,
	files EpisodeFileResolver,
	formats UpgradeAllowedFormatCalculator,
	log *slog.Logger,
) (*UpgradeAllowedSpecification, error) {
	if upgradable == nil {
		return nil, fmt.Errorf("decisionengine: UpgradeAllowedSpecification requires a non-nil UpgradeAllowedChecker")
	}
	if profileLookup == nil {
		return nil, fmt.Errorf("decisionengine: UpgradeAllowedSpecification requires a non-nil UpgradeAllowedProfileReader")
	}
	if files == nil {
		return nil, fmt.Errorf("decisionengine: UpgradeAllowedSpecification requires a non-nil EpisodeFileResolver")
	}
	if formats == nil {
		return nil, fmt.Errorf("decisionengine: UpgradeAllowedSpecification requires a non-nil UpgradeAllowedFormatCalculator")
	}
	if log == nil {
		log = slog.Default()
	}
	return &UpgradeAllowedSpecification{
		upgradable:    upgradable,
		profileLookup: profileLookup,
		files:         files,
		formats:       formats,
		log:           log,
	}, nil
}

// Name returns the canonical spec name.
func (*UpgradeAllowedSpecification) Name() string {
	return UpgradeAllowedSpecificationName
}

// Kind reports Permanent — UpgradeAllowed=false on the profile
// doesn't flip without a fresh Subject.
func (*UpgradeAllowedSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — the profile flag is static config.
func (*UpgradeAllowedSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:26-55`.
func (s *UpgradeAllowedSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	if subject.Series.QualityProfileID == 0 {
		return Accept()
	}
	view, ok, err := s.profileLookup.LookupProfile(ctx, subject.Series.QualityProfileID)
	if err != nil {
		s.log.Debug("upgradeAllowed: profile lookup failed; accepting (defensive)",
			"err", err, "seriesID", subject.Series.ID)
		return Accept()
	}
	if !ok || view == nil || view.Profile == nil {
		return Accept()
	}

	for _, ep := range subject.Episodes {
		if ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
			continue
		}
		file, err := s.files.GetFile(ctx, *ep.EpisodeFileID)
		if err != nil {
			if errors.Is(err, episodefiles.ErrNotFound) {
				continue
			}
			s.log.Warn("upgradeAllowed: episode-file lookup failed; skipping",
				"episodeFileId", *ep.EpisodeFileID, "err", err)
			continue
		}
		if file == nil {
			s.log.Debug("upgradeAllowed: file is no longer available, skipping this file")
			continue
		}
		fileQuality, err := decodeFileQuality(file)
		if err != nil {
			s.log.Warn("upgradeAllowed: decode existing-file quality; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}
		fileFormats, err := s.formats.ParseCustomFormat(ctx, file, subject.Series)
		if err != nil {
			s.log.Warn("upgradeAllowed: per-file custom-format calc failed; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}
		s.log.Debug("upgradeAllowed: comparing file quality with report",
			"existing", fileQuality, "candidate", subject.ParsedEpisodeInfo.Quality)
		if !s.upgradable.IsUpgradeAllowed(view, fileQuality, fileFormats, subject.ParsedEpisodeInfo.Quality, subject.CustomFormats) {
			s.log.Debug("upgradeAllowed: upgrading is not allowed by the quality profile",
				"profile", view.Profile.Name)
			return Reject(ReasonQualityUpgradesDisabled,
				"Existing file and the Quality profile does not allow upgrades")
		}
	}
	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*UpgradeAllowedSpecification)(nil)
