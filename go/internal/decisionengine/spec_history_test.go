package decisionengine_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

var histNow = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

// --- stubs --- (reuses stubCDH + stubHistoryByEpisode from spec_already_imported_test.go)

type stubHistoryFormats struct{}

func (stubHistoryFormats) ParseCustomFormat(_ context.Context, _ *history.Entry, _ *series.Series) ([]customformats.CustomFormat, error) {
	return nil, nil
}

type stubHistoryProfile struct{ view *decisionengine.ProfileView }

func (s stubHistoryProfile) LookupProfile(_ context.Context, _ int) (*decisionengine.ProfileView, bool, error) {
	return s.view, s.view != nil, nil
}

// --- helpers ---

// histView builds a 3-tier ProfileView (SDTV<HDTV720p<HDTV1080p) with
// the given cutoff and upgrades allowed.
func histView(cutoff quality.Quality) *decisionengine.ProfileView {
	return &decisionengine.ProfileView{
		Profile: &qualityprofiles.Profile{Name: "P", UpgradeAllowed: true, Cutoff: cutoff.ID},
		Items: []qualityprofiles.QualityItem{
			{Quality: quality.SDTV, Allowed: true},
			{Quality: quality.HDTV720p, Allowed: true},
			{Quality: quality.HDTV1080p, Allowed: true},
		},
	}
}

func grabRow(q quality.Quality, at time.Time) history.Entry {
	qm, _ := json.Marshal(quality.Model{Quality: q, Revision: quality.NewRevision()})
	et := int(history.EventTypeGrabbed)
	return history.Entry{EpisodeID: 1, EventType: &et, Date: at, Quality: string(qm)}
}

func histImportedRow(q quality.Quality, at time.Time) history.Entry {
	qm, _ := json.Marshal(quality.Model{Quality: q, Revision: quality.NewRevision()})
	et := int(history.EventTypeDownloadFolderImported)
	return history.Entry{EpisodeID: 1, EventType: &et, Date: at, Quality: string(qm)}
}

func histSubject(candidate quality.Quality) *decisionengine.Subject {
	return &decisionengine.Subject{
		Series:            &series.Series{ID: 7, QualityProfileID: 3},
		Episodes:          []episodes.Episode{{ID: 1, EpisodeNumber: 1}},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{Quality: quality.Model{Quality: candidate, Revision: quality.NewRevision()}},
		Release:           &parser.ReleaseInfo{Title: "Show.S01E01"},
	}
}

func newHistorySpec(t *testing.T, rows []history.Entry, cutoff quality.Quality, cdh bool) *decisionengine.HistorySpecification {
	t.Helper()
	upg, err := decisionengine.NewUpgradableSpecification(stubPropersCfg{kind: quality.ProperPreferAndUpgrade}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewUpgradableSpecification: %v", err)
	}
	spec, err := decisionengine.NewHistorySpecification(
		stubHistoryByEpisode{rows: rows},
		upg,
		stubHistoryProfile{view: histView(cutoff)},
		stubHistoryFormats{},
		stubCDH(cdh),
		func() time.Time { return histNow },
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewHistorySpecification: %v", err)
	}
	return spec
}

// --- tests ---

func TestHistory_SearchBypass(t *testing.T) {
	t.Parallel()
	// Would reject (recent grab at cutoff), but a search bypasses it.
	spec := newHistorySpec(t, []history.Entry{grabRow(quality.HDTV720p, histNow.Add(-1*time.Hour))}, quality.HDTV720p, true)
	info := decisionengine.ReleaseDecisionInformation{SearchCriteria: &decisionengine.SearchCriteria{}}
	if !spec.IsSatisfiedBy(t.Context(), histSubject(quality.HDTV720p), info).Accepted {
		t.Error("search should bypass the history gate")
	}
}

func TestHistory_NoHistory_Accept(t *testing.T) {
	t.Parallel()
	spec := newHistorySpec(t, nil, quality.HDTV720p, true)
	if !spec.IsSatisfiedBy(t.Context(), histSubject(quality.HDTV720p), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("no history → Accept")
	}
}

func TestHistory_MostRecentNotGrabbed_Accept(t *testing.T) {
	t.Parallel()
	// Most recent is an import (date-DESC first) → the grab gate is skipped.
	rows := []history.Entry{
		histImportedRow(quality.HDTV720p, histNow.Add(-1*time.Hour)),
		grabRow(quality.HDTV720p, histNow.Add(-2*time.Hour)),
	}
	spec := newHistorySpec(t, rows, quality.HDTV720p, true)
	if !spec.IsSatisfiedBy(t.Context(), histSubject(quality.HDTV720p), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("most-recent-not-grabbed → Accept")
	}
}

func TestHistory_RecentGrabAtCutoff_RejectsRecentCutoffMet(t *testing.T) {
	t.Parallel()
	spec := newHistorySpec(t, []history.Entry{grabRow(quality.HDTV720p, histNow.Add(-1*time.Hour))}, quality.HDTV720p, true)
	got := spec.IsSatisfiedBy(t.Context(), histSubject(quality.HDTV720p), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonHistoryRecentCutoffMet {
		t.Fatalf("recent grab at cutoff should reject HistoryRecentCutoffMet; got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
}

func TestHistory_OldGrabWithCDH_Accept(t *testing.T) {
	t.Parallel()
	// Old grab (>12h) + CDH enabled → skip (CDH reconciles post-import).
	spec := newHistorySpec(t, []history.Entry{grabRow(quality.HDTV720p, histNow.Add(-24*time.Hour))}, quality.HDTV720p, true)
	if !spec.IsSatisfiedBy(t.Context(), histSubject(quality.HDTV720p), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("old grab + CDH enabled → Accept")
	}
}

func TestHistory_OldGrabCdhDisabledAtCutoff_RejectsCdhDisabled(t *testing.T) {
	t.Parallel()
	// Old grab + CDH DISABLED → the gate still applies; cutoff met → reject.
	spec := newHistorySpec(t, []history.Entry{grabRow(quality.HDTV720p, histNow.Add(-24*time.Hour))}, quality.HDTV720p, false)
	got := spec.IsSatisfiedBy(t.Context(), histSubject(quality.HDTV720p), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonHistoryCdhDisabledCutoffMet {
		t.Fatalf("old grab CDH-disabled at cutoff should reject HistoryCdhDisabledCutoffMet; got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
}

func TestHistory_GenuineUpgrade_Accept(t *testing.T) {
	t.Parallel()
	// Recent grab SDTV, candidate HDTV1080p, cutoff HDTV1080p → real upgrade.
	spec := newHistorySpec(t, []history.Entry{grabRow(quality.SDTV, histNow.Add(-1*time.Hour))}, quality.HDTV1080p, true)
	if !spec.IsSatisfiedBy(t.Context(), histSubject(quality.HDTV1080p), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("candidate is a genuine upgrade over the recent grab → Accept")
	}
}

func TestHistory_RecentGrabHigherQuality_RejectsHigherPreference(t *testing.T) {
	t.Parallel()
	// Recent grab HDTV720p, candidate SDTV (lower), cutoff HDTV1080p (so the
	// grab hasn't met cutoff) → equal-or-higher preference reject.
	spec := newHistorySpec(t, []history.Entry{grabRow(quality.HDTV720p, histNow.Add(-1*time.Hour))}, quality.HDTV1080p, true)
	got := spec.IsSatisfiedBy(t.Context(), histSubject(quality.SDTV), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonHistoryHigherPreference {
		t.Fatalf("recent higher-quality grab should reject HistoryHigherPreference; got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
}
