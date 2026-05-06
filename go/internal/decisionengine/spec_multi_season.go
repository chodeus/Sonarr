package decisionengine

import (
	"context"
	"log/slog"
)

// MultiSeasonSpecificationName is the canonical Spec.Name() identifier.
const MultiSeasonSpecificationName = "MultiSeason"

// MultiSeasonSpecification rejects releases parsed as multi-season
// packs (the C# `IsMultiSeason` flag). Permanent rejection — Sonarr
// does not import multi-season packs as single units; the user has
// to grab per-season releases instead.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/MultiSeasonSpecification.cs:1-29`.
type MultiSeasonSpecification struct {
	log *slog.Logger
}

// NewMultiSeasonSpecification builds the spec. log defaults to slog.Default
// when nil.
func NewMultiSeasonSpecification(log *slog.Logger) *MultiSeasonSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &MultiSeasonSpecification{log: log}
}

// Name returns the canonical spec name.
func (*MultiSeasonSpecification) Name() string { return MultiSeasonSpecificationName }

// Kind reports Permanent.
func (*MultiSeasonSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default.
func (*MultiSeasonSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:18-27`. Rejects when
// `ParsedEpisodeInfo.IsMultiSeason` is true; accepts otherwise.
func (s *MultiSeasonSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	if subject.ParsedEpisodeInfo.IsMultiSeason {
		s.log.Debug("multiSeason: rejecting multi-season pack")
		return Reject(ReasonMultiSeason, "Multi-season releases are not supported")
	}
	return Accept()
}
