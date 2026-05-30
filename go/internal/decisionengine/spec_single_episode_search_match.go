package decisionengine

import (
	"context"
	"log/slog"
)

// SingleEpisodeSearchMatchSpecificationName is the canonical Spec.Name() identifier.
const SingleEpisodeSearchMatchSpecificationName = "SingleEpisodeSearchMatch"

// SingleEpisodeSearchMatchSpecification rejects a single-episode-search
// result that doesn't match the searched episode: a wrong season, a
// full-season pack (the user asked for one episode, not the whole
// season), or a different episode. The anime branch rejects a
// full-season pack unless the search was itself a season search.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/Search/SingleEpisodeSearchMatchSpecification.cs:1-82`.
// The C# constructor takes ISceneMappingService but IsSatisfiedBy never
// references it — logger-only (same posture as SceneMappingSpecification,
// J488).
//
// # Algorithm (C# `:25-81`)
//
//  1. No SearchCriteria → Accept.
//  2. SingleEpisodeSearchCriteria (Kind == SearchKindSingleEpisode):
//     - searched season != parsed season → Reject [ReasonWrongSeason].
//     - parsed EpisodeNumbers empty → Reject [ReasonFullSeason] ("Full
//     season pack"): a season pack landed during a single-ep search.
//     - parsed EpisodeNumbers missing the searched episode → Reject
//     [ReasonWrongEpisode].
//     - otherwise → Accept.
//  3. AnimeEpisodeSearchCriteria (Kind == SearchKindAnimeEpisode):
//     - parsed FullSeason AND the search was not a season search →
//     Reject [ReasonFullSeason].
//     - otherwise → Accept.
//  4. Any other criteria kind → Accept.
//
// Permanent rejection — the searched (season, episode) + the parsed
// numbers are stable for a given Subject.
type SingleEpisodeSearchMatchSpecification struct {
	log *slog.Logger
}

// NewSingleEpisodeSearchMatchSpecification builds the spec. log defaults
// to [slog.Default] when nil.
func NewSingleEpisodeSearchMatchSpecification(log *slog.Logger) *SingleEpisodeSearchMatchSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &SingleEpisodeSearchMatchSpecification{log: log}
}

// Name returns the canonical spec name.
func (*SingleEpisodeSearchMatchSpecification) Name() string {
	return SingleEpisodeSearchMatchSpecificationName
}

// Kind reports Permanent.
func (*SingleEpisodeSearchMatchSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — in-memory comparison, no I/O.
func (*SingleEpisodeSearchMatchSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:25-81`.
func (s *SingleEpisodeSearchMatchSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	criteria, ok := info.SearchCriteria.(*SearchCriteria)
	if !ok || criteria == nil {
		return Accept()
	}
	if subject == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	parsed := subject.ParsedEpisodeInfo

	switch criteria.Kind {
	case SearchKindSingleEpisode:
		if criteria.SeasonNumber != parsed.SeasonNumber {
			s.log.Debug("singleEpisodeSearchMatch: season number does not match searched season number, skipping",
				"searched", criteria.SeasonNumber, "parsed", parsed.SeasonNumber)
			return Reject(ReasonWrongSeason, "Wrong season")
		}
		if len(parsed.EpisodeNumbers) == 0 {
			s.log.Debug("singleEpisodeSearchMatch: full season result during single episode search, skipping")
			return Reject(ReasonFullSeason, "Full season pack")
		}
		if !containsEpisodeNumber(parsed.EpisodeNumbers, criteria.EpisodeNumber) {
			s.log.Debug("singleEpisodeSearchMatch: episode number does not match searched episode number, skipping",
				"searched", criteria.EpisodeNumber, "parsed", parsed.EpisodeNumbers)
			return Reject(ReasonWrongEpisode, "Wrong episode")
		}
		return Accept()

	case SearchKindAnimeEpisode:
		if parsed.FullSeason && !criteria.AnimeIsSeasonSearch {
			s.log.Debug("singleEpisodeSearchMatch: full season result during single episode search, skipping")
			return Reject(ReasonFullSeason, "Full season pack")
		}
		return Accept()

	default:
		return Accept()
	}
}

func containsEpisodeNumber(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// Compile-time interface satisfaction.
var _ Specification = (*SingleEpisodeSearchMatchSpecification)(nil)
