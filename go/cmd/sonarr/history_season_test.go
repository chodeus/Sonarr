package main

import (
	"context"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
)

// fakeHistoryEntryReader is a minimal history.EntryReader returning
// canned series rows; the other methods are unused by GetBySeason.
type fakeHistoryEntryReader struct{ seriesRows []history.Entry }

func (f fakeHistoryEntryReader) AllBySeriesID(context.Context, int) ([]history.Entry, error) {
	return f.seriesRows, nil
}
func (fakeHistoryEntryReader) AllByDownloadID(context.Context, string) ([]history.Entry, error) {
	return nil, nil
}
func (fakeHistoryEntryReader) AllByEpisodeID(context.Context, int) ([]history.Entry, error) {
	return nil, nil
}
func (fakeHistoryEntryReader) Insert(_ context.Context, e *history.Entry) (*history.Entry, error) {
	return e, nil
}
func (fakeHistoryEntryReader) DeleteForSeries(context.Context, []int) (int64, error) { return 0, nil }
func (fakeHistoryEntryReader) Get(context.Context, int) (*history.Entry, error)      { return nil, nil }
func (fakeHistoryEntryReader) Since(context.Context, time.Time) ([]history.Entry, error) {
	return nil, nil
}

// fakeSeasonEpisodes satisfies historyEpisodeLookup, returning a fixed
// set of season episodes from GetEpisodesBySeason.
type fakeSeasonEpisodes struct{ eps []episodes.Episode }

func (fakeSeasonEpisodes) GetEpisode(context.Context, int) (*episodes.Episode, error) {
	return nil, episodes.ErrNotFound
}
func (f fakeSeasonEpisodes) GetEpisodesBySeason(context.Context, int, int) ([]episodes.Episode, error) {
	return f.eps, nil
}

// J428 (G8): GetBySeason filters the series' history to the season's
// episode-id set.
func TestHistoryServiceWithPopulator_GetBySeason_J428(t *testing.T) {
	t.Parallel()
	svc := history.NewService(fakeHistoryEntryReader{seriesRows: []history.Entry{
		{ID: 1, EpisodeID: 100}, // season 2
		{ID: 2, EpisodeID: 101}, // season 2
		{ID: 3, EpisodeID: 999}, // other season — must be filtered out
	}})
	w := &historyServiceWithPopulator{
		Service:     svc,
		episodesSvc: fakeSeasonEpisodes{eps: []episodes.Episode{{ID: 100}, {ID: 101}}},
	}

	got, err := w.GetBySeason(context.Background(), 5, 2, nil)
	if err != nil {
		t.Fatalf("GetBySeason: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (season-2 episodes only)", len(got))
	}
	for _, e := range got {
		if e.EpisodeID == 999 {
			t.Errorf("row for episode 999 leaked through the season filter")
		}
	}

	// nil episode lookup → error.
	wNil := &historyServiceWithPopulator{Service: svc}
	if _, err := wNil.GetBySeason(context.Background(), 5, 2, nil); err == nil {
		t.Error("nil episode lookup should error")
	}
}
