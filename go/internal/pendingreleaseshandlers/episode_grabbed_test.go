package pendingreleaseshandlers_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/pendingreleaseshandlers"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

type stubGrabbedRemover struct {
	calls []pendingreleases.GrabbedInput
	err   error
}

func (s *stubGrabbedRemover) RemoveGrabbed(_ context.Context, in pendingreleases.GrabbedInput) error {
	s.calls = append(s.calls, in)
	return s.err
}

func TestHandleEpisodeGrabbed_NilRemoverErrors(t *testing.T) {
	t.Parallel()
	if err := pendingreleaseshandlers.HandleEpisodeGrabbed(t.Context(), nil, &decisionengine.EpisodeGrabbedEvent{}); err == nil {
		t.Error("nil remover should error")
	}
}

func TestHandleEpisodeGrabbed_NilEventIsNoop(t *testing.T) {
	t.Parallel()
	r := &stubGrabbedRemover{}
	if err := pendingreleaseshandlers.HandleEpisodeGrabbed(t.Context(), r, nil); err != nil {
		t.Errorf("nil event err: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("expected zero calls, got %v", r.calls)
	}
}

func TestHandleEpisodeGrabbed_NilEpisodeIsNoop(t *testing.T) {
	t.Parallel()
	r := &stubGrabbedRemover{}
	if err := pendingreleaseshandlers.HandleEpisodeGrabbed(t.Context(), r, &decisionengine.EpisodeGrabbedEvent{Episode: nil}); err != nil {
		t.Errorf("err: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("expected zero calls, got %v", r.calls)
	}
}

func TestHandleEpisodeGrabbed_NilSeriesIsNoop(t *testing.T) {
	t.Parallel()
	r := &stubGrabbedRemover{}
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{Episodes: []episodes.Episode{{EpisodeNumber: 1}}},
	}
	if err := pendingreleaseshandlers.HandleEpisodeGrabbed(t.Context(), r, ev); err != nil {
		t.Errorf("err: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("expected zero calls, got %v", r.calls)
	}
}

func TestHandleEpisodeGrabbed_EmptyEpisodesIsNoop(t *testing.T) {
	t.Parallel()
	r := &stubGrabbedRemover{}
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{Series: &series.Series{ID: 7}},
	}
	if err := pendingreleaseshandlers.HandleEpisodeGrabbed(t.Context(), r, ev); err != nil {
		t.Errorf("err: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("expected zero calls, got %v", r.calls)
	}
}

func TestHandleEpisodeGrabbed_BuildsGrabbedInputFromSubject(t *testing.T) {
	t.Parallel()
	r := &stubGrabbedRemover{}
	q := quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()}
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{
			Series:             &series.Series{ID: 42},
			MappedSeasonNumber: 3,
			Episodes: []episodes.Episode{
				{EpisodeNumber: 1},
				{EpisodeNumber: 2},
			},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{Quality: q},
		},
	}
	if err := pendingreleaseshandlers.HandleEpisodeGrabbed(t.Context(), r, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(r.calls))
	}
	in := r.calls[0]
	if in.SeriesID != 42 {
		t.Errorf("SeriesID = %d, want 42", in.SeriesID)
	}
	if in.SeasonNumber != 3 {
		t.Errorf("SeasonNumber = %d, want 3 (MappedSeasonNumber, not ParsedEpisodeInfo.SeasonNumber)", in.SeasonNumber)
	}
	if len(in.EpisodeNumbers) != 2 || in.EpisodeNumbers[0] != 1 || in.EpisodeNumbers[1] != 2 {
		t.Errorf("EpisodeNumbers = %v, want [1, 2]", in.EpisodeNumbers)
	}
	if in.Quality.Quality.ID != q.Quality.ID {
		t.Errorf("Quality = %+v, want HDTV720p", in.Quality)
	}
}

func TestHandleEpisodeGrabbed_NilParsedEpisodeInfoDegradesToZeroQuality(t *testing.T) {
	t.Parallel()
	r := &stubGrabbedRemover{}
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{
			Series:            &series.Series{ID: 1},
			Episodes:          []episodes.Episode{{EpisodeNumber: 1}},
			ParsedEpisodeInfo: nil, // defensive
		},
	}
	if err := pendingreleaseshandlers.HandleEpisodeGrabbed(t.Context(), r, ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %d", len(r.calls))
	}
	if r.calls[0].Quality.Quality.ID != 0 {
		t.Errorf("expected zero quality, got %+v", r.calls[0].Quality)
	}
}

func TestHandleEpisodeGrabbed_RemoverErrorPropagates(t *testing.T) {
	t.Parallel()
	r := &stubGrabbedRemover{err: errors.New("EIO")}
	ev := &decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{
			Series:   &series.Series{ID: 1},
			Episodes: []episodes.Episode{{EpisodeNumber: 1}},
		},
	}
	if err := pendingreleaseshandlers.HandleEpisodeGrabbed(t.Context(), r, ev); err == nil {
		t.Error("remover error should propagate")
	}
}
