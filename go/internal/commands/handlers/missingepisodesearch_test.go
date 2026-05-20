package handlers_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/episodes"
)

type stubMissingLister struct {
	out []episodes.Episode
	err error
}

func (s stubMissingLister) EpisodesWithoutFiles(_ context.Context, _ bool, _ time.Time) ([]episodes.Episode, error) {
	return s.out, s.err
}

type stubEpisodeSearcher struct {
	per   map[int]int
	errOn map[int]error
	calls []int
}

func (s *stubEpisodeSearcher) EpisodeSearch(_ context.Context, episodeID int) (int, error) {
	s.calls = append(s.calls, episodeID)
	if e, ok := s.errOn[episodeID]; ok {
		return 0, e
	}
	if n, ok := s.per[episodeID]; ok {
		return n, nil
	}
	return 0, nil
}

func TestMissingEpisodeSearch_StubWhenUnwired(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewMissingEpisodeSearch(nil, nil, logger)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "stub Execute") {
		t.Errorf("expected stub log; got: %q", buf.String())
	}
}

func TestMissingEpisodeSearch_DispatchesPerEpisode(t *testing.T) {
	t.Parallel()
	lister := stubMissingLister{out: []episodes.Episode{
		{ID: 1, SeriesID: 10},
		{ID: 2, SeriesID: 10},
		{ID: 3, SeriesID: 20},
	}}
	searcher := &stubEpisodeSearcher{per: map[int]int{1: 2, 2: 0, 3: 5}}
	h := handlers.NewMissingEpisodeSearch(lister, searcher, slog.Default())
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(searcher.calls) != 3 {
		t.Errorf("per-episode dispatch: got %d calls, want 3 (%+v)", len(searcher.calls), searcher.calls)
	}
}

func TestMissingEpisodeSearch_SeriesIdFilter(t *testing.T) {
	t.Parallel()
	lister := stubMissingLister{out: []episodes.Episode{
		{ID: 1, SeriesID: 10},
		{ID: 2, SeriesID: 10},
		{ID: 3, SeriesID: 20},
	}}
	searcher := &stubEpisodeSearcher{}
	h := handlers.NewMissingEpisodeSearch(lister, searcher, slog.Default())
	if err := h.Execute(t.Context(), `{"seriesId":10}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Series 10 has 2 episodes; series 20's episode should be filtered.
	if len(searcher.calls) != 2 {
		t.Errorf("SeriesId filter: got %d calls, want 2 (%+v)", len(searcher.calls), searcher.calls)
	}
	for _, id := range searcher.calls {
		if id == 3 {
			t.Errorf("episode 3 (series 20) should be filtered; got call for it")
		}
	}
}

func TestMissingEpisodeSearch_PerEpisodeErrorAbsorbed(t *testing.T) {
	t.Parallel()
	lister := stubMissingLister{out: []episodes.Episode{
		{ID: 1, SeriesID: 1},
		{ID: 2, SeriesID: 1},
	}}
	searcher := &stubEpisodeSearcher{
		per:   map[int]int{1: 3},
		errOn: map[int]error{2: errors.New("indexer down")},
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h := handlers.NewMissingEpisodeSearch(lister, searcher, logger)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute: want nil (per-episode error absorbed); got %v", err)
	}
	if !strings.Contains(buf.String(), "per-episode search failed") {
		t.Errorf("expected per-episode warn log; got: %q", buf.String())
	}
	if len(searcher.calls) != 2 {
		t.Errorf("both episodes should be attempted: got %d calls", len(searcher.calls))
	}
}

func TestMissingEpisodeSearch_ListerErrorPropagates(t *testing.T) {
	t.Parallel()
	lister := stubMissingLister{err: errors.New("DB error")}
	searcher := &stubEpisodeSearcher{}
	h := handlers.NewMissingEpisodeSearch(lister, searcher, slog.Default())
	if err := h.Execute(t.Context(), ""); err == nil {
		t.Errorf("Execute: want error (lister enumerate failure)")
	}
	if len(searcher.calls) != 0 {
		t.Errorf("searcher should not be called when lister fails")
	}
}

func TestMissingEpisodeSearch_EmptyCandidateSet(t *testing.T) {
	t.Parallel()
	lister := stubMissingLister{out: nil}
	searcher := &stubEpisodeSearcher{}
	h := handlers.NewMissingEpisodeSearch(lister, searcher, slog.Default())
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute: %v", err)
	}
	if len(searcher.calls) != 0 {
		t.Errorf("empty candidate set: got %d calls, want 0", len(searcher.calls))
	}
}

func TestMissingEpisodeSearch_MalformedBodyUsesDefaults(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	lister := stubMissingLister{out: []episodes.Episode{{ID: 1}}}
	searcher := &stubEpisodeSearcher{}
	h := handlers.NewMissingEpisodeSearch(lister, searcher, logger)
	if err := h.Execute(t.Context(), `not json`); err != nil {
		t.Errorf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "malformed body") {
		t.Errorf("expected malformed-body warn log; got: %q", buf.String())
	}
	if len(searcher.calls) != 1 {
		t.Errorf("malformed body should default to monitored=true + no SeriesId: got %d calls", len(searcher.calls))
	}
}
