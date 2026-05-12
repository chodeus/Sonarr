package indexersearch

import (
	"context"

	"sonarr.tv/sonarr/internal/decisionengine"
)

// DecisionMaker is the narrow surface [Service.dispatchSearch]
// consumes to convert raw indexer reports into typed
// [decisionengine.Decision] values. Mirrors the C# call site at
// `ReleaseSearchService.cs:549` —
// `_makeDownloadDecision.GetSearchDecision(reports, criteriaBase)`.
//
// Production binds [*decisionengine.DownloadDecisionMaker] via Go's
// structural typing; the method signature matches exactly. Tests
// can inject a stub that returns decisions wrapping the supplied
// reports without exercising the full spec pipeline.
//
// Slice 4.BV (C-cleanup [C13]) introduced the interface; slice 4.BW
// (C-cleanup [C14]) retired the criteriaFromBase projection helper —
// post-merge [CriteriaBase] IS [decisionengine.SearchCriteria] (type
// alias), so dispatchSearch passes `&base` directly.
type DecisionMaker interface {
	GetSearchDecision(ctx context.Context, reports []any, criteria *decisionengine.SearchCriteria) []decisionengine.Decision
}

// passthroughDecisionMaker is the nil-tolerant fallback wired by
// [NewService] when the caller passes nil for the DecisionMaker
// parameter. Each raw report becomes an APPROVED Decision wrapping
// the report verbatim — no spec evaluation runs. Use for
// dispatcher-only test setups; production wiring MUST supply a real
// [*decisionengine.DownloadDecisionMaker].
type passthroughDecisionMaker struct{}

// GetSearchDecision wraps each raw report into a [decisionengine.Decision].
func (passthroughDecisionMaker) GetSearchDecision(_ context.Context, reports []any, _ *decisionengine.SearchCriteria) []decisionengine.Decision {
	out := make([]decisionengine.Decision, 0, len(reports))
	for _, r := range reports {
		out = append(out, decisionengine.NewDecision(r))
	}
	return out
}
