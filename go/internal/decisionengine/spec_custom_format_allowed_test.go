package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// stubCFProfileReader is the minimal CustomFormatAllowedByProfileProfileReader.
type stubCFProfileReader struct {
	view *decisionengine.ProfileView
	ok   bool
	err  error
}

func (s stubCFProfileReader) LookupProfile(_ context.Context, _ int) (*decisionengine.ProfileView, bool, error) {
	return s.view, s.ok, s.err
}

func cfaSpec(t *testing.T, reader stubCFProfileReader) *decisionengine.CustomFormatAllowedByProfileSpecification {
	t.Helper()
	spec, err := decisionengine.NewCustomFormatAllowedByProfileSpecification(reader, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	return spec
}

func cfaProfileView(t *testing.T, minScore int) *decisionengine.ProfileView {
	t.Helper()
	view, err := decisionengine.NewProfileView(&qualityprofiles.Profile{
		Name:           "test",
		Cutoff:         9,
		Items:          "[]",
		FormatItems:    "[]",
		MinFormatScore: minScore,
	})
	if err != nil {
		t.Fatalf("NewProfileView: %v", err)
	}
	return view
}

func cfaSubject(score int) *decisionengine.Subject {
	profileID := 1
	return &decisionengine.Subject{
		Series:            &series.Series{ID: 7, QualityProfileID: profileID},
		CustomFormatScore: score,
	}
}

func TestCustomFormatAllowedByProfile_NewRejectsNilReader(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewCustomFormatAllowedByProfileSpecification(nil, nil); err == nil {
		t.Errorf("expected error on nil reader, got nil")
	}
}

// --- C# CustomFormatAllowedByProfileSpecificationFixture ports -----

// should_allow_if_format_score_greater_than_min — score=2, min=1 → Accept.
func TestCustomFormatAllowedByProfile_AcceptsScoreGreaterThanMin(t *testing.T) {
	t.Parallel()
	view := cfaProfileView(t, 1)
	spec := cfaSpec(t, stubCFProfileReader{view: view, ok: true})
	got := spec.IsSatisfiedBy(t.Context(), cfaSubject(2), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept, got %+v", got)
	}
}

// should_deny_if_format_score_not_greater_than_min — score=0, min=1 → Reject.
func TestCustomFormatAllowedByProfile_RejectsScoreBelowMin(t *testing.T) {
	t.Parallel()
	view := cfaProfileView(t, 1)
	spec := cfaSpec(t, stubCFProfileReader{view: view, ok: true})
	got := spec.IsSatisfiedBy(t.Context(), cfaSubject(0), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Fatalf("expected Reject, got Accept")
	}
	if got.Reason != decisionengine.ReasonCustomFormatMinimumScore {
		t.Errorf("Reason = %v, want ReasonCustomFormatMinimumScore", got.Reason)
	}
}

// should_deny_if_format_score_not_greater_than_min_2 — negative score, min=1.
func TestCustomFormatAllowedByProfile_RejectsNegativeScoreBelowMin(t *testing.T) {
	t.Parallel()
	view := cfaProfileView(t, 1)
	spec := cfaSpec(t, stubCFProfileReader{view: view, ok: true})
	got := spec.IsSatisfiedBy(t.Context(), cfaSubject(-5), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject for negative score below min, got Accept")
	}
}

// should_allow_if_all_format_is_defined_in_profile — score=2, min=1 → Accept.
// (Same as the first case after the C# fixture's score arithmetic.)
func TestCustomFormatAllowedByProfile_AcceptsScoreEqualToHighProfileScore(t *testing.T) {
	t.Parallel()
	view := cfaProfileView(t, 1)
	spec := cfaSpec(t, stubCFProfileReader{view: view, ok: true})
	got := spec.IsSatisfiedBy(t.Context(), cfaSubject(5), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept, got %+v", got)
	}
}

// should_deny_if_no_format_was_parsed_and_min_score_positive — score=0, min=1.
func TestCustomFormatAllowedByProfile_RejectsZeroScoreWhenMinPositive(t *testing.T) {
	t.Parallel()
	view := cfaProfileView(t, 1)
	spec := cfaSpec(t, stubCFProfileReader{view: view, ok: true})
	got := spec.IsSatisfiedBy(t.Context(), cfaSubject(0), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject when score=0 and min=1, got Accept")
	}
}

// should_allow_if_no_format_was_parsed_min_score_is_zero — score=0, min=0 → Accept.
func TestCustomFormatAllowedByProfile_AcceptsZeroScoreWhenMinZero(t *testing.T) {
	t.Parallel()
	view := cfaProfileView(t, 0)
	spec := cfaSpec(t, stubCFProfileReader{view: view, ok: true})
	got := spec.IsSatisfiedBy(t.Context(), cfaSubject(0), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when score=0 and min=0, got %+v", got)
	}
}

// --- defensive arms ------------------------------------------------

func TestCustomFormatAllowedByProfile_AcceptsOnNilSubject(t *testing.T) {
	t.Parallel()
	view := cfaProfileView(t, 1)
	spec := cfaSpec(t, stubCFProfileReader{view: view, ok: true})
	got := spec.IsSatisfiedBy(t.Context(), nil, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("nil subject must Accept defensively")
	}
}

func TestCustomFormatAllowedByProfile_AcceptsOnNilSeries(t *testing.T) {
	t.Parallel()
	view := cfaProfileView(t, 1)
	spec := cfaSpec(t, stubCFProfileReader{view: view, ok: true})
	subj := &decisionengine.Subject{Series: nil, CustomFormatScore: -10}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("nil Series must Accept defensively")
	}
}

func TestCustomFormatAllowedByProfile_AcceptsWhenSeriesHasNoProfileID(t *testing.T) {
	t.Parallel()
	view := cfaProfileView(t, 1)
	spec := cfaSpec(t, stubCFProfileReader{view: view, ok: true})
	subj := &decisionengine.Subject{
		Series:            &series.Series{ID: 7, QualityProfileID: 0},
		CustomFormatScore: -10,
	}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("nil QualityProfileID must Accept defensively")
	}
}

func TestCustomFormatAllowedByProfile_AcceptsOnLookupError(t *testing.T) {
	t.Parallel()
	spec := cfaSpec(t, stubCFProfileReader{err: errors.New("boom")})
	got := spec.IsSatisfiedBy(t.Context(), cfaSubject(-10), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("lookup error must degrade to Accept, got %+v", got)
	}
}

func TestCustomFormatAllowedByProfile_AcceptsOnProfileNotFound(t *testing.T) {
	t.Parallel()
	spec := cfaSpec(t, stubCFProfileReader{ok: false})
	got := spec.IsSatisfiedBy(t.Context(), cfaSubject(-10), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("missing profile must Accept defensively, got %+v", got)
	}
}

func TestCustomFormatAllowedByProfile_NameAndKindAndPriority(t *testing.T) {
	t.Parallel()
	spec := cfaSpec(t, stubCFProfileReader{ok: false})
	if got := spec.Name(); got != decisionengine.CustomFormatAllowedByProfileSpecificationName {
		t.Errorf("Name = %q, want %q", got, decisionengine.CustomFormatAllowedByProfileSpecificationName)
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v, want Default", spec.Priority())
	}
}
