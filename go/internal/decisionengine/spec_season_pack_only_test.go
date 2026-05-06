package decisionengine_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// fixedNow returns a fixed clock function returning t.
func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// makeSeasonPackOnlySpec builds the spec with a fixed clock and a
// discarded logger.
func makeSeasonPackOnlySpec(now time.Time) *decisionengine.SeasonPackOnlySpecification {
	return decisionengine.NewSeasonPackOnlySpecification(fixedNow(now), slog.New(slog.DiscardHandler))
}

// makeSPOSubject builds a Subject with one episode, Standard series,
// non-FullSeason parsed info, and a release with the given
// SeasonSearchMaximumSingleEpisodeAge value.
func makeSPOSubject(seasonNumber, maxAgeDays int) *decisionengine.Subject {
	return &decisionengine.Subject{
		Series: &series.Series{ID: 7, SeriesType: series.SeriesTypeStandard},
		Episodes: []episodes.Episode{
			{ID: 1, SeasonNumber: seasonNumber, EpisodeNumber: 5},
		},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{FullSeason: false},
		Release: &parser.ReleaseInfo{
			Title:                               "Show.S01E05",
			SeasonSearchMaximumSingleEpisodeAge: maxAgeDays,
		},
	}
}

// makeSearchInfo builds a ReleaseDecisionInformation with the given
// search-criteria episodes attached.
func makeSearchInfo(eps []episodes.Episode) decisionengine.ReleaseDecisionInformation {
	return decisionengine.ReleaseDecisionInformation{
		SearchCriteria: &decisionengine.SearchCriteria{Episodes: eps},
	}
}

// asAirDate is a helper to take a *time.Time addr from a literal.
func asAirDate(t time.Time) *time.Time { return &t }

func TestSeasonPackOnly_AcceptsNilSearchCriteria(t *testing.T) {
	t.Parallel()
	spec := makeSeasonPackOnlySpec(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	subj := makeSPOSubject(1, 30)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil SearchCriteria, got %+v", got)
	}
}

func TestSeasonPackOnly_AcceptsSingleEpisodeSearch(t *testing.T) {
	t.Parallel()
	spec := makeSeasonPackOnlySpec(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	subj := makeSPOSubject(1, 30)
	air := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // very old
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept on 1-episode search, got %+v", got)
	}
}

func TestSeasonPackOnly_AcceptsMaxAgeZero(t *testing.T) {
	t.Parallel()
	spec := makeSeasonPackOnlySpec(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	subj := makeSPOSubject(1, 0)
	air := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // way old
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
		{ID: 2, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept when MaxAge=0 (opt-out), got %+v", got)
	}
}

func TestSeasonPackOnly_AcceptsNonStandardSeries(t *testing.T) {
	t.Parallel()
	spec := makeSeasonPackOnlySpec(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	subj := makeSPOSubject(1, 30)
	subj.Series.SeriesType = series.SeriesTypeAnime
	air := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
		{ID: 2, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept on Anime series, got %+v", got)
	}
}

func TestSeasonPackOnly_AcceptsFullSeasonRelease(t *testing.T) {
	t.Parallel()
	spec := makeSeasonPackOnlySpec(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	subj := makeSPOSubject(1, 30)
	subj.ParsedEpisodeInfo.FullSeason = true
	air := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
		{ID: 2, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept on FullSeason release, got %+v", got)
	}
}

func TestSeasonPackOnly_RejectsTooOldSingleEpisode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeSeasonPackOnlySpec(now)
	subj := makeSPOSubject(1, 30)
	air := now.Add(-60 * 24 * time.Hour) // 60 days ago, well past 30-day cutoff
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
		{ID: 2, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if got.Accepted {
		t.Errorf("expected Reject on aged-out season, got Accept")
	}
	if got.Reason != decisionengine.ReasonNotSeasonPack {
		t.Errorf("Reason = %v, want ReasonNotSeasonPack", got.Reason)
	}
	if !strings.Contains(got.Message, "30 days") {
		t.Errorf("Message %q should mention the threshold", got.Message)
	}
}

func TestSeasonPackOnly_AcceptsRecentEpisode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeSeasonPackOnlySpec(now)
	subj := makeSPOSubject(1, 30)
	air := now.Add(-7 * 24 * time.Hour) // 7 days ago, within 30-day window
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
		{ID: 2, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept on recent episode, got %+v", got)
	}
}

func TestSeasonPackOnly_FiltersBySeasonOfCandidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeSeasonPackOnlySpec(now)
	subj := makeSPOSubject(2, 30) // candidate is in season 2
	oldAir := now.Add(-60 * 24 * time.Hour)
	recentAir := now.Add(-1 * 24 * time.Hour)
	// Search criteria spans both seasons. Season 1 is old (irrelevant);
	// Season 2 (the candidate's season) is recent → accept.
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: asAirDate(oldAir)},
		{ID: 2, SeasonNumber: 2, AirDateUtc: asAirDate(recentAir)},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept (season-2 episode is recent), got %+v", got)
	}
}

func TestSeasonPackOnly_AcceptsWhenAllAirDatesNil(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeSeasonPackOnlySpec(now)
	subj := makeSPOSubject(1, 30)
	// No AirDateUtc on any criteria episode → C# `subset.Count > 0`
	// gate fails → accept.
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: nil},
		{ID: 2, SeasonNumber: 1, AirDateUtc: nil},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept on no-air-dates, got %+v", got)
	}
}

func TestSeasonPackOnly_UsesMaxAirDateAcrossSubset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeSeasonPackOnlySpec(now)
	subj := makeSPOSubject(1, 30)
	veryOld := now.Add(-100 * 24 * time.Hour)
	recent := now.Add(-2 * 24 * time.Hour)
	// Most episodes are old, but one is recent — the spec uses MAX of
	// AirDateUtc, so the recent one wins → accept.
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: asAirDate(veryOld)},
		{ID: 2, SeasonNumber: 1, AirDateUtc: asAirDate(veryOld)},
		{ID: 3, SeasonNumber: 1, AirDateUtc: asAirDate(recent)},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept (max air date is recent), got %+v", got)
	}
}

func TestSeasonPackOnly_AcceptsNoSubjectEpisodes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeSeasonPackOnlySpec(now)
	subj := makeSPOSubject(1, 30)
	subj.Episodes = nil
	air := now.Add(-60 * 24 * time.Hour)
	info := makeSearchInfo([]episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
		{ID: 2, SeasonNumber: 1, AirDateUtc: asAirDate(air)},
	})
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept on empty subject episodes, got %+v", got)
	}
}

func TestSeasonPackOnly_AcceptsWrongTypeSearchCriteria(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := makeSeasonPackOnlySpec(now)
	subj := makeSPOSubject(1, 30)
	// Pass a string as SearchCriteria → type assertion fails → accept.
	info := decisionengine.ReleaseDecisionInformation{SearchCriteria: "not a criteria"}
	got := spec.IsSatisfiedBy(t.Context(), subj, info)
	if !got.Accepted {
		t.Errorf("expected Accept on wrong-typed criteria, got %+v", got)
	}
}

func TestSeasonPackOnly_AcceptsNilRelease(t *testing.T) {
	t.Parallel()
	spec := makeSeasonPackOnlySpec(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	got := spec.IsSatisfiedBy(t.Context(), &decisionengine.Subject{}, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil release, got %+v", got)
	}
}

func TestSeasonPackOnly_InterfaceShape(t *testing.T) {
	t.Parallel()
	var iface decisionengine.Specification = decisionengine.NewSeasonPackOnlySpecification(nil, nil)
	if iface.Name() != decisionengine.SeasonPackOnlySpecificationName {
		t.Errorf("Name = %q", iface.Name())
	}
	if iface.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v", iface.Priority())
	}
}
