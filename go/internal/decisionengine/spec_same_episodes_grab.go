package decisionengine

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/episodes"
)

// SameEpisodesGrabSpecificationName is the canonical Spec.Name() identifier.
const SameEpisodesGrabSpecificationName = "SameEpisodesGrab"

// SameEpisodesEpisodeFinder narrows the C# `IEpisodeService.GetEpisodesByFileId`
// (one method) — the shared helper at C#
// `DecisionEngine/Specifications/SameEpisodesSpecification.cs:24`.
// Production binding: [*episodes.Service.GetEpisodesByFileID].
//
// The same-shape interface lives in `internal/episodeimport/specs.go`
// at slice 3.65 because the `episodeimport.IsSameEpisodes` helper (the
// per-import call site) consumes it. Rather than reach across packages
// (which would require an import cycle since episodeimport already
// depends on decisionengine via ProfileView/CompareQualityModels), the
// Go port carries a parallel narrow interface here. A future cleanup
// slice may consolidate by lifting the helper to a neutral package
// (e.g. internal/episodes).
type SameEpisodesEpisodeFinder interface {
	GetEpisodesByFileID(ctx context.Context, fileID int) ([]episodes.Episode, error)
}

// isSameEpisodesGrab is the per-release variant of the C# helper at
// `DecisionEngine/Specifications/SameEpisodesSpecification.cs:17-33`.
// Returns true when every existing on-disk file linked to one of the
// candidate's episodes covers a SUBSET of the candidate's episode
// list — i.e. grabbing this release won't leave any existing on-disk
// file with episodes the new file doesn't cover.
//
// Mirrors the [internal/episodeimport.IsSameEpisodes] helper byte-for-
// byte; lives in decisionengine because the dep-graph direction
// (episodeimport → decisionengine) precludes the reverse edge that
// importing the helper would require.
//
// Errors propagate verbatim — the caller (SameEpisodesGrabSpec)
// translates a lookup failure to defensive Accept (the C# call doesn't
// catch either, but the Go port mirrors the [SameEpisodesImportSpec]
// posture from slice 3.91 — transient DB outages shouldn't reject
// every candidate in a batch).
func isSameEpisodesGrab(ctx context.Context, finder SameEpisodesEpisodeFinder, eps []episodes.Episode) (bool, error) {
	if finder == nil {
		return false, fmt.Errorf("decisionengine: isSameEpisodesGrab: nil finder")
	}
	if len(eps) == 0 {
		return true, nil
	}

	episodeIDs := make(map[int]struct{}, len(eps))
	for _, e := range eps {
		episodeIDs[e.ID] = struct{}{}
	}

	seenFiles := make(map[int]struct{})
	for _, e := range eps {
		if e.EpisodeFileID == nil || *e.EpisodeFileID == 0 {
			continue
		}
		seenFiles[*e.EpisodeFileID] = struct{}{}
	}

	for fileID := range seenFiles {
		linkedEpisodes, err := finder.GetEpisodesByFileID(ctx, fileID)
		if err != nil {
			return false, err
		}
		for _, le := range linkedEpisodes {
			if _, ok := episodeIDs[le.ID]; !ok {
				return false, nil
			}
		}
	}
	return true, nil
}

// SameEpisodesGrabSpecification rejects a release when the existing
// on-disk file(s) linked to any of the candidate's episodes ALSO
// cover other episodes the candidate doesn't include — grabbing this
// release would leave those other episodes orphaned without their
// existing file's data.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/SameEpisodesGrabSpecification.cs:1-26`.
// The C# class is a thin per-release wrapper around the shared
// SameEpisodesSpecification helper.
//
// Permanent rejection — the existing-file episode coverage is
// structural; doesn't flip without an episode-file-row change which
// produces a fresh Subject.
type SameEpisodesGrabSpecification struct {
	finder SameEpisodesEpisodeFinder
	log    *slog.Logger
}

// NewSameEpisodesGrabSpecification builds the spec. log defaults to
// [slog.Default] when nil. finder MUST be non-nil.
func NewSameEpisodesGrabSpecification(finder SameEpisodesEpisodeFinder, log *slog.Logger) (*SameEpisodesGrabSpecification, error) {
	if finder == nil {
		return nil, fmt.Errorf("decisionengine: SameEpisodesGrabSpecification requires a non-nil SameEpisodesEpisodeFinder")
	}
	if log == nil {
		log = slog.Default()
	}
	return &SameEpisodesGrabSpecification{finder: finder, log: log}, nil
}

// Name returns the canonical spec name.
func (*SameEpisodesGrabSpecification) Name() string { return SameEpisodesGrabSpecificationName }

// Kind reports Permanent.
func (*SameEpisodesGrabSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default.
func (*SameEpisodesGrabSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:18-25`.
func (s *SameEpisodesGrabSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil {
		return Accept()
	}
	ok, err := isSameEpisodesGrab(ctx, s.finder, subject.Episodes)
	if err != nil {
		// Defensive — match SameEpisodesImportSpec posture: a
		// transient DB outage shouldn't bounce every candidate in a
		// batch.
		s.log.Warn("sameEpisodesGrab: lookup failed; accepting defensively",
			"err", err)
		return Accept()
	}
	if !ok {
		s.log.Debug("sameEpisodesGrab: existing file covers more episodes than this release")
		return Reject(ReasonExistingFileHasMoreEpisodes,
			"Episode file on disk contains more episodes than this release contains")
	}
	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*SameEpisodesGrabSpecification)(nil)
