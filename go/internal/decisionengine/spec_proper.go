package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/quality"
)

// ProperSpecificationName is the canonical Spec.Name() identifier.
const ProperSpecificationName = "Proper"

// properOldFileWindow is the age past which a revision-upgrade (proper)
// for an existing file is rejected — C# `DateTime.Today.AddDays(-7)` at
// `ProperSpecification.cs:52`.
const properOldFileWindow = 7 * 24 * time.Hour

// ProperSpecification rejects a proper (revision upgrade) when the user
// disabled auto-downloading propers, or when the existing file is older
// than 7 days (a proper for a long-settled file isn't worth re-grabbing).
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RssSync/ProperSpecification.cs:1-66`.
// Reuses the shared [EpisodeFileResolver], [UpgradableRevisionChecker]
// and [PropersAndRepacksConfigReader] interfaces (declared in
// spec_repack.go) plus [decodeFileQuality].
//
// # Algorithm (C# `:27-64`)
//
//  1. SearchCriteria present → Accept (the gate is RSS-only; an explicit
//     search may want the proper regardless).
//  2. DownloadPropersAndRepacks == DoNotPrefer → Accept.
//  3. For each Subject.Episode with an existing file: resolve the file;
//     if the candidate is a revision upgrade over it:
//     a. DoNotUpgrade → Reject [ReasonPropersDisabled].
//     b. file.DateAdded older than 7 days → Reject [ReasonProperForOldFile].
//
// Permanent rejection — the policy + file age are stable for a Subject.
type ProperSpecification struct {
	upgradable UpgradableRevisionChecker
	cfg        PropersAndRepacksConfigReader
	files      EpisodeFileResolver
	now        func() time.Time
	log        *slog.Logger
}

// NewProperSpecification builds the spec. upgradable / cfg / files MUST
// be non-nil. now defaults to [time.Now] when nil; log to [slog.Default].
func NewProperSpecification(
	upgradable UpgradableRevisionChecker,
	cfg PropersAndRepacksConfigReader,
	files EpisodeFileResolver,
	now func() time.Time,
	log *slog.Logger,
) (*ProperSpecification, error) {
	if upgradable == nil {
		return nil, fmt.Errorf("decisionengine: ProperSpecification requires a non-nil UpgradableRevisionChecker")
	}
	if cfg == nil {
		return nil, fmt.Errorf("decisionengine: ProperSpecification requires a non-nil PropersAndRepacksConfigReader")
	}
	if files == nil {
		return nil, fmt.Errorf("decisionengine: ProperSpecification requires a non-nil EpisodeFileResolver")
	}
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &ProperSpecification{upgradable: upgradable, cfg: cfg, files: files, now: now, log: log}, nil
}

// Name returns the canonical spec name.
func (*ProperSpecification) Name() string { return ProperSpecificationName }

// Kind reports Permanent.
func (*ProperSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Database — needs a per-episode file lookup.
func (*ProperSpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:27-64`.
func (s *ProperSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	// C# `:29-32` — search results skip the proper gate.
	if criteria, ok := info.SearchCriteria.(*SearchCriteria); ok && criteria != nil {
		return Accept()
	}

	if s.cfg.DownloadPropersAndRepacks() == quality.ProperDoNotPrefer {
		s.log.Debug("proper: propers/repacks not preferred, skipping check")
		return Accept()
	}

	// C# `DateTime.Today.AddDays(-7)`: floor to the current day, then back
	// 7 days. Computed once per evaluation.
	now := s.now().UTC()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(-properOldFileWindow)

	for _, ep := range subject.Episodes {
		if ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
			continue
		}
		file, err := s.files.GetFile(ctx, *ep.EpisodeFileID)
		if err != nil {
			if errors.Is(err, episodefiles.ErrNotFound) {
				continue
			}
			s.log.Warn("proper: episode-file lookup failed; skipping",
				"episodeFileId", *ep.EpisodeFileID, "err", err)
			continue
		}
		if file == nil {
			continue
		}
		fileQuality, err := decodeFileQuality(file)
		if err != nil {
			s.log.Warn("proper: decode existing-file quality; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}
		if !s.upgradable.IsRevisionUpgrade(fileQuality, subject.ParsedEpisodeInfo.Quality) {
			continue
		}

		if s.cfg.DownloadPropersAndRepacks() == quality.ProperDoNotUpgrade {
			s.log.Debug("proper: auto-downloading propers disabled")
			return Reject(ReasonPropersDisabled, "Proper downloading is disabled")
		}

		if file.DateAdded.Before(cutoff) {
			s.log.Debug("proper: proper for an old file, rejecting",
				"episodeFileId", file.ID, "dateAdded", file.DateAdded)
			return Reject(ReasonProperForOldFile, "Proper for old file")
		}
	}

	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*ProperSpecification)(nil)
