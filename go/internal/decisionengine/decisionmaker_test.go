package decisionengine_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
)

// dmFakeSpec is a configurable test double that returns a
// pre-configured SpecDecision and reports a configurable Priority +
// Kind. Tracks call count for ordering / short-circuit assertions.
type dmFakeSpec struct {
	name     string
	priority decisionengine.SpecPriority
	kind     decisionengine.RejectionKind
	decision decisionengine.SpecDecision
	calls    int
	panic    any // when non-nil, IsSatisfiedBy panics with this value
}

func (f *dmFakeSpec) Name() string                          { return f.name }
func (f *dmFakeSpec) Kind() decisionengine.RejectionKind    { return f.kind }
func (f *dmFakeSpec) Priority() decisionengine.SpecPriority { return f.priority }
func (f *dmFakeSpec) IsSatisfiedBy(_ context.Context, _ *decisionengine.Subject, _ decisionengine.ReleaseDecisionInformation) decisionengine.SpecDecision {
	f.calls++
	if f.panic != nil {
		panic(f.panic)
	}
	return f.decision
}

// accepting builds a passing dmFakeSpec.
func accepting(name string, prio decisionengine.SpecPriority) *dmFakeSpec {
	return &dmFakeSpec{
		name: name, priority: prio, kind: decisionengine.RejectionPermanent,
		decision: decisionengine.Accept(),
	}
}

// rejecting builds a dmFakeSpec that rejects with the given reason +
// message + kind.
func rejecting(name string, prio decisionengine.SpecPriority, kind decisionengine.RejectionKind, reason decisionengine.RejectionReason, msg string) *dmFakeSpec {
	return &dmFakeSpec{
		name: name, priority: prio, kind: kind,
		decision: decisionengine.Reject(reason, msg),
	}
}

// makeMakerSubject builds a minimal Subject for orchestrator tests.
func makeMakerSubject() *decisionengine.Subject {
	return &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.S01E01"},
	}
}

func TestDecisionMaker_NilSpecsApprovesAll(t *testing.T) {
	t.Parallel()
	dm := decisionengine.NewDecisionMaker(nil, slog.New(slog.DiscardHandler))
	got := dm.Decide(t.Context(), makeMakerSubject(), decisionengine.ReleaseDecisionInformation{})
	if !got.Approved() {
		t.Errorf("expected approved with no specs, got %+v", got.Rejections)
	}
}

func TestDecisionMaker_AllAcceptApproves(t *testing.T) {
	t.Parallel()
	specs := []decisionengine.Specification{
		accepting("A", decisionengine.SpecPriorityDefault),
		accepting("B", decisionengine.SpecPriorityDefault),
		accepting("C", decisionengine.SpecPriorityDisk),
	}
	dm := decisionengine.NewDecisionMaker(specs, slog.New(slog.DiscardHandler))
	got := dm.Decide(t.Context(), makeMakerSubject(), decisionengine.ReleaseDecisionInformation{})
	if !got.Approved() {
		t.Errorf("expected approved when every spec accepts, got %+v", got.Rejections)
	}
}

func TestDecisionMaker_SingleRejectSurfaces(t *testing.T) {
	t.Parallel()
	specs := []decisionengine.Specification{
		accepting("A", decisionengine.SpecPriorityDefault),
		rejecting("B", decisionengine.SpecPriorityDefault, decisionengine.RejectionPermanent, decisionengine.ReasonBlocklisted, "bad"),
	}
	dm := decisionengine.NewDecisionMaker(specs, slog.New(slog.DiscardHandler))
	got := dm.Decide(t.Context(), makeMakerSubject(), decisionengine.ReleaseDecisionInformation{})
	if got.Approved() {
		t.Fatalf("expected reject")
	}
	if len(got.Rejections) != 1 {
		t.Fatalf("expected 1 rejection, got %d", len(got.Rejections))
	}
	if got.Rejections[0].Reason != decisionengine.ReasonBlocklisted {
		t.Errorf("Reason = %v", got.Rejections[0].Reason)
	}
	if got.Rejections[0].Kind != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", got.Rejections[0].Kind)
	}
}

func TestDecisionMaker_SameTierAllSpecsEvaluated(t *testing.T) {
	t.Parallel()
	// Within one priority tier, every spec runs even if an earlier one
	// rejected — every rejection is collected.
	a := rejecting("A", decisionengine.SpecPriorityDefault, decisionengine.RejectionPermanent, decisionengine.ReasonBlocklisted, "a")
	b := accepting("B", decisionengine.SpecPriorityDefault)
	c := rejecting("C", decisionengine.SpecPriorityDefault, decisionengine.RejectionPermanent, decisionengine.ReasonRaw, "c")
	specs := []decisionengine.Specification{a, b, c}
	dm := decisionengine.NewDecisionMaker(specs, slog.New(slog.DiscardHandler))
	got := dm.Decide(t.Context(), makeMakerSubject(), decisionengine.ReleaseDecisionInformation{})
	if got.Approved() {
		t.Fatalf("expected reject")
	}
	if len(got.Rejections) != 2 {
		t.Errorf("expected 2 rejections (A + C), got %d: %+v", len(got.Rejections), got.Rejections)
	}
	if a.calls != 1 || b.calls != 1 || c.calls != 1 {
		t.Errorf("expected each spec called once: A=%d B=%d C=%d", a.calls, b.calls, c.calls)
	}
	// Order preserved: input-slice order within tier.
	if got.Rejections[0].Reason != decisionengine.ReasonBlocklisted {
		t.Errorf("expected first rejection to be A's, got %v", got.Rejections[0].Reason)
	}
	if got.Rejections[1].Reason != decisionengine.ReasonRaw {
		t.Errorf("expected second rejection to be C's, got %v", got.Rejections[1].Reason)
	}
}

func TestDecisionMaker_HigherTierShortCircuited(t *testing.T) {
	t.Parallel()
	// Default-tier reject prevents the Disk-tier spec from running.
	defaultReject := rejecting("Default", decisionengine.SpecPriorityDefault, decisionengine.RejectionPermanent, decisionengine.ReasonBlocklisted, "x")
	diskSpec := accepting("Disk", decisionengine.SpecPriorityDisk)
	dm := decisionengine.NewDecisionMaker([]decisionengine.Specification{diskSpec, defaultReject}, slog.New(slog.DiscardHandler))
	got := dm.Decide(t.Context(), makeMakerSubject(), decisionengine.ReleaseDecisionInformation{})
	if got.Approved() {
		t.Fatalf("expected reject")
	}
	if defaultReject.calls != 1 {
		t.Errorf("expected default reject to fire once, got %d", defaultReject.calls)
	}
	if diskSpec.calls != 0 {
		t.Errorf("expected Disk-tier spec to be skipped, got %d calls", diskSpec.calls)
	}
}

func TestDecisionMaker_LowerTierAllowsHigherTierToRun(t *testing.T) {
	t.Parallel()
	// Default-tier all-accept; Disk-tier rejects → final decision is the
	// Disk rejection.
	defAccept := accepting("Default", decisionengine.SpecPriorityDefault)
	diskReject := rejecting("Disk", decisionengine.SpecPriorityDisk, decisionengine.RejectionPermanent, decisionengine.ReasonRaw, "d")
	dm := decisionengine.NewDecisionMaker([]decisionengine.Specification{diskReject, defAccept}, slog.New(slog.DiscardHandler))
	got := dm.Decide(t.Context(), makeMakerSubject(), decisionengine.ReleaseDecisionInformation{})
	if got.Approved() {
		t.Fatalf("expected reject from Disk tier")
	}
	if defAccept.calls != 1 || diskReject.calls != 1 {
		t.Errorf("expected each spec called once: def=%d disk=%d", defAccept.calls, diskReject.calls)
	}
	if got.Rejections[0].Reason != decisionengine.ReasonRaw {
		t.Errorf("Reason = %v, want ReasonRaw", got.Rejections[0].Reason)
	}
}

func TestDecisionMaker_TemporaryKindPreserved(t *testing.T) {
	t.Parallel()
	tmp := rejecting("Tmp", decisionengine.SpecPriorityDefault, decisionengine.RejectionTemporary, decisionengine.ReasonMinimumAge, "wait")
	dm := decisionengine.NewDecisionMaker([]decisionengine.Specification{tmp}, slog.New(slog.DiscardHandler))
	got := dm.Decide(t.Context(), makeMakerSubject(), decisionengine.ReleaseDecisionInformation{})
	if got.Approved() {
		t.Fatalf("expected reject")
	}
	if !got.TemporarilyRejected() {
		t.Errorf("expected TemporarilyRejected, got %+v", got)
	}
	if got.Rejected() {
		t.Errorf("Rejected() should be false for temporary-only: %+v", got)
	}
	if got.Rejections[0].Kind != decisionengine.RejectionTemporary {
		t.Errorf("Kind = %v, want Temporary", got.Rejections[0].Kind)
	}
}

func TestDecisionMaker_PanicRecoveryEmitsDecisionError(t *testing.T) {
	t.Parallel()
	panicker := &dmFakeSpec{
		name: "Boom", priority: decisionengine.SpecPriorityDefault,
		kind: decisionengine.RejectionPermanent, panic: "kaboom",
	}
	survivor := accepting("Survivor", decisionengine.SpecPriorityDefault)
	dm := decisionengine.NewDecisionMaker([]decisionengine.Specification{panicker, survivor}, slog.New(slog.DiscardHandler))
	got := dm.Decide(t.Context(), makeMakerSubject(), decisionengine.ReleaseDecisionInformation{})
	if got.Approved() {
		t.Fatalf("expected reject from panicking spec")
	}
	if got.Rejections[0].Reason != decisionengine.ReasonDecisionError {
		t.Errorf("Reason = %v, want ReasonDecisionError", got.Rejections[0].Reason)
	}
	// Survivor still ran in the same tier.
	if survivor.calls != 1 {
		t.Errorf("expected Survivor to run after panicker, got calls=%d", survivor.calls)
	}
}

func TestDecisionMaker_NilSpecInListSkipped(t *testing.T) {
	t.Parallel()
	specs := []decisionengine.Specification{
		nil,
		accepting("A", decisionengine.SpecPriorityDefault),
		nil,
	}
	dm := decisionengine.NewDecisionMaker(specs, slog.New(slog.DiscardHandler))
	got := dm.Decide(t.Context(), makeMakerSubject(), decisionengine.ReleaseDecisionInformation{})
	if !got.Approved() {
		t.Errorf("expected approved (nil specs ignored), got %+v", got.Rejections)
	}
}

func TestDecisionMaker_DecideMany(t *testing.T) {
	t.Parallel()
	rejectAt2nd := &dmFakeSpec{
		name: "Conditional", priority: decisionengine.SpecPriorityDefault,
		kind: decisionengine.RejectionPermanent,
	}
	// We can't easily make a conditional dmFakeSpec, but DecideMany doesn't
	// need conditional logic — just verify N inputs → N outputs in order.
	rejectAt2nd.decision = decisionengine.Reject(decisionengine.ReasonBlocklisted, "always")
	dm := decisionengine.NewDecisionMaker([]decisionengine.Specification{rejectAt2nd}, slog.New(slog.DiscardHandler))
	subjects := []*decisionengine.Subject{
		makeMakerSubject(), makeMakerSubject(), makeMakerSubject(),
	}
	got := dm.DecideMany(t.Context(), subjects, decisionengine.ReleaseDecisionInformation{})
	if len(got) != 3 {
		t.Fatalf("expected 3 decisions, got %d", len(got))
	}
	for i, d := range got {
		if d.Approved() {
			t.Errorf("decision[%d] should be rejected", i)
		}
	}
	if rejectAt2nd.calls != 3 {
		t.Errorf("expected spec called 3 times, got %d", rejectAt2nd.calls)
	}
}

// === ReleaseSourceFor ===

func TestReleaseSourceFor_RSS(t *testing.T) {
	t.Parallel()
	if got := decisionengine.ReleaseSourceFor(false, nil); got != decisionengine.ReleaseSourceRSS {
		t.Errorf("got %v, want RSS", got)
	}
}

func TestReleaseSourceFor_ReleasePush(t *testing.T) {
	t.Parallel()
	if got := decisionengine.ReleaseSourceFor(true, nil); got != decisionengine.ReleaseSourceReleasePush {
		t.Errorf("got %v, want ReleasePush", got)
	}
}

func TestReleaseSourceFor_AutomaticSearch(t *testing.T) {
	t.Parallel()
	got := decisionengine.ReleaseSourceFor(false, &decisionengine.SearchCriteria{})
	if got != decisionengine.ReleaseSourceSearch {
		t.Errorf("got %v, want Search", got)
	}
}

func TestReleaseSourceFor_UserInvoked(t *testing.T) {
	t.Parallel()
	got := decisionengine.ReleaseSourceFor(false, &decisionengine.SearchCriteria{UserInvokedSearch: true})
	if got != decisionengine.ReleaseSourceUserInvokedSearch {
		t.Errorf("got %v, want UserInvokedSearch", got)
	}
}

func TestReleaseSourceFor_Interactive(t *testing.T) {
	t.Parallel()
	got := decisionengine.ReleaseSourceFor(false, &decisionengine.SearchCriteria{InteractiveSearch: true})
	if got != decisionengine.ReleaseSourceInteractiveSearch {
		t.Errorf("got %v, want InteractiveSearch", got)
	}
}

func TestReleaseSourceFor_InteractiveBeatsUserInvoked(t *testing.T) {
	t.Parallel()
	// When both flags are set, Interactive wins (mirrors C# if/else
	// chain at GetReleaseSource).
	got := decisionengine.ReleaseSourceFor(false, &decisionengine.SearchCriteria{
		InteractiveSearch: true, UserInvokedSearch: true,
	})
	if got != decisionengine.ReleaseSourceInteractiveSearch {
		t.Errorf("got %v, want InteractiveSearch", got)
	}
}
