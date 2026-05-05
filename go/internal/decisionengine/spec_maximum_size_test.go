package decisionengine_test

import (
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
)

// stubMaximumSize is the minimal MaximumSizeReader implementation used
// by the spec tests.
type stubMaximumSize struct{ mb int }

func (s stubMaximumSize) MaximumSize() int { return s.mb }

// newMaximumSizeSpec builds a MaximumSizeSpecification with a stub
// reader and a discarded logger.
func newMaximumSizeSpec(t *testing.T, mb int) *decisionengine.MaximumSizeSpecification {
	t.Helper()
	spec, err := decisionengine.NewMaximumSizeSpecification(stubMaximumSize{mb: mb}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewMaximumSizeSpecification: %v", err)
	}
	return spec
}

func TestMaximumSize_NewRejectsNilConfig(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewMaximumSizeSpecification(nil, nil); err == nil {
		t.Fatalf("expected error on nil config, got nil")
	}
}

func TestMaximumSize_AcceptsWhenMaxIsZero(t *testing.T) {
	t.Parallel()
	spec := newMaximumSizeSpec(t, 0)
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.S01E01", Size: 1000 * 1024 * 1024},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when MaximumSize=0, got %+v", got)
	}
}

func TestMaximumSize_AcceptsWhenSizeBelowMax(t *testing.T) {
	t.Parallel()
	spec := newMaximumSizeSpec(t, 2000)
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.S01E01", Size: 1999 * 1024 * 1024},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when size < max, got %+v", got)
	}
}

func TestMaximumSize_AcceptsAtBoundary(t *testing.T) {
	t.Parallel()
	// C# behaviour: `if (size > maximumSize)` — size == maximumSize accepts.
	spec := newMaximumSizeSpec(t, 2000)
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.S01E01", Size: 2000 * 1024 * 1024},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept at boundary (size == max), got %+v", got)
	}
}

func TestMaximumSize_RejectsWhenSizeAboveMax(t *testing.T) {
	t.Parallel()
	spec := newMaximumSizeSpec(t, 2000)
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.S01E01", Size: 2001 * 1024 * 1024},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject when size > max, got Accept")
	}
	if got.Reason != decisionengine.ReasonMaximumSizeExceeded {
		t.Errorf("Reason = %v, want ReasonMaximumSizeExceeded", got.Reason)
	}
	if !strings.Contains(got.Message, "too big") {
		t.Errorf("Message = %q, want to contain 'too big'", got.Message)
	}
	if !strings.Contains(got.Message, "Settings->Indexers->Maximum Size") {
		t.Errorf("Message = %q, want to contain settings hint", got.Message)
	}
}

func TestMaximumSize_AcceptsWhenSizeIsZero(t *testing.T) {
	t.Parallel()
	// Size 0 == "unknown size"; C# tree skips the check.
	spec := newMaximumSizeSpec(t, 2000)
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.S01E01", Size: 0},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on unknown size, got %+v", got)
	}
}

func TestMaximumSize_AcceptsNilRelease(t *testing.T) {
	t.Parallel()
	spec := newMaximumSizeSpec(t, 2000)
	got := spec.IsSatisfiedBy(t.Context(), &decisionengine.Subject{}, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil-release, got %+v", got)
	}
}

func TestMaximumSize_TorrentInfoFlowsThrough(t *testing.T) {
	t.Parallel()
	spec := newMaximumSizeSpec(t, 1000)
	subject := &decisionengine.Subject{
		Release: &parser.TorrentInfo{ReleaseInfo: parser.ReleaseInfo{
			Title: "Show.S01E01",
			Size:  1500 * 1024 * 1024,
		}},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on TorrentInfo above max, got Accept")
	}
}

func TestMaximumSize_AcceptsWhenMaxIsNegative(t *testing.T) {
	t.Parallel()
	// Defensive: a negative MaximumSize from a misconfigured config-service
	// is treated like 0 (disabled). The C# layer never persists negatives
	// (the column is unsigned in spirit) but the int signedness leaks
	// through the API surface, so guard against it.
	spec := newMaximumSizeSpec(t, -1)
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.S01E01", Size: 9999 * 1024 * 1024},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on negative MaximumSize, got %+v", got)
	}
}

func TestMaximumSize_InterfaceShape(t *testing.T) {
	t.Parallel()
	spec, err := decisionengine.NewMaximumSizeSpecification(stubMaximumSize{mb: 100}, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	var iface decisionengine.Specification = spec
	if iface.Name() != decisionengine.MaximumSizeSpecificationName {
		t.Errorf("Name = %q", iface.Name())
	}
	if iface.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v", iface.Priority())
	}
}
