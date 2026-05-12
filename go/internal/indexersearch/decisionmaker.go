package indexersearch

import (
	"context"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
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
// Slice 4.BV (C-cleanup [C13]).
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

// criteriaFromBase projects a CriteriaBase into the
// [decisionengine.SearchCriteria] shape the DecisionMaker consumes.
// Mirrors the C# pattern where the same `SearchCriteriaBase` value
// is passed straight through to `GetSearchDecision`. The Go port has
// a separate decisionengine.SearchCriteria type (with a narrower set
// of fields — the C14 sub-arc will unify them); for now the
// projection picks the four fields the spec engine actually reads:
// Series, Episodes, UserInvokedSearch, InteractiveSearch.
func criteriaFromBase(base CriteriaBase) *decisionengine.SearchCriteria {
	if base.Series == nil {
		return nil
	}
	return &decisionengine.SearchCriteria{
		Series:            base.Series,
		Episodes:          append([]episodes.Episode(nil), base.Episodes...),
		UserInvokedSearch: base.UserInvokedSearch,
		InteractiveSearch: base.InteractiveSearch,
	}
}
