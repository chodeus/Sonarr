package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// === ReleaseRestrictionsSpecification ===

type stubReleaseProfileFinder struct {
	profiles []releaseprofiles.Profile
	err      error
}

func (s stubReleaseProfileFinder) EnabledForTags(_ context.Context, _ []int, _ int) ([]releaseprofiles.Profile, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.profiles, nil
}

// stubTermMatcher matches any term that's a case-insensitive substring
// of value (the simplest faithful subset of the real
// releaseprofiles.MatcherFor "case-insensitive substring" matcher).
type stubTermMatcher struct{}

func (stubTermMatcher) IsMatch(term, value string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(term))
}

func newReleaseRestrictionsSpec(t *testing.T, profiles []releaseprofiles.Profile, err error) *decisionengine.ReleaseRestrictionsSpecification {
	t.Helper()
	spec, e := decisionengine.NewReleaseRestrictionsSpecification(
		stubReleaseProfileFinder{profiles: profiles, err: err},
		stubTermMatcher{},
		slog.New(slog.DiscardHandler),
	)
	if e != nil {
		t.Fatalf("NewReleaseRestrictionsSpecification: %v", e)
	}
	return spec
}

func releaseRestrictionsSubject(title string) *decisionengine.Subject {
	return &decisionengine.Subject{
		Series: &series.Series{ID: 7, Tags: []int{1, 2}},
		Release: &parser.ReleaseInfo{
			Title:     title,
			IndexerID: 5,
		},
	}
}

func TestReleaseRestrictions_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewReleaseRestrictionsSpecification(nil, stubTermMatcher{}, nil); err == nil {
		t.Errorf("expected error on nil profiles")
	}
	if _, err := decisionengine.NewReleaseRestrictionsSpecification(stubReleaseProfileFinder{}, nil, nil); err == nil {
		t.Errorf("expected error on nil matcher")
	}
}

func TestReleaseRestrictions_AcceptsNoProfiles(t *testing.T) {
	t.Parallel()
	spec := newReleaseRestrictionsSpec(t, nil, nil)
	got := spec.IsSatisfiedBy(t.Context(), releaseRestrictionsSubject("Show.S01E01"),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on no profiles, got %+v", got)
	}
}

func TestReleaseRestrictions_AcceptsRequiredFound(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{Required: []string{"WEBDL"}},
	}
	spec := newReleaseRestrictionsSpec(t, profiles, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		releaseRestrictionsSubject("Show.S01E01.WEBDL.x264"),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when required term present, got %+v", got)
	}
}

func TestReleaseRestrictions_RejectsMissingRequired(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{Required: []string{"WEBDL", "BluRay"}},
	}
	spec := newReleaseRestrictionsSpec(t, profiles, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		releaseRestrictionsSubject("Show.S01E01.HDTV.x264"),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on missing required, got Accept")
	}
	if got.Reason != decisionengine.ReasonMustContainMissing {
		t.Errorf("Reason = %v, want ReasonMustContainMissing", got.Reason)
	}
}

func TestReleaseRestrictions_RejectsIgnoredPresent(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{Ignored: []string{"CAM", "TS"}},
	}
	spec := newReleaseRestrictionsSpec(t, profiles, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		releaseRestrictionsSubject("Show.S01E01.CAM.x264"),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on ignored term, got Accept")
	}
	if got.Reason != decisionengine.ReasonMustNotContainPresent {
		t.Errorf("Reason = %v, want ReasonMustNotContainPresent", got.Reason)
	}
}

func TestReleaseRestrictions_AcceptsBothRulesPass(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{Required: []string{"WEBDL"}, Ignored: []string{"CAM"}},
	}
	spec := newReleaseRestrictionsSpec(t, profiles, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		releaseRestrictionsSubject("Show.S01E01.WEBDL.x264"),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when required present + ignored absent, got %+v", got)
	}
}

func TestReleaseRestrictions_LookupErrorAcceptsDefensively(t *testing.T) {
	t.Parallel()
	spec := newReleaseRestrictionsSpec(t, nil, errors.New("db down"))
	got := spec.IsSatisfiedBy(t.Context(), releaseRestrictionsSubject("Show.S01E01"),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected defensive Accept on lookup error, got %+v", got)
	}
}

func TestReleaseRestrictions_NilSubjectAccepts(t *testing.T) {
	t.Parallel()
	spec := newReleaseRestrictionsSpec(t, nil, nil)
	got := spec.IsSatisfiedBy(t.Context(), nil, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil subject, got %+v", got)
	}
}

func TestReleaseRestrictions_Metadata(t *testing.T) {
	t.Parallel()
	spec := newReleaseRestrictionsSpec(t, nil, nil)
	if spec.Name() != "ReleaseRestrictions" {
		t.Errorf("Name = %q, want ReleaseRestrictions", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
}
