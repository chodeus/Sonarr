package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/scenemappings"
)

func newSceneMappingSpec(t *testing.T) *decisionengine.SceneMappingSpecification {
	t.Helper()
	return decisionengine.NewSceneMappingSpecification(slog.New(slog.DiscardHandler))
}

func sceneSubject(mapping *scenemappings.Mapping) *decisionengine.Subject {
	return &decisionengine.Subject{SceneMapping: mapping}
}

func TestSceneMapping_KindIsTemporary(t *testing.T) {
	t.Parallel()
	if newSceneMappingSpec(t).Kind() != decisionengine.RejectionTemporary {
		t.Error("Kind should be Temporary")
	}
}

func TestSceneMapping_NoMapping_Accept(t *testing.T) {
	t.Parallel()
	spec := newSceneMappingSpec(t)
	if !spec.IsSatisfiedBy(t.Context(), sceneSubject(nil), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("no scene mapping should Accept")
	}
	// nil subject too.
	if !spec.IsSatisfiedBy(t.Context(), nil, decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("nil subject should Accept")
	}
}

func TestSceneMapping_BlankOrigin_Accept(t *testing.T) {
	t.Parallel()
	spec := newSceneMappingSpec(t)
	if !spec.IsSatisfiedBy(t.Context(), sceneSubject(&scenemappings.Mapping{SceneOrigin: "  "}), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("blank scene origin should Accept")
	}
}

func TestSceneMapping_MixedNoComment_RejectsAmbiguous(t *testing.T) {
	t.Parallel()
	spec := newSceneMappingSpec(t)
	got := spec.IsSatisfiedBy(t.Context(), sceneSubject(&scenemappings.Mapping{SceneOrigin: "mixed"}), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonAmbiguousNumbering {
		t.Fatalf("mixed origin should reject AmbiguousNumbering; got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
	if got.Message != "Ambiguous numbering" {
		t.Errorf("message = %q, want %q", got.Message, "Ambiguous numbering")
	}
}

func TestSceneMapping_MixedWithComment_RejectsWithCFMessage(t *testing.T) {
	t.Parallel()
	spec := newSceneMappingSpec(t)
	got := spec.IsSatisfiedBy(t.Context(), sceneSubject(&scenemappings.Mapping{SceneOrigin: "mixed", Comment: "TheXEM note"}), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonAmbiguousNumbering {
		t.Fatalf("mixed+comment should reject AmbiguousNumbering; got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
	// Faithful to the C# latent un-formatted literal.
	if got.Message != "{0} has ambiguous numbering" {
		t.Errorf("message = %q, want the faithful C# literal %q", got.Message, "{0} has ambiguous numbering")
	}
}

func TestSceneMapping_Unknown_Accept(t *testing.T) {
	t.Parallel()
	spec := newSceneMappingSpec(t)
	if !spec.IsSatisfiedBy(t.Context(), sceneSubject(&scenemappings.Mapping{SceneOrigin: "unknown:tvdb"}), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("unknown origin should Accept (log only)")
	}
}

func TestSceneMapping_OtherOrigin_Accept(t *testing.T) {
	t.Parallel()
	spec := newSceneMappingSpec(t)
	if !spec.IsSatisfiedBy(t.Context(), sceneSubject(&scenemappings.Mapping{SceneOrigin: "tvdb"}), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("non-mixed/non-unknown origin should Accept")
	}
}
