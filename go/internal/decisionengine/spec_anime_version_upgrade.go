package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// AnimeVersionUpgradeSpecificationName is the canonical Spec.Name() identifier.
const AnimeVersionUpgradeSpecificationName = "AnimeVersionUpgrade"

// AnimeVersionUpgradeSpecification rejects anime-series propers/repacks
// that would upgrade an existing file but whose release group differs
// from the existing file's release group.
//
// Anime-specific because the convention in the anime release scene is
// that a v2/v3 release within the SAME group is a legitimate fix
// (timing tweak, encoder retag, etc.) while a v2 release from a
// DIFFERENT group is functionally a re-release that the user shouldn't
// silently swap in for their existing copy.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/AnimeVersionUpgradeSpecification.cs:1-71`.
//
// # Algorithm
//
//  1. Series.SeriesType != Anime → accept (anime-only check).
//  2. DownloadPropersAndRepacks == DoNotPrefer → accept (user
//     disabled version-upgrade handling).
//  3. For each Subject.Episode whose EpisodeFileID > 0:
//     a. Resolve the existing EpisodeFile via [EpisodeFileResolver].
//     Lookup miss / nil → continue.
//     b. Skip when not [UpgradableRevisionChecker.IsRevisionUpgrade]
//     — only revision upgrades enter the release-group compare.
//     c. file.ReleaseGroup empty → reject [ReasonUnknownReleaseGroup].
//     d. release.ReleaseGroup empty → reject
//     [ReasonUnknownReleaseGroup].
//     e. case-sensitive mismatch → reject
//     [ReasonReleaseGroupDoesNotMatch] (NB: the C# tree uses
//     case-sensitive `!=` here, distinct from RepackSpec's
//     `EqualsIgnoreCase`. Mirrored verbatim — anime release-group
//     casing IS significant in the scene's convention).
//
// Permanent rejection — release-group identity doesn't flip without
// a fresh Subject.
type AnimeVersionUpgradeSpecification struct {
	upgradable UpgradableRevisionChecker
	cfg        PropersAndRepacksConfigReader
	files      EpisodeFileResolver
	log        *slog.Logger
}

// NewAnimeVersionUpgradeSpecification builds the spec. log defaults
// to [slog.Default] when nil. upgradable / cfg / files MUST be
// non-nil.
func NewAnimeVersionUpgradeSpecification(
	upgradable UpgradableRevisionChecker,
	cfg PropersAndRepacksConfigReader,
	files EpisodeFileResolver,
	log *slog.Logger,
) (*AnimeVersionUpgradeSpecification, error) {
	if upgradable == nil {
		return nil, fmt.Errorf("decisionengine: AnimeVersionUpgradeSpecification requires a non-nil UpgradableRevisionChecker")
	}
	if cfg == nil {
		return nil, fmt.Errorf("decisionengine: AnimeVersionUpgradeSpecification requires a non-nil PropersAndRepacksConfigReader")
	}
	if files == nil {
		return nil, fmt.Errorf("decisionengine: AnimeVersionUpgradeSpecification requires a non-nil EpisodeFileResolver")
	}
	if log == nil {
		log = slog.Default()
	}
	return &AnimeVersionUpgradeSpecification{upgradable: upgradable, cfg: cfg, files: files, log: log}, nil
}

// Name returns the canonical spec name.
func (*AnimeVersionUpgradeSpecification) Name() string {
	return AnimeVersionUpgradeSpecificationName
}

// Kind reports Permanent.
func (*AnimeVersionUpgradeSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default.
func (*AnimeVersionUpgradeSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:30-70`.
func (s *AnimeVersionUpgradeSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.ParsedEpisodeInfo == nil || subject.Series == nil {
		return Accept()
	}
	if subject.Series.SeriesType != series.SeriesTypeAnime {
		return Accept()
	}
	if s.cfg.DownloadPropersAndRepacks() == quality.ProperDoNotPrefer {
		s.log.Debug("animeVersionUpgrade: version upgrades not preferred, skipping check")
		return Accept()
	}

	releaseGroup := subject.ParsedEpisodeInfo.ReleaseGroup

	for _, ep := range subject.Episodes {
		if ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
			continue
		}
		file, err := s.files.GetFile(ctx, *ep.EpisodeFileID)
		if err != nil {
			if errors.Is(err, episodefiles.ErrNotFound) {
				continue
			}
			s.log.Warn("animeVersionUpgrade: episode-file lookup failed; skipping",
				"episodeFileId", *ep.EpisodeFileID, "err", err)
			continue
		}
		if file == nil {
			continue
		}
		fileQuality, err := decodeFileQuality(file)
		if err != nil {
			s.log.Warn("animeVersionUpgrade: decode existing-file quality; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}
		if !s.upgradable.IsRevisionUpgrade(fileQuality, subject.ParsedEpisodeInfo.Quality) {
			continue
		}

		fileReleaseGroup := strings.TrimSpace(file.ReleaseGroup)
		if fileReleaseGroup == "" {
			s.log.Debug("animeVersionUpgrade: existing file's release group unknown")
			return Reject(ReasonUnknownReleaseGroup, "Existing release group is unknown")
		}
		if strings.TrimSpace(releaseGroup) == "" {
			s.log.Debug("animeVersionUpgrade: release's release group unknown")
			return Reject(ReasonUnknownReleaseGroup, "Release group is unknown")
		}
		// C# `file.ReleaseGroup != releaseGroup` is case-sensitive —
		// preserved verbatim. Anime release-group casing is
		// significant (`HorribleSubs` ≠ `horriblesubs`).
		if file.ReleaseGroup != releaseGroup {
			s.log.Debug("animeVersionUpgrade: release-group mismatch",
				"releaseGroup", releaseGroup, "fileReleaseGroup", file.ReleaseGroup)
			return Reject(ReasonReleaseGroupDoesNotMatch,
				fmt.Sprintf("%s does not match existing release group %s",
					releaseGroup, file.ReleaseGroup))
		}
	}
	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*AnimeVersionUpgradeSpecification)(nil)
