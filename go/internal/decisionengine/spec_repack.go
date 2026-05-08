package decisionengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/quality"
)

// decodeFileQuality unmarshals the embedded JSON Quality column on an
// EpisodeFile row into a typed [quality.Model]. Empty / `"{}"`
// Quality strings decode to a zero Model — matches the
// internal/episodeimport package's identical helper at
// `internal/episodeimport/specs.go:1198`. Defined here as a shared
// decisionengine helper because RepackSpec, AnimeVersionUpgradeSpec,
// and any future spec that dereferences Episode.EpisodeFileID all
// need the same parse step.
func decodeFileQuality(file *episodefiles.File) (quality.Model, error) {
	if file == nil || strings.TrimSpace(file.Quality) == "" || file.Quality == "{}" {
		return quality.Model{}, nil
	}
	var m quality.Model
	if err := json.Unmarshal([]byte(file.Quality), &m); err != nil {
		return quality.Model{}, fmt.Errorf("decisionengine: decode EpisodeFile.Quality: %w", err)
	}
	return m, nil
}

// RepackSpecificationName is the canonical Spec.Name() identifier.
const RepackSpecificationName = "Repack"

// EpisodeFileResolver narrows the C# `IEpisodeFileService.Get` (one
// method) the way the decisionengine specs reach for it. Returns the
// persisted [*episodefiles.File] for the given fileID. nil-or-error
// signals "no file" — callers (RepackSpec, AnimeVersionUpgradeSpec,
// future UpgradeDiskSpec) all skip the per-file branch defensively
// when the lookup fails.
//
// Production binding: [*episodefiles.Service].GetFile satisfies this
// interface via Go's structural typing.
//
// The narrow interface lives next to the first spec that uses it
// (RepackSpec) but is shared by AnimeVersionUpgradeSpec and any
// future spec that needs to dereference Episode.EpisodeFileID.
type EpisodeFileResolver interface {
	GetFile(ctx context.Context, fileID int) (*episodefiles.File, error)
}

// UpgradableRevisionChecker narrows
// [UpgradableSpecification.IsRevisionUpgrade] (one method). Defining
// the dep behind an interface keeps RepackSpec/AnimeVersionUpgradeSpec
// stub-able in tests without constructing a full UpgradableSpec.
//
// Production binding: [*UpgradableSpecification].IsRevisionUpgrade
// satisfies this interface.
type UpgradableRevisionChecker interface {
	IsRevisionUpgrade(currentQuality, newQuality quality.Model) bool
}

// PropersAndRepacksConfigReader narrows [config.Provider].DownloadPropersAndRepacks
// (one method). Shared by RepackSpec + AnimeVersionUpgradeSpec.
type PropersAndRepacksConfigReader interface {
	DownloadPropersAndRepacks() quality.ProperDownloadType
}

// RepackSpecification rejects repack releases when the user's
// proper/repack policy disallows the upgrade OR the repack's release
// group differs from the existing file's release group.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RepackSpecification.cs:1-89`.
//
// # Algorithm
//
//  1. ParsedEpisodeInfo.Quality.Revision.IsRepack == false → accept
//     (not a repack, no check).
//  2. DownloadPropersAndRepacks == DoNotPrefer → accept (user
//     disabled repack handling).
//  3. For each Subject.Episode whose EpisodeFileID > 0:
//     a. Resolve the existing EpisodeFile via [EpisodeFileResolver].
//     Lookup failure / nil file → skip the episode (matches the C#
//     `c.EpisodeFile.Value` lazy-load returning null on missing
//     row).
//     b. Skip when not [UpgradableRevisionChecker.IsRevisionUpgrade]
//     — only repacks that would be a revision upgrade enter the
//     release-group compare.
//     c. DownloadPropersAndRepacks == DoNotUpgrade → reject with
//     [ReasonRepackDisabled].
//     d. fileReleaseGroup empty → reject [ReasonRepackUnknownReleaseGroup].
//     e. releaseReleaseGroup empty → reject [ReasonRepackUnknownReleaseGroup].
//     f. case-insensitive mismatch → reject
//     [ReasonRepackReleaseGroupDoesNotMatch].
//
// Permanent rejection — the release-group / repack-policy decisions
// don't flip without a config change which produces a fresh Subject.
type RepackSpecification struct {
	upgradable UpgradableRevisionChecker
	cfg        PropersAndRepacksConfigReader
	files      EpisodeFileResolver
	log        *slog.Logger
}

// NewRepackSpecification builds the spec. log defaults to
// [slog.Default] when nil. upgradable / cfg / files MUST be non-nil.
func NewRepackSpecification(
	upgradable UpgradableRevisionChecker,
	cfg PropersAndRepacksConfigReader,
	files EpisodeFileResolver,
	log *slog.Logger,
) (*RepackSpecification, error) {
	if upgradable == nil {
		return nil, fmt.Errorf("decisionengine: RepackSpecification requires a non-nil UpgradableRevisionChecker")
	}
	if cfg == nil {
		return nil, fmt.Errorf("decisionengine: RepackSpecification requires a non-nil PropersAndRepacksConfigReader")
	}
	if files == nil {
		return nil, fmt.Errorf("decisionengine: RepackSpecification requires a non-nil EpisodeFileResolver")
	}
	if log == nil {
		log = slog.Default()
	}
	return &RepackSpecification{upgradable: upgradable, cfg: cfg, files: files, log: log}, nil
}

// Name returns the canonical spec name.
func (*RepackSpecification) Name() string { return RepackSpecificationName }

// Kind reports Permanent.
func (*RepackSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Database — needs a per-episode file lookup.
func (*RepackSpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:30-87`.
func (s *RepackSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	if !subject.ParsedEpisodeInfo.Quality.Revision.IsRepack {
		return Accept()
	}

	if s.cfg.DownloadPropersAndRepacks() == quality.ProperDoNotPrefer {
		s.log.Debug("repack: propers/repacks not preferred, skipping check")
		return Accept()
	}

	releaseGroup := strings.TrimSpace(subject.ParsedEpisodeInfo.ReleaseGroup)

	for _, ep := range subject.Episodes {
		if ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
			continue
		}
		file, err := s.files.GetFile(ctx, *ep.EpisodeFileID)
		if err != nil {
			// "row not found" is the expected miss. C# `EpisodeFile.Value`
			// lazy-load returns null on missing row. Treat any lookup
			// failure as "skip this episode" — a transient DB hiccup
			// shouldn't reject the whole release.
			if errors.Is(err, episodefiles.ErrNotFound) {
				continue
			}
			s.log.Warn("repack: episode-file lookup failed; skipping",
				"episodeFileId", *ep.EpisodeFileID, "err", err)
			continue
		}
		if file == nil {
			continue
		}
		fileQuality, err := decodeFileQuality(file)
		if err != nil {
			s.log.Warn("repack: decode existing-file quality; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}
		if !s.upgradable.IsRevisionUpgrade(fileQuality, subject.ParsedEpisodeInfo.Quality) {
			continue
		}

		if s.cfg.DownloadPropersAndRepacks() == quality.ProperDoNotUpgrade {
			s.log.Debug("repack: auto-downloading repacks disabled")
			return Reject(ReasonRepackDisabled, "Repack downloading is disabled")
		}

		fileReleaseGroup := strings.TrimSpace(file.ReleaseGroup)
		if fileReleaseGroup == "" {
			return Reject(ReasonRepackUnknownReleaseGroup,
				"Unable to determine release group for the existing file")
		}
		if releaseGroup == "" {
			return Reject(ReasonRepackUnknownReleaseGroup,
				"Unable to determine release group for this release")
		}
		if !strings.EqualFold(fileReleaseGroup, releaseGroup) {
			s.log.Debug("repack: release-group mismatch",
				"releaseGroup", releaseGroup, "fileReleaseGroup", fileReleaseGroup)
			return Reject(ReasonRepackReleaseGroupDoesNotMatch,
				fmt.Sprintf("Release is a repack for a different release group. Release Group: %s. File release group: %s",
					releaseGroup, fileReleaseGroup))
		}
	}
	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*RepackSpecification)(nil)
