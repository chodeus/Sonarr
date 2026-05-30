package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"sonarr.tv/sonarr/internal/episodes"
)

// EpisodeRequestedSpecificationName is the canonical Spec.Name() identifier.
const EpisodeRequestedSpecificationName = "EpisodeRequested"

// EpisodeRequestedSpecification rejects a search result whose matched
// episodes don't intersect the episodes the search actually asked for —
// indexers routinely return unrelated releases for a query, and without
// this gate those wrong episodes would be grabbed.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/Search/EpisodeRequestedSpecification.cs:1-58`.
//
// # Algorithm (C# `:19-56`)
//
//  1. No SearchCriteria (RSS sync / push) → Accept (the gate is
//     search-only; RSS has no requested-episode set).
//  2. Intersect the searched episode IDs with the release's matched
//     episode IDs. Non-empty intersection → Accept.
//  3. Empty intersection → Reject [ReasonWrongEpisode] with a message
//     naming the release's episode span (or a bare message when the
//     release matched no episodes).
//
// Permanent rejection — the searched set + the parsed episodes are
// stable for a given Subject.
type EpisodeRequestedSpecification struct {
	log *slog.Logger
}

// NewEpisodeRequestedSpecification builds the spec. log defaults to
// [slog.Default] when nil.
func NewEpisodeRequestedSpecification(log *slog.Logger) *EpisodeRequestedSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &EpisodeRequestedSpecification{log: log}
}

// Name returns the canonical spec name.
func (*EpisodeRequestedSpecification) Name() string { return EpisodeRequestedSpecificationName }

// Kind reports Permanent.
func (*EpisodeRequestedSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — in-memory ID-set intersection, no I/O.
func (*EpisodeRequestedSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:19-56`.
func (s *EpisodeRequestedSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	if subject == nil {
		return Accept()
	}
	criteria, ok := info.SearchCriteria.(*SearchCriteria)
	if !ok || criteria == nil {
		return Accept()
	}

	requested := make(map[int]struct{}, len(criteria.Episodes))
	for _, e := range criteria.Episodes {
		requested[e.ID] = struct{}{}
	}
	for _, e := range subject.Episodes {
		if _, hit := requested[e.ID]; hit {
			return Accept()
		}
	}

	// No intersection → reject, naming the release's episode span (C#
	// `:31-53`).
	if len(subject.Episodes) == 0 {
		s.log.Debug("episodeRequested: release matched no episodes; rejecting")
		return Reject(ReasonWrongEpisode, "Episode wasn't requested")
	}

	sorted := append([]episodes.Episode(nil), subject.Episodes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].SeasonNumber != sorted[j].SeasonNumber {
			return sorted[i].SeasonNumber < sorted[j].SeasonNumber
		}
		return sorted[i].EpisodeNumber < sorted[j].EpisodeNumber
	})
	first := sorted[0]
	last := sorted[len(sorted)-1]

	var msg string
	if len(sorted) > 1 {
		msg = fmt.Sprintf("Episode wasn't requested: %dx%d-%d", first.SeasonNumber, first.EpisodeNumber, last.EpisodeNumber)
	} else {
		msg = fmt.Sprintf("Episode wasn't requested: %dx%d", first.SeasonNumber, first.EpisodeNumber)
	}
	s.log.Debug("episodeRequested: release episodes not in the search request; rejecting", "msg", msg)
	return Reject(ReasonWrongEpisode, msg)
}

// Compile-time interface satisfaction.
var _ Specification = (*EpisodeRequestedSpecification)(nil)
