package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

func makeMonitoredSpec() *decisionengine.MonitoredEpisodeSpecification {
	return decisionengine.NewMonitoredEpisodeSpecification(slog.New(slog.DiscardHandler))
}

func monitoredSubject(seriesMonitored bool, epMonitored ...bool) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(epMonitored))
	for i, m := range epMonitored {
		eps[i] = episodes.Episode{ID: i + 1, Monitored: m}
	}
	return &decisionengine.Subject{
		Series:   &series.Series{ID: 7, Title: "Show", Monitored: seriesMonitored},
		Episodes: eps,
	}
}

func TestMonitoredEpisodeSpec_NilSubjectOrSeries_Accept(t *testing.T) {
	t.Parallel()
	s := makeMonitoredSpec()
	info := decisionengine.NewReleaseDecisionInformation()
	if !s.IsSatisfiedBy(t.Context(), nil, info).Accepted {
		t.Error("nil subject should Accept")
	}
	if !s.IsSatisfiedBy(t.Context(), &decisionengine.Subject{}, info).Accepted {
		t.Error("nil series should Accept")
	}
}

func TestMonitoredEpisodeSpec_SeriesNotMonitored_Reject(t *testing.T) {
	t.Parallel()
	got := makeMonitoredSpec().IsSatisfiedBy(t.Context(), monitoredSubject(false, true), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted {
		t.Fatal("unmonitored series should be rejected")
	}
	if got.Reason != decisionengine.ReasonSeriesNotMonitored {
		t.Errorf("reason = %v, want SeriesNotMonitored", got.Reason)
	}
}

func TestMonitoredEpisodeSpec_AllEpisodesMonitored_Accept(t *testing.T) {
	t.Parallel()
	got := makeMonitoredSpec().IsSatisfiedBy(t.Context(), monitoredSubject(true, true, true), decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Errorf("all-monitored should Accept, got reject: %s", got.Message)
	}
}

func TestMonitoredEpisodeSpec_SingleUnmonitoredEpisode_Reject(t *testing.T) {
	t.Parallel()
	got := makeMonitoredSpec().IsSatisfiedBy(t.Context(), monitoredSubject(true, false), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonEpisodeNotMonitored {
		t.Fatalf("single unmonitored episode should reject EpisodeNotMonitored, got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
	if got.Message != "Episode is not monitored" {
		t.Errorf("message = %q", got.Message)
	}
}

func TestMonitoredEpisodeSpec_PartialUnmonitoredMulti_Reject(t *testing.T) {
	t.Parallel()
	got := makeMonitoredSpec().IsSatisfiedBy(t.Context(), monitoredSubject(true, true, false), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonEpisodeNotMonitored {
		t.Fatalf("partial-unmonitored multi should reject, got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
	if got.Message != "One or more episodes is not monitored" {
		t.Errorf("message = %q", got.Message)
	}
}

// TestMonitoredEpisodeSpec_SearchMonitoredEpisodesOnlyFalse_Accept verifies
// the C# `SearchCriteria is { MonitoredEpisodesOnly: false }` skip — an
// interactive search that opted to include unmonitored episodes bypasses
// the gate even for an unmonitored series.
func TestMonitoredEpisodeSpec_SearchMonitoredEpisodesOnlyFalse_Accept(t *testing.T) {
	t.Parallel()
	info := decisionengine.ReleaseDecisionInformation{
		SearchCriteria: &decisionengine.SearchCriteria{MonitoredEpisodesOnly: false},
	}
	got := makeMonitoredSpec().IsSatisfiedBy(t.Context(), monitoredSubject(false, false), info)
	if !got.Accepted {
		t.Errorf("search with MonitoredEpisodesOnly=false should skip the gate (Accept), got reject: %s", got.Message)
	}
}

// TestMonitoredEpisodeSpec_SearchMonitoredEpisodesOnlyTrue_StillChecks verifies
// the gate still applies when MonitoredEpisodesOnly=true (the search wants
// only monitored episodes).
func TestMonitoredEpisodeSpec_SearchMonitoredEpisodesOnlyTrue_StillChecks(t *testing.T) {
	t.Parallel()
	info := decisionengine.ReleaseDecisionInformation{
		SearchCriteria: &decisionengine.SearchCriteria{MonitoredEpisodesOnly: true},
	}
	got := makeMonitoredSpec().IsSatisfiedBy(t.Context(), monitoredSubject(false, true), info)
	if got.Accepted {
		t.Error("MonitoredEpisodesOnly=true must still reject an unmonitored series")
	}
}

func TestMonitoredEpisodeSpec_Metadata(t *testing.T) {
	t.Parallel()
	s := makeMonitoredSpec()
	if s.Name() != decisionengine.MonitoredEpisodeSpecificationName {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind() = %v, want Permanent", s.Kind())
	}
}
