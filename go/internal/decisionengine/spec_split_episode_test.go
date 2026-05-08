package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
)

// === SplitEpisodeSpecification ===

func makeSplitEpisodeSpec() *decisionengine.SplitEpisodeSpecification {
	return decisionengine.NewSplitEpisodeSpecification(slog.New(slog.DiscardHandler))
}

func splitEpisodeSubject(isSplit bool) *decisionengine.Subject {
	return &decisionengine.Subject{
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			ReleaseTitle:   "Show.S04E01.WEBRip.x264",
			IsSplitEpisode: isSplit,
		},
	}
}

func TestSplitEpisode_AcceptsRegular(t *testing.T) {
	t.Parallel()
	spec := makeSplitEpisodeSpec()
	got := spec.IsSatisfiedBy(t.Context(), splitEpisodeSubject(false), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on non-split release, got %+v", got)
	}
}

func TestSplitEpisode_RejectsSplit(t *testing.T) {
	t.Parallel()
	spec := makeSplitEpisodeSpec()
	got := spec.IsSatisfiedBy(t.Context(), splitEpisodeSubject(true), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on split release, got Accept")
	}
	if got.Reason != decisionengine.ReasonSplitEpisode {
		t.Errorf("Reason = %v, want ReasonSplitEpisode", got.Reason)
	}
}

func TestSplitEpisode_NilSubjectAccepts(t *testing.T) {
	t.Parallel()
	spec := makeSplitEpisodeSpec()
	got := spec.IsSatisfiedBy(t.Context(), nil, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil subject, got %+v", got)
	}
}

func TestSplitEpisode_NilParsedInfoAccepts(t *testing.T) {
	t.Parallel()
	spec := makeSplitEpisodeSpec()
	subject := &decisionengine.Subject{ParsedEpisodeInfo: nil}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil ParsedEpisodeInfo, got %+v", got)
	}
}

func TestSplitEpisode_NilLogDefaults(t *testing.T) {
	t.Parallel()
	// nil logger should default to slog.Default — not panic.
	spec := decisionengine.NewSplitEpisodeSpecification(nil)
	got := spec.IsSatisfiedBy(t.Context(), splitEpisodeSubject(true), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on split release with nil-log spec, got Accept")
	}
}

func TestSplitEpisode_Metadata(t *testing.T) {
	t.Parallel()
	spec := makeSplitEpisodeSpec()
	if spec.Name() != decisionengine.SplitEpisodeSpecificationName {
		t.Errorf("Name = %q, want %q", spec.Name(), decisionengine.SplitEpisodeSpecificationName)
	}
	if spec.Name() != "SplitEpisode" {
		t.Errorf("SplitEpisodeSpecificationName = %q, want %q", spec.Name(), "SplitEpisode")
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v, want Default", spec.Priority())
	}
}
