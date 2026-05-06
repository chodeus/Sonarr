package decisionengine_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
)

// === FullSeasonSpecification ===

func makeFSSpec(now time.Time) *decisionengine.FullSeasonSpecification {
	return decisionengine.NewFullSeasonSpecification(func() time.Time { return now }, slog.New(slog.DiscardHandler))
}

// fsSubject builds a Subject with FullSeason=true and the given
// episode air-dates.
func fsSubject(fullSeason bool, airDates []*time.Time) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(airDates))
	for i, ad := range airDates {
		eps[i] = episodes.Episode{ID: i + 1, AirDateUtc: ad}
	}
	return &decisionengine.Subject{
		Episodes:          eps,
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{FullSeason: fullSeason},
		Release:           &parser.ReleaseInfo{Title: "Show.S01"},
	}
}

func TestFullSeason_AcceptsNonFullSeason(t *testing.T) {
	t.Parallel()
	spec := makeFSSpec(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	got := spec.IsSatisfiedBy(t.Context(), fsSubject(false, nil), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on non-FullSeason, got %+v", got)
	}
}

func TestFullSeason_AcceptsAllAired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeFSSpec(now)
	pastA := now.Add(-30 * 24 * time.Hour)
	pastB := now.Add(-1 * 24 * time.Hour)
	got := spec.IsSatisfiedBy(t.Context(), fsSubject(true, []*time.Time{&pastA, &pastB}), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on all-aired full season, got %+v", got)
	}
}

func TestFullSeason_AcceptsWithinGracePeriod(t *testing.T) {
	t.Parallel()
	// Episode airs in 2 hours — within 24h grace → accept.
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeFSSpec(now)
	past := now.Add(-30 * 24 * time.Hour)
	soon := now.Add(2 * time.Hour)
	got := spec.IsSatisfiedBy(t.Context(), fsSubject(true, []*time.Time{&past, &soon}), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on within-grace future episode, got %+v", got)
	}
}

func TestFullSeason_RejectsWhenAnyEpisodeUnaired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeFSSpec(now)
	past := now.Add(-30 * 24 * time.Hour)
	future := now.Add(30 * 24 * time.Hour)
	got := spec.IsSatisfiedBy(t.Context(), fsSubject(true, []*time.Time{&past, &future}), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on partially-unaired full season, got Accept")
	}
	if got.Reason != decisionengine.ReasonFullSeasonNotAired {
		t.Errorf("Reason = %v, want ReasonFullSeasonNotAired", got.Reason)
	}
	if !strings.Contains(got.Message, "All episodes haven't aired") {
		t.Errorf("Message = %q, want C#-text-matching", got.Message)
	}
}

func TestFullSeason_RejectsWhenAnyEpisodeMissingAirDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeFSSpec(now)
	past := now.Add(-30 * 24 * time.Hour)
	got := spec.IsSatisfiedBy(t.Context(), fsSubject(true, []*time.Time{&past, nil}), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on nil AirDateUtc, got Accept")
	}
}

func TestFullSeason_AcceptsNilParsedInfo(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeFSSpec(now)
	subj := &decisionengine.Subject{}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil ParsedEpisodeInfo, got %+v", got)
	}
}

func TestFullSeason_InterfaceShape(t *testing.T) {
	t.Parallel()
	var iface decisionengine.Specification = decisionengine.NewFullSeasonSpecification(nil, nil)
	if iface.Name() != decisionengine.FullSeasonSpecificationName {
		t.Errorf("Name = %q", iface.Name())
	}
	if iface.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v", iface.Priority())
	}
}

// === MultiSeasonSpecification ===

func msSubject(multi bool) *decisionengine.Subject {
	return &decisionengine.Subject{
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{IsMultiSeason: multi},
		Release:           &parser.ReleaseInfo{Title: "Show.S01-S03"},
	}
}

func TestMultiSeason_AcceptsSingleSeason(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewMultiSeasonSpecification(slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), msSubject(false), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on single-season, got %+v", got)
	}
}

func TestMultiSeason_RejectsMultiSeason(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewMultiSeasonSpecification(slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), msSubject(true), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on multi-season, got Accept")
	}
	if got.Reason != decisionengine.ReasonMultiSeason {
		t.Errorf("Reason = %v, want ReasonMultiSeason", got.Reason)
	}
	if got.Message != "Multi-season releases are not supported" {
		t.Errorf("Message = %q, want exact C# string", got.Message)
	}
}

func TestMultiSeason_AcceptsNilParsedInfo(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewMultiSeasonSpecification(slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), &decisionengine.Subject{}, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil ParsedEpisodeInfo, got %+v", got)
	}
}

func TestMultiSeason_InterfaceShape(t *testing.T) {
	t.Parallel()
	var iface decisionengine.Specification = decisionengine.NewMultiSeasonSpecification(nil)
	if iface.Name() != decisionengine.MultiSeasonSpecificationName {
		t.Errorf("Name = %q", iface.Name())
	}
	if iface.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v", iface.Priority())
	}
}
