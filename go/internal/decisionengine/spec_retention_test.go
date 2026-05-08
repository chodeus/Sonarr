package decisionengine_test

import (
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
)

// === RetentionSpecification ===

type stubRetentionCfg struct{ days int }

func (s stubRetentionCfg) Retention() int { return s.days }

func newRetentionSpec(t *testing.T, days int) *decisionengine.RetentionSpecification {
	t.Helper()
	spec, err := decisionengine.NewRetentionSpecification(stubRetentionCfg{days: days}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewRetentionSpecification: %v", err)
	}
	return spec
}

func retentionSubject(protocol indexers.DownloadProtocol, ageDays int) *decisionengine.Subject {
	return &decisionengine.Subject{
		Release: &parser.ReleaseInfo{
			Title:            "Show.S01E01",
			DownloadProtocol: protocol,
			PublishDate:      time.Now().Add(-time.Duration(ageDays) * 24 * time.Hour),
		},
	}
}

func TestRetention_NewRejectsNilCfg(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewRetentionSpecification(nil, nil); err == nil {
		t.Errorf("expected error on nil cfg")
	}
}

func TestRetention_AcceptsNonUsenet(t *testing.T) {
	t.Parallel()
	spec := newRetentionSpec(t, 100)
	got := spec.IsSatisfiedBy(t.Context(), retentionSubject(indexers.DownloadProtocolTorrent, 1000),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on non-usenet release, got %+v", got)
	}
}

func TestRetention_AcceptsWhenDisabled(t *testing.T) {
	t.Parallel()
	spec := newRetentionSpec(t, 0)
	got := spec.IsSatisfiedBy(t.Context(), retentionSubject(indexers.DownloadProtocolUsenet, 1000),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when retention=0, got %+v", got)
	}
}

func TestRetention_AcceptsYoungerThanLimit(t *testing.T) {
	t.Parallel()
	spec := newRetentionSpec(t, 30)
	got := spec.IsSatisfiedBy(t.Context(), retentionSubject(indexers.DownloadProtocolUsenet, 5),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on younger release, got %+v", got)
	}
}

func TestRetention_RejectsOlderThanLimit(t *testing.T) {
	t.Parallel()
	spec := newRetentionSpec(t, 30)
	got := spec.IsSatisfiedBy(t.Context(), retentionSubject(indexers.DownloadProtocolUsenet, 50),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on too-old release, got Accept")
	}
	if got.Reason != decisionengine.ReasonMaximumAge {
		t.Errorf("Reason = %v, want ReasonMaximumAge", got.Reason)
	}
}

func TestRetention_BoundaryEqual(t *testing.T) {
	t.Parallel()
	// Age exactly == retention should pass (C# `age > retention` not `>=`).
	spec := newRetentionSpec(t, 30)
	got := spec.IsSatisfiedBy(t.Context(), retentionSubject(indexers.DownloadProtocolUsenet, 30),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept at age==retention boundary, got %+v", got)
	}
}

func TestRetention_NilSubjectAccepts(t *testing.T) {
	t.Parallel()
	spec := newRetentionSpec(t, 30)
	got := spec.IsSatisfiedBy(t.Context(), nil, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil subject, got %+v", got)
	}
}

func TestRetention_Metadata(t *testing.T) {
	t.Parallel()
	spec := newRetentionSpec(t, 0)
	if spec.Name() != decisionengine.RetentionSpecificationName || spec.Name() != "Retention" {
		t.Errorf("Name mismatch: %q", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
}
