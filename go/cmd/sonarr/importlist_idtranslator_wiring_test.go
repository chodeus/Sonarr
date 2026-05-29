package main

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

// fakeSeriesIDSearcher returns canned results per id-kind for the
// translator test.
type fakeSeriesIDSearcher struct {
	imdb, tmdb, anilist, mal []*series.Series
	err                      error
}

func (f fakeSeriesIDSearcher) SearchForNewSeriesByImdbID(context.Context, string) ([]*series.Series, error) {
	return f.imdb, f.err
}
func (f fakeSeriesIDSearcher) SearchForNewSeriesByTmdbID(context.Context, int) ([]*series.Series, error) {
	return f.tmdb, f.err
}
func (f fakeSeriesIDSearcher) SearchForNewSeriesByAniListID(context.Context, int) ([]*series.Series, error) {
	return f.anilist, f.err
}
func (f fakeSeriesIDSearcher) SearchForNewSeriesByMyAnimeListID(context.Context, int) ([]*series.Series, error) {
	return f.mal, f.err
}

func TestImportListIDTranslator_ResolvesFirstMatch(t *testing.T) {
	t.Parallel()
	tr := importListIDTranslator{search: fakeSeriesIDSearcher{
		imdb:    []*series.Series{{TvdbID: 12345, Title: "Show"}, {TvdbID: 999}},
		tmdb:    []*series.Series{{TvdbID: 222, Title: "TmdbShow"}},
		anilist: []*series.Series{{TvdbID: 333, Title: "Anime"}},
		mal:     []*series.Series{{TvdbID: 444, Title: "MalShow"}},
	}}
	for _, c := range []struct {
		name      string
		got       func() (int, string, bool)
		wantID    int
		wantTitle string
	}{
		{"imdb", func() (int, string, bool) { return tr.ByImdb(context.Background(), "tt1") }, 12345, "Show"},
		{"tmdb", func() (int, string, bool) { return tr.ByTmdb(context.Background(), 1) }, 222, "TmdbShow"},
		{"anilist", func() (int, string, bool) { return tr.ByAniList(context.Background(), 1) }, 333, "Anime"},
		{"mal", func() (int, string, bool) { return tr.ByMyAnimeList(context.Background(), 1) }, 444, "MalShow"},
	} {
		id, title, ok := c.got()
		if !ok || id != c.wantID || title != c.wantTitle {
			t.Errorf("%s: got (%d,%q,%v), want (%d,%q,true)", c.name, id, title, ok, c.wantID, c.wantTitle)
		}
	}
}

func TestImportListIDTranslator_FalseOnEmptyErrorOrZeroTvdb(t *testing.T) {
	t.Parallel()
	// empty results
	tr := importListIDTranslator{search: fakeSeriesIDSearcher{}}
	if _, _, ok := tr.ByImdb(context.Background(), "tt1"); ok {
		t.Error("empty results must return ok=false")
	}
	// error
	trErr := importListIDTranslator{search: fakeSeriesIDSearcher{err: errors.New("boom")}}
	if _, _, ok := trErr.ByTmdb(context.Background(), 1); ok {
		t.Error("lookup error must return ok=false")
	}
	// match with TvdbID <= 0 → not usable (C# fall-through gate)
	trZero := importListIDTranslator{search: fakeSeriesIDSearcher{anilist: []*series.Series{{TvdbID: 0, Title: "x"}}}}
	if _, _, ok := trZero.ByAniList(context.Background(), 1); ok {
		t.Error("zero TvdbID must return ok=false")
	}
}
