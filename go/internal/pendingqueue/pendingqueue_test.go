package pendingqueue_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingqueue"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
)

var fixedNow = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

// ---- stubs ----

type stubPendingLister struct{ rows []pendingreleases.Release }

func (s stubPendingLister) All(_ context.Context) ([]pendingreleases.Release, error) {
	return s.rows, nil
}

type stubSeriesLookup struct{ byID map[int]*series.Series }

func (s stubSeriesLookup) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if v, ok := s.byID[id]; ok {
		return v, nil
	}
	return nil, series.ErrNotFound
}

type stubEpisodeFinder struct{ byKey map[string]*episodes.Episode }

func (s stubEpisodeFinder) FindEpisode(_ context.Context, seriesID, season, epNum int) (*episodes.Episode, error) {
	if v, ok := s.byKey[epKey(seriesID, season, epNum)]; ok {
		return v, nil
	}
	return nil, episodes.ErrNotFound
}

func epKey(seriesID, season, epNum int) string {
	return string(rune(seriesID)) + "/" + string(rune(season)) + "/" + string(rune(epNum))
}

type stubDelay struct{ minutes int }

func (s stubDelay) ProtocolDelay(_ context.Context, _ []int, _ indexers.DownloadProtocol) (int, bool, error) {
	return s.minutes, true, nil
}

type stubConfig struct {
	minAge   int
	interval int
}

func (s stubConfig) MinimumAge() int      { return s.minAge }
func (s stubConfig) RssSyncInterval() int { return s.interval }

// ---- helpers ----

func pendingRow(t *testing.T, id, seriesID, season int, epNums []int, reason pendingreleases.Reason, publishDate time.Time) pendingreleases.Release {
	t.Helper()
	parsed, _ := json.Marshal(parser.ParsedEpisodeInfo{
		SeasonNumber:   season,
		EpisodeNumbers: epNums,
		Quality:        quality.Model{Quality: quality.HDTV720p},
	})
	rel, _ := json.Marshal(parser.ReleaseInfo{
		Title:            "Show.S01E01",
		Indexer:          "nzbIndexer",
		Size:             5000,
		DownloadProtocol: indexers.DownloadProtocolUsenet,
		PublishDate:      publishDate,
	})
	return pendingreleases.Release{
		ID:                id,
		SeriesID:          seriesID,
		Title:             "Show.S01E01",
		Added:             publishDate,
		ParsedEpisodeInfo: string(parsed),
		ReleaseInfo:       string(rel),
		Reason:            int(reason),
	}
}

func newProjector(t *testing.T, rows []pendingreleases.Release) *pendingqueue.Projector {
	t.Helper()
	p, err := pendingqueue.NewProjector(
		stubPendingLister{rows: rows},
		stubSeriesLookup{byID: map[int]*series.Series{7: {ID: 7, Title: "Show"}}},
		stubEpisodeFinder{byKey: map[string]*episodes.Episode{
			epKey(7, 1, 1): {ID: 101, SeasonNumber: 1, EpisodeNumber: 1},
		}},
		stubDelay{minutes: 0},
		stubConfig{minAge: 0, interval: 15},
		nil, // no nextRss reader
		func() time.Time { return fixedNow },
		nil,
	)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	return p
}

// ---- tests ----

func TestNewProjector_NilDeps(t *testing.T) {
	t.Parallel()
	_, err := pendingqueue.NewProjector(nil, stubSeriesLookup{}, stubEpisodeFinder{}, stubDelay{}, stubConfig{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error on nil pending lister")
	}
	_, err = pendingqueue.NewProjector(stubPendingLister{}, nil, stubEpisodeFinder{}, stubDelay{}, stubConfig{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error on nil series lookup")
	}
}

func TestProject_DelayRow(t *testing.T) {
	t.Parallel()
	rows := []pendingreleases.Release{pendingRow(t, 1, 7, 1, []int{1}, pendingreleases.ReasonDelay, fixedNow.Add(-10*time.Minute))}
	got, err := newProjector(t, rows).Project(t.Context())
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	q := got[0]
	if q.Status != queue.QueueStatusDelay {
		t.Errorf("Status = %v, want Delay", q.Status)
	}
	if q.Series == nil || q.Series.ID != 7 {
		t.Errorf("Series not rehydrated: %+v", q.Series)
	}
	if len(q.Episodes) != 1 || q.Episodes[0].ID != 101 {
		t.Errorf("Episodes not rehydrated: %+v", q.Episodes)
	}
	if q.Title != "Show.S01E01" || q.Size != 5000 || q.Indexer != "nzbIndexer" {
		t.Errorf("scalar fields wrong: title=%q size=%d indexer=%q", q.Title, q.Size, q.Indexer)
	}
	if q.Protocol != indexers.DownloadProtocolUsenet {
		t.Errorf("Protocol = %v, want Usenet", q.Protocol)
	}
	// ect = publishDate(-10m) + delay(0) + interval(15) = fixedNow+5m → timeLeft 5m.
	if q.TimeLeft == nil || *q.TimeLeft != 5*time.Minute {
		t.Errorf("TimeLeft = %v, want 5m", q.TimeLeft)
	}
	if q.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q, want empty", q.ErrorMessage)
	}
}

func TestProject_FallbackSkipped(t *testing.T) {
	t.Parallel()
	rows := []pendingreleases.Release{pendingRow(t, 2, 7, 1, []int{1}, pendingreleases.ReasonFallback, fixedNow)}
	got, err := newProjector(t, rows).Project(t.Context())
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Fallback rows should be skipped; got %d", len(got))
	}
}

func TestProject_DownloadClientUnavailableStatus(t *testing.T) {
	t.Parallel()
	rows := []pendingreleases.Release{pendingRow(t, 3, 7, 1, []int{1}, pendingreleases.ReasonDownloadClientUnavailable, fixedNow)}
	got, err := newProjector(t, rows).Project(t.Context())
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(got) != 1 || got[0].Status != queue.QueueStatusDownloadClientUnavailable {
		t.Fatalf("want one DownloadClientUnavailable row, got %+v", got)
	}
}

func TestProject_NoMatchingEpisodes_ErrorMessage(t *testing.T) {
	t.Parallel()
	// Episode 9 isn't in the finder map → empty Episodes + error message.
	rows := []pendingreleases.Release{pendingRow(t, 4, 7, 1, []int{9}, pendingreleases.ReasonDelay, fixedNow)}
	got, err := newProjector(t, rows).Project(t.Context())
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	if len(got[0].Episodes) != 0 {
		t.Errorf("Episodes = %d, want 0", len(got[0].Episodes))
	}
	if got[0].ErrorMessage != "Unable to find matching episode(s)" {
		t.Errorf("ErrorMessage = %q", got[0].ErrorMessage)
	}
}

func TestProject_MinimumAgeFloorRaisesDelay(t *testing.T) {
	t.Parallel()
	// delay profile = 0 but MinimumAge = 30 → ect = publish + 30 (>nextRss
	// absent, so + interval 15) = publish+45. publish = fixedNow → timeLeft 45m.
	p, err := pendingqueue.NewProjector(
		stubPendingLister{rows: []pendingreleases.Release{pendingRow(t, 5, 7, 1, []int{1}, pendingreleases.ReasonDelay, fixedNow)}},
		stubSeriesLookup{byID: map[int]*series.Series{7: {ID: 7}}},
		stubEpisodeFinder{byKey: map[string]*episodes.Episode{epKey(7, 1, 1): {ID: 101}}},
		stubDelay{minutes: 0},
		stubConfig{minAge: 30, interval: 15},
		nil,
		func() time.Time { return fixedNow },
		nil,
	)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	got, err := p.Project(t.Context())
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got[0].TimeLeft == nil || *got[0].TimeLeft != 45*time.Minute {
		t.Errorf("TimeLeft = %v, want 45m (MinimumAge floor 30 + interval 15)", got[0].TimeLeft)
	}
}
