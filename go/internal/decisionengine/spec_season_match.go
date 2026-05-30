package decisionengine

import (
	"context"
	"log/slog"
)

// SeasonMatchSpecificationName is the canonical Spec.Name() identifier.
const SeasonMatchSpecificationName = "SeasonMatch"

// SeasonMatchSpecification rejects a season-search result whose parsed
// season doesn't match the searched season — indexers routinely return
// the wrong season for a query, and without this gate that wrong-season
// pack would be grabbed during a "search this season" run.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/Search/SeasonMatchSpecification.cs:1-49`.
// Note the C# constructor takes ISceneMappingService but IsSatisfiedBy
// never references it — this spec is logger-only (same posture as
// SceneMappingSpecification, J488).
//
// # Algorithm (C# `:25-46`)
//
//  1. No SearchCriteria (RSS sync / push) → Accept.
//  2. Criteria is not a SeasonSearchCriteria (Kind != SearchKindSeason)
//     → Accept (the gate is season-search-only — NOT anime-season,
//     which is a separate C# subtype).
//  3. Searched season != parsed season → Reject [ReasonWrongSeason].
//  4. Otherwise → Accept.
//
// Permanent rejection — the searched season + the parsed season are
// stable for a given Subject.
type SeasonMatchSpecification struct {
	log *slog.Logger
}

// NewSeasonMatchSpecification builds the spec. log defaults to
// [slog.Default] when nil.
func NewSeasonMatchSpecification(log *slog.Logger) *SeasonMatchSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &SeasonMatchSpecification{log: log}
}

// Name returns the canonical spec name.
func (*SeasonMatchSpecification) Name() string { return SeasonMatchSpecificationName }

// Kind reports Permanent.
func (*SeasonMatchSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — in-memory comparison, no I/O.
func (*SeasonMatchSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:25-46`.
func (s *SeasonMatchSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	criteria, ok := info.SearchCriteria.(*SearchCriteria)
	if !ok || criteria == nil {
		return Accept()
	}
	if criteria.Kind != SearchKindSeason {
		return Accept()
	}
	if subject == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	if criteria.SeasonNumber != subject.ParsedEpisodeInfo.SeasonNumber {
		s.log.Debug("seasonMatch: season number does not match searched season number, skipping",
			"searched", criteria.SeasonNumber, "parsed", subject.ParsedEpisodeInfo.SeasonNumber)
		return Reject(ReasonWrongSeason, "Wrong season")
	}
	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*SeasonMatchSpecification)(nil)
