package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
)

func newPendingSpec(t *testing.T, pending decisionengine.PendingReleaseAgeReader) *decisionengine.PendingSpecification {
	t.Helper()
	spec, err := decisionengine.NewPendingSpecification(pending, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewPendingSpecification: %v", err)
	}
	return spec
}

// pushedSubject builds a subject for a PUSHED release (one episode, no
// file) so the pending guard applies.
func pushedSubject() *decisionengine.Subject {
	sub := delaySubject(5, q720(), 0)
	sub.ReleaseSource = decisionengine.ReleaseSourceReleasePush
	return sub
}

func TestPending_KindIsTemporary(t *testing.T) {
	t.Parallel()
	spec := newPendingSpec(t, stubDelayPending{found: false})
	if spec.Kind() != decisionengine.RejectionTemporary {
		t.Errorf("Kind = %v, want Temporary", spec.Kind())
	}
}

func TestPending_PushedWithMatch_RejectsTemporary(t *testing.T) {
	t.Parallel()
	spec := newPendingSpec(t, stubDelayPending{found: true})
	got := spec.IsSatisfiedBy(t.Context(), pushedSubject(), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonMinimumAgeDelayPushed {
		t.Fatalf("pushed release with a pending match should reject MinimumAgeDelayPushed; got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
}

func TestPending_PushedNoMatch_Accept(t *testing.T) {
	t.Parallel()
	spec := newPendingSpec(t, stubDelayPending{found: false})
	if !spec.IsSatisfiedBy(t.Context(), pushedSubject(), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("pushed release with no pending match should Accept")
	}
}

func TestPending_RSSSource_Accept(t *testing.T) {
	t.Parallel()
	// Even with a pending match, RSS sync bypasses the pending guard.
	spec := newPendingSpec(t, stubDelayPending{found: true})
	sub := pushedSubject()
	sub.ReleaseSource = decisionengine.ReleaseSourceRSS
	if !spec.IsSatisfiedBy(t.Context(), sub, decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("RSS source should bypass the pending guard")
	}
}

func TestPending_UserInvokedSearch_Accept(t *testing.T) {
	t.Parallel()
	spec := newPendingSpec(t, stubDelayPending{found: true})
	info := decisionengine.ReleaseDecisionInformation{SearchCriteria: &decisionengine.SearchCriteria{UserInvokedSearch: true}}
	if !spec.IsSatisfiedBy(t.Context(), pushedSubject(), info).Accepted {
		t.Error("user-invoked search should bypass the pending guard")
	}
}

func TestPending_NilReader_Accept(t *testing.T) {
	t.Parallel()
	// Optional dep: a nil reader makes the spec inert.
	spec := newPendingSpec(t, nil)
	if !spec.IsSatisfiedBy(t.Context(), pushedSubject(), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("nil pending reader should make the spec inert (Accept)")
	}
}
