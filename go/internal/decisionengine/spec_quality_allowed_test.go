package decisionengine_test

import (
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// === QualityAllowedByProfileSpecification ===

func newQualityAllowedSpec(t *testing.T, profiles map[int]*decisionengine.ProfileView, lookupErr error) *decisionengine.QualityAllowedByProfileSpecification {
	t.Helper()
	reader := stubProfileReader{views: profiles, err: lookupErr}
	spec, err := decisionengine.NewQualityAllowedByProfileSpecification(reader, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewQualityAllowedByProfileSpecification: %v", err)
	}
	return spec
}

func qualityAllowedSubject(qualityProfileID int, q quality.Quality) *decisionengine.Subject {
	return &decisionengine.Subject{
		Series: &series.Series{ID: 7, QualityProfileID: qualityProfileID},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality: quality.Model{Quality: q},
		},
	}
}

// allowedProfile builds a profile whose only Allowed item is q.
func allowedProfile(q quality.Quality) *decisionengine.ProfileView {
	return &decisionengine.ProfileView{
		Items: []qualityprofiles.QualityItem{
			{Quality: q, Allowed: true},
		},
	}
}

// disallowedProfile builds a profile with q as Allowed=false.
func disallowedProfile(q quality.Quality) *decisionengine.ProfileView {
	return &decisionengine.ProfileView{
		Items: []qualityprofiles.QualityItem{
			{Quality: q, Allowed: false},
		},
	}
}

func TestQualityAllowed_NewRejectsNilReader(t *testing.T) {
	t.Parallel()
	_, err := decisionengine.NewQualityAllowedByProfileSpecification(nil, nil)
	if err == nil {
		t.Errorf("expected error on nil reader")
	}
}

func TestQualityAllowed_AcceptsAllowedQuality(t *testing.T) {
	t.Parallel()
	id := 1
	spec := newQualityAllowedSpec(t, map[int]*decisionengine.ProfileView{
		1: allowedProfile(quality.HDTV720p),
	}, nil)
	got := spec.IsSatisfiedBy(t.Context(), qualityAllowedSubject(id, quality.HDTV720p),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on allowed quality, got %+v", got)
	}
}

func TestQualityAllowed_RejectsDisallowedQuality(t *testing.T) {
	t.Parallel()
	id := 1
	spec := newQualityAllowedSpec(t, map[int]*decisionengine.ProfileView{
		1: disallowedProfile(quality.HDTV720p),
	}, nil)
	got := spec.IsSatisfiedBy(t.Context(), qualityAllowedSubject(id, quality.HDTV720p),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on disallowed quality, got Accept")
	}
	if got.Reason != decisionengine.ReasonQualityNotWanted {
		t.Errorf("Reason = %v, want ReasonQualityNotWanted", got.Reason)
	}
}

func TestQualityAllowed_NilSeriesAccepts(t *testing.T) {
	t.Parallel()
	spec := newQualityAllowedSpec(t, nil, nil)
	got := spec.IsSatisfiedBy(t.Context(), &decisionengine.Subject{}, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil Series, got %+v", got)
	}
}

func TestQualityAllowed_NilProfileIDAccepts(t *testing.T) {
	t.Parallel()
	spec := newQualityAllowedSpec(t, nil, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		qualityAllowedSubject(0, quality.HDTV720p),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil QualityProfileID, got %+v", got)
	}
}

func TestQualityAllowed_LookupErrorAccepts(t *testing.T) {
	t.Parallel()
	id := 1
	spec := newQualityAllowedSpec(t, nil, errors.New("db down"))
	got := spec.IsSatisfiedBy(t.Context(),
		qualityAllowedSubject(id, quality.HDTV720p),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected defensive Accept on lookup error, got %+v", got)
	}
}

func TestQualityAllowed_MissingProfileAccepts(t *testing.T) {
	t.Parallel()
	id := 999 // not in the views map
	spec := newQualityAllowedSpec(t, map[int]*decisionengine.ProfileView{
		1: allowedProfile(quality.HDTV720p),
	}, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		qualityAllowedSubject(id, quality.HDTV720p),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on missing profile, got %+v", got)
	}
}

func TestQualityAllowed_EmptyProfileAccepts(t *testing.T) {
	t.Parallel()
	id := 1
	spec := newQualityAllowedSpec(t, map[int]*decisionengine.ProfileView{
		1: {Items: nil},
	}, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		qualityAllowedSubject(id, quality.HDTV720p),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on empty profile, got %+v", got)
	}
}

// TestQualityAllowed_UnknownQualityCSharpQuirk pins the C#
// `GetIndex(unknown) → Index=0` quirk: an unknown quality is checked
// against the FIRST item's Allowed bit (not rejected outright).
//
// Profile: [DVD (Allowed=true)]. Subject parses as HDTV720p (not in
// profile). Expectation: Accept (Index=0 → DVD → Allowed).
func TestQualityAllowed_UnknownQualityCSharpQuirk_FirstAllowed(t *testing.T) {
	t.Parallel()
	id := 1
	spec := newQualityAllowedSpec(t, map[int]*decisionengine.ProfileView{
		1: allowedProfile(quality.DVD),
	}, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		qualityAllowedSubject(id, quality.HDTV720p),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on unknown quality with first-item-Allowed (C# GetIndex=0 quirk), got %+v", got)
	}
}

// TestQualityAllowed_UnknownQualityCSharpQuirk_FirstDisallowed pins
// the inverse: when the first profile item is Disallowed, an unknown
// quality is ALSO rejected (because the C# Index=0 lands on the
// disallowed first item).
func TestQualityAllowed_UnknownQualityCSharpQuirk_FirstDisallowed(t *testing.T) {
	t.Parallel()
	id := 1
	spec := newQualityAllowedSpec(t, map[int]*decisionengine.ProfileView{
		1: disallowedProfile(quality.DVD),
	}, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		qualityAllowedSubject(id, quality.HDTV720p),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on unknown quality with first-item-Disallowed (C# quirk), got Accept")
	}
}

func TestQualityAllowed_Metadata(t *testing.T) {
	t.Parallel()
	spec := newQualityAllowedSpec(t, nil, nil)
	if spec.Name() != decisionengine.QualityAllowedByProfileSpecificationName {
		t.Errorf("Name mismatch")
	}
	if spec.Name() != "QualityAllowedByProfile" {
		t.Errorf("Name = %q, want QualityAllowedByProfile", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
}
