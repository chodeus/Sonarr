package decisionengine

import "context"

// Specification is the IDownloadDecisionEngineSpecification port at
// `src/NzbDrone.Core/DecisionEngine/Specifications/IDownloadDecisionEngineSpecification.cs:1-12`.
//
// One Specification implementation per rejection-class. The
// orchestrator (DownloadDecisionMaker, future slice) sorts the spec
// list by Priority(), then calls IsSatisfiedBy on each spec for each
// candidate Subject; collected rejections become the Decision's
// Rejections list.
//
// Convention:
//
//   - IsSatisfiedBy returns Accept() when the spec passes; Reject()
//     when it fails. Specs DO NOT swallow context.Canceled — the
//     orchestrator wraps the per-spec call in a per-Subject context
//     and routes cancellation back into the Decision's Rejections.
//   - Kind() is a classifier for the Rejection the orchestrator
//     promotes from a Reject result. The C# tree uses a per-spec
//     `RejectionType` property; the Go port preserves the shape so
//     a Permanent spec can't accidentally produce Temporary
//     rejections (or vice versa).
//   - Priority() gates spec ordering — cheap (Default = 0) specs
//     run before expensive (Disk = 1) so a Permanent rejection
//     short-circuits the disk reads.
//   - Name() is used in log lines + the per-spec rejection-attribution
//     a future telemetry layer reads. Mirrors the C# `GetType().Name`
//     reflection pattern; specs return their canonical name as a
//     constant.
type Specification interface {
	// Name returns the canonical spec identifier — used for telemetry,
	// logging, and the IRejectWithReason rejection metadata. Convention:
	// the C# class name without the `Specification` suffix
	// (e.g. "AcceptableSize", "MinimumAge", "Blocklist").
	Name() string

	// Kind reports whether this spec produces Permanent or Temporary
	// rejections. Constant per-spec; doesn't depend on the candidate.
	Kind() RejectionKind

	// Priority gates spec ordering in the orchestrator. Cheap
	// specs (Default) run first, expensive (Disk) last.
	Priority() SpecPriority

	// IsSatisfiedBy is the per-Subject evaluation. Returns
	// Accept() when the candidate passes the spec; Reject(reason, msg)
	// when it doesn't. Implementations MUST be goroutine-safe — the
	// orchestrator may evaluate multiple Subjects concurrently against
	// the same Specification instance.
	IsSatisfiedBy(ctx context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision
}

// ReleaseDecisionInformation is the cross-spec context passed to every
// IsSatisfiedBy invocation. Mirrors
// `src/NzbDrone.Core/DecisionEngine/ReleaseDecisionInformation.cs:1-19`.
//
// Two fields:
//
//   - PushedRelease — true when the candidate came from
//     POST /api/v3/release/push (the user manually pushed a release
//     to the queue). Several specs relax their gate when this is true
//     (DelayProfile, MinimumAge) on the theory that user intent
//     already cleared those checks.
//   - SearchCriteria — when the candidate came from a search (RSS or
//     interactive), the criteria that produced it. Specs use this to
//     gate "is this episode/series the one we asked for?" tests
//     (WrongEpisode / WrongSeries / etc.). Nil for non-search paths.
//     Typed as `any` because SearchCriteriaBase isn't ported yet
//     (its own slice once IndexerSearch is on the Phase 4 critical
//     path); IsSatisfiedBy implementations type-switch when they
//     need it.
//
// The struct is value-typed; the orchestrator constructs one per
// per-Subject batch and passes it by value into every IsSatisfiedBy
// call. Mutation between specs is not supported.
type ReleaseDecisionInformation struct {
	// PushedRelease is true when the candidate came from a manual
	// release push. Specs that want to relax their gate read this.
	PushedRelease bool
	// SearchCriteria carries the search context (when relevant). Typed
	// as `any` until SearchCriteriaBase is ported in its own slice.
	SearchCriteria any
}

// NewReleaseDecisionInformation mirrors the C# default constructor at
// `:9-13` — both fields zero/nil. Used by the orchestrator's RSS-sweep
// path where there's no search criteria to attach.
func NewReleaseDecisionInformation() ReleaseDecisionInformation {
	return ReleaseDecisionInformation{}
}

// NewPushedRelease is the convenience constructor for the
// release-push entry path: the candidate came from a manual user push.
// The C# tree uses the multi-arg constructor at `:15-19`; the Go port
// splits into named factories per call-site.
func NewPushedRelease() ReleaseDecisionInformation {
	return ReleaseDecisionInformation{PushedRelease: true}
}

// NewSearchInformation tags the search criteria. Use when the
// candidate came from an automatic or interactive search.
func NewSearchInformation(criteria any) ReleaseDecisionInformation {
	return ReleaseDecisionInformation{SearchCriteria: criteria}
}
