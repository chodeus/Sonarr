package handlers_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/episodes"
)

type stubCutoffLister struct {
	out []episodes.Episode
	err error
}

func (s stubCutoffLister) CutoffUnmetEpisodes(_ context.Context, _ bool) ([]episodes.Episode, error) {
	return s.out, s.err
}

func TestCutoffUnmetEpisodeSearch_StubWhenUnwired(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewCutoffUnmetEpisodeSearch(nil, nil, logger)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "stub Execute") {
		t.Errorf("expected stub log; got: %q", buf.String())
	}
}

func TestCutoffUnmetEpisodeSearch_DispatchesPerEpisode(t *testing.T) {
	t.Parallel()
	lister := stubCutoffLister{out: []episodes.Episode{
		{ID: 1, SeriesID: 10},
		{ID: 2, SeriesID: 10},
		{ID: 3, SeriesID: 20},
	}}
	searcher := &stubEpisodeSearcher{per: map[int]int{1: 2, 2: 1, 3: 0}}
	h := handlers.NewCutoffUnmetEpisodeSearch(lister, searcher, slog.Default())
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(searcher.calls) != 3 {
		t.Errorf("expected 3 per-episode searches; got %v", searcher.calls)
	}
}

func TestCutoffUnmetEpisodeSearch_SeriesIDFilter(t *testing.T) {
	t.Parallel()
	lister := stubCutoffLister{out: []episodes.Episode{
		{ID: 1, SeriesID: 10},
		{ID: 2, SeriesID: 10},
		{ID: 3, SeriesID: 20},
	}}
	searcher := &stubEpisodeSearcher{}
	h := handlers.NewCutoffUnmetEpisodeSearch(lister, searcher, slog.Default())
	// seriesId=20 → only episode 3 searched.
	if err := h.Execute(t.Context(), `{"seriesId":20}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(searcher.calls) != 1 || searcher.calls[0] != 3 {
		t.Errorf("seriesId filter: expected only [3] searched; got %v", searcher.calls)
	}
}

func TestCutoffUnmetEpisodeSearch_ListerError(t *testing.T) {
	t.Parallel()
	h := handlers.NewCutoffUnmetEpisodeSearch(stubCutoffLister{err: errors.New("boom")}, &stubEpisodeSearcher{}, slog.Default())
	if err := h.Execute(t.Context(), ""); err == nil {
		t.Fatal("expected lister error to propagate")
	}
}
