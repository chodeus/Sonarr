package decisionengine_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// === AirDateSpecification ===

func newAirDateSpec(t *testing.T, profiles []releaseprofiles.Profile, err error) *decisionengine.AirDateSpecification {
	t.Helper()
	spec, e := decisionengine.NewAirDateSpecification(
		stubReleaseProfileFinder{profiles: profiles, err: err},
		slog.New(slog.DiscardHandler),
	)
	if e != nil {
		t.Fatalf("NewAirDateSpecification: %v", e)
	}
	return spec
}

func airDateSubject(publishDate time.Time, airDates []*time.Time) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(airDates))
	for i, ad := range airDates {
		eps[i] = episodes.Episode{ID: i + 1, AirDateUtc: ad}
	}
	return &decisionengine.Subject{
		Series: &series.Series{ID: 7, Tags: []int{1}},
		Release: &parser.ReleaseInfo{
			Title:       "Show.S01E01",
			IndexerID:   5,
			PublishDate: publishDate,
		},
		Episodes: eps,
	}
}

func TestAirDate_NewRejectsNilProfiles(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewAirDateSpecification(nil, nil); err == nil {
		t.Errorf("expected error on nil profiles")
	}
}

func TestAirDate_AcceptsNoProfiles(t *testing.T) {
	t.Parallel()
	spec := newAirDateSpec(t, nil, nil)
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	got := spec.IsSatisfiedBy(t.Context(), airDateSubject(now, nil),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on no profiles, got %+v", got)
	}
}

func TestAirDate_AcceptsWhenRestrictionDisabled(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{AirDateRestriction: false, AirDateGracePeriod: 0},
	}
	spec := newAirDateSpec(t, profiles, nil)
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	got := spec.IsSatisfiedBy(t.Context(), airDateSubject(now, nil),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when AirDateRestriction=false, got %+v", got)
	}
}

func TestAirDate_RejectsBeforeAdjustedAirDate(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{AirDateRestriction: true, AirDateGracePeriod: 7},
	}
	spec := newAirDateSpec(t, profiles, nil)
	publish := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	airDate := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC) // adjusted = Apr 12
	got := spec.IsSatisfiedBy(t.Context(),
		airDateSubject(publish, []*time.Time{&airDate}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject before adjusted airdate, got Accept")
	}
	if got.Reason != decisionengine.ReasonBeforeAirDate {
		t.Errorf("Reason = %v, want ReasonBeforeAirDate", got.Reason)
	}
}

func TestAirDate_AcceptsAtOrAfterAdjustedAirDate(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{AirDateRestriction: true, AirDateGracePeriod: 7},
	}
	spec := newAirDateSpec(t, profiles, nil)
	airDate := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC) // adjusted = Apr 8
	publish := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	got := spec.IsSatisfiedBy(t.Context(),
		airDateSubject(publish, []*time.Time{&airDate}),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept after adjusted airdate, got %+v", got)
	}
}

func TestAirDate_RejectsNilAirDate(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{AirDateRestriction: true, AirDateGracePeriod: 0},
	}
	spec := newAirDateSpec(t, profiles, nil)
	publish := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	got := spec.IsSatisfiedBy(t.Context(),
		airDateSubject(publish, []*time.Time{nil}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on nil air date, got Accept")
	}
	if got.Reason != decisionengine.ReasonBeforeAirDate {
		t.Errorf("Reason = %v, want ReasonBeforeAirDate", got.Reason)
	}
}

// TestAirDate_BestProfilePicksRestrictionTrue pins the C# tie-break:
// AirDateRestriction=true profile beats one with AirDateRestriction=false
// even if the false profile has the larger grace.
func TestAirDate_BestProfilePicksRestrictionTrue(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{AirDateRestriction: false, AirDateGracePeriod: 30},
		{AirDateRestriction: true, AirDateGracePeriod: 1},
	}
	spec := newAirDateSpec(t, profiles, nil)
	airDate := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) // adjusted = May 2 (1-day grace)
	publish := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	got := spec.IsSatisfiedBy(t.Context(),
		airDateSubject(publish, []*time.Time{&airDate}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject (restriction-true profile chosen), got Accept")
	}
}

// TestAirDate_BestProfilePicksLargerGrace pins the C# tie-break secondary:
// among AirDateRestriction=true profiles, the LARGER grace wins.
func TestAirDate_BestProfilePicksLargerGrace(t *testing.T) {
	t.Parallel()
	profiles := []releaseprofiles.Profile{
		{AirDateRestriction: true, AirDateGracePeriod: 1},
		{AirDateRestriction: true, AirDateGracePeriod: 30},
	}
	spec := newAirDateSpec(t, profiles, nil)
	// 30-day grace makes adjusted May 31; release at May 15 should reject
	// (1-day grace would have accepted at May 2).
	airDate := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	publish := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	got := spec.IsSatisfiedBy(t.Context(),
		airDateSubject(publish, []*time.Time{&airDate}),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject under larger-grace profile, got Accept")
	}
}

func TestAirDate_LookupErrorAcceptsDefensively(t *testing.T) {
	t.Parallel()
	spec := newAirDateSpec(t, nil, errors.New("db down"))
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	got := spec.IsSatisfiedBy(t.Context(), airDateSubject(now, nil),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected defensive Accept on lookup error, got %+v", got)
	}
}

func TestAirDate_Metadata(t *testing.T) {
	t.Parallel()
	spec := newAirDateSpec(t, nil, nil)
	if spec.Name() != "AirDate" {
		t.Errorf("Name = %q, want AirDate", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDatabase {
		t.Errorf("Priority = %v, want Database", spec.Priority())
	}
}
