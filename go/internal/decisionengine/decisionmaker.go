package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
)

// DecisionMaker is the per-Subject orchestrator that walks the
// registered Specifications by priority tier and collects the
// resulting Decisions.
//
// Mirrors the C# `DownloadDecisionMaker` at
// `src/NzbDrone.Core/DecisionEngine/DownloadDecisionMaker.cs:1-242`,
// minus the parsing / mapping / aggregation front-end. Those depend
// on subsystems (ParsingService, AggregationService,
// CustomFormatCalculationService, SceneMappingService) that haven't
// landed yet; once they do, the orchestrator gains a higher-level
// `GetRssDecision(reports, ...)` / `GetSearchDecision(reports, ...)`
// surface that funnels through `Decide`.
//
// For now the orchestrator's job is the spec-evaluation core:
//
//   - Group specs by Priority (Default = 0, Database = 0, Disk = 1)
//     in ascending priority order. Cheaper tiers run first.
//   - Within a single priority tier, evaluate every spec against the
//     candidate; collect every rejection.
//   - **Tier short-circuit**: if any spec in the current tier
//     rejected, stop — don't evaluate the higher-priority tiers.
//     The C# version does this so an expensive Disk-tier spec
//     doesn't fire on a candidate that's already permanently rejected
//     at the cheap tier.
//   - Panic recovery per spec: a panicking spec contributes a
//     `ReasonDecisionError` rejection and the next spec runs. This
//     mirrors the C# `try/catch` block at `:201-216`.
type DecisionMaker struct {
	// specsByPriority is the spec list pre-grouped by Priority,
	// sorted ascending. Built once in NewDecisionMaker so per-Decide
	// calls don't pay the grouping cost.
	specsByPriority [][]Specification
	log             *slog.Logger
}

// NewDecisionMaker builds a DecisionMaker. The supplied spec list is
// snapshot-copied; subsequent mutations to the input slice don't
// affect the orchestrator. log defaults to slog.Default when nil.
//
// Specs sharing a Priority retain their input-slice order within the
// tier — the C# tree relies on this for telemetry / debug
// readability.
func NewDecisionMaker(specs []Specification, log *slog.Logger) *DecisionMaker {
	if log == nil {
		log = slog.Default()
	}

	type priorityIndexed struct {
		priority SpecPriority
		spec     Specification
		ord      int
	}
	indexed := make([]priorityIndexed, 0, len(specs))
	for i, s := range specs {
		if s == nil {
			continue
		}
		indexed = append(indexed, priorityIndexed{priority: s.Priority(), spec: s, ord: i})
	}
	// Group by priority: stable-sort ascending by priority, preserving
	// input order within each priority tier.
	sort.SliceStable(indexed, func(i, j int) bool {
		return indexed[i].priority < indexed[j].priority
	})

	var groups [][]Specification
	for _, e := range indexed {
		if len(groups) == 0 || groups[len(groups)-1][0].Priority() != e.priority {
			groups = append(groups, []Specification{e.spec})
		} else {
			groups[len(groups)-1] = append(groups[len(groups)-1], e.spec)
		}
	}

	return &DecisionMaker{specsByPriority: groups, log: log}
}

// Decide evaluates every registered specification against the
// supplied subject under the supplied information context, and
// returns a Decision summarising the outcome.
//
// Per-tier short-circuit: if any spec at priority N rejects, specs at
// priorities >N are NOT evaluated. The returned Decision's
// Rejections list contains every rejection from the first
// rejection-emitting tier (in input-slice order within the tier).
//
// An approved Decision (no rejections) means every spec at every
// priority tier passed.
//
// A nil-typed Subject is tolerated: every spec defensive-accepts on
// nil; the orchestrator surfaces an approved Decision.
//
// The context is forwarded verbatim to each spec's IsSatisfiedBy.
// Specs that need to bail on context cancellation must respect the
// passed ctx themselves.
func (dm *DecisionMaker) Decide(ctx context.Context, subject *Subject, info ReleaseDecisionInformation) Decision {
	for _, group := range dm.specsByPriority {
		var rejections []Rejection
		for _, spec := range group {
			if r, ok := dm.evaluateSpec(ctx, spec, subject, info); ok {
				rejections = append(rejections, r)
			}
		}
		if len(rejections) > 0 {
			return Decision{Subject: subject, Rejections: rejections}
		}
	}
	return Decision{Subject: subject}
}

// DecideMany batch-evaluates a slice of subjects under one
// information context. Returns one Decision per subject in input
// order. Equivalent to calling Decide N times; the helper exists for
// caller ergonomics.
func (dm *DecisionMaker) DecideMany(ctx context.Context, subjects []*Subject, info ReleaseDecisionInformation) []Decision {
	out := make([]Decision, 0, len(subjects))
	for _, s := range subjects {
		out = append(out, dm.Decide(ctx, s, info))
	}
	return out
}

// evaluateSpec calls the spec's IsSatisfiedBy with panic recovery.
// Returns (rejection, true) if the spec rejected (or panicked); the
// (zeroValue, false) branch indicates an accept.
//
// Mirrors the C# `EvaluateSpec` at
// `src/NzbDrone.Core/DecisionEngine/DownloadDecisionMaker.cs:199-219`.
// The C# version logs the parsed/report data on the exception via
// `e.Data.Add`; the Go port emits the title via slog instead.
func (dm *DecisionMaker) evaluateSpec(ctx context.Context, spec Specification, subject *Subject, info ReleaseDecisionInformation) (rej Rejection, gotRejection bool) {
	defer func() {
		if r := recover(); r != nil {
			title := "<unknown>"
			if rel := releaseFromSubject(subject); rel != nil {
				title = rel.Title
			}
			dm.log.Error("decisionMaker: spec panicked",
				"spec", spec.Name(), "title", title, "panic", r)
			rej = Rejection{
				Reason:  ReasonDecisionError,
				Message: fmt.Sprintf("%s: %v", spec.Name(), r),
				Kind:    RejectionPermanent,
			}
			gotRejection = true
		}
	}()

	result := spec.IsSatisfiedBy(ctx, subject, info)
	if result.Accepted {
		return Rejection{}, false
	}
	return Rejection{
		Reason:  result.Reason,
		Message: result.Message,
		Kind:    spec.Kind(),
	}, true
}

// ReleaseSourceFor mirrors the C# `GetReleaseSource` at
// `src/NzbDrone.Core/DecisionEngine/DownloadDecisionMaker.cs:221-240`.
// Derives a [ReleaseSourceType] tag from the candidate's entry-path:
//
//   - criteria == nil + pushed=true → ReleaseSourceReleasePush
//   - criteria == nil + pushed=false → ReleaseSourceRSS
//   - criteria != nil + InteractiveSearch → ReleaseSourceInteractiveSearch
//   - criteria != nil + UserInvokedSearch → ReleaseSourceUserInvokedSearch
//   - criteria != nil + neither → ReleaseSourceSearch (automatic)
func ReleaseSourceFor(pushed bool, criteria *SearchCriteria) ReleaseSourceType {
	if criteria == nil {
		if pushed {
			return ReleaseSourceReleasePush
		}
		return ReleaseSourceRSS
	}
	if criteria.InteractiveSearch {
		return ReleaseSourceInteractiveSearch
	}
	if criteria.UserInvokedSearch {
		return ReleaseSourceUserInvokedSearch
	}
	return ReleaseSourceSearch
}
