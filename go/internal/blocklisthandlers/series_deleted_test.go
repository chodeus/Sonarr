package blocklisthandlers_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/blocklisthandlers"
	"sonarr.tv/sonarr/internal/series"
)

type stubSeriesDeleter struct {
	gotIDs   []int
	affected int64
	err      error
	calls    int
}

func (s *stubSeriesDeleter) DeleteForSeries(_ context.Context, ids []int) (int64, error) {
	s.calls++
	s.gotIDs = append([]int(nil), ids...)
	if s.err != nil {
		return 0, s.err
	}
	return s.affected, nil
}

func TestHandleSeriesDeleted_NilDeleter(t *testing.T) {
	t.Parallel()
	ev := &series.SeriesDeletedEvent{Series: []series.Series{{ID: 1}}}
	if err := blocklisthandlers.HandleSeriesDeleted(t.Context(), nil, ev); err == nil {
		t.Errorf("expected error on nil deleter")
	}
}

func TestHandleSeriesDeleted_NoOpOnNilEvent(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{}
	if err := blocklisthandlers.HandleSeriesDeleted(t.Context(), d, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if d.calls != 0 {
		t.Errorf("nil event: deleter should not be called; calls=%d", d.calls)
	}
}

func TestHandleSeriesDeleted_NoOpOnEmptySeries(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{}
	ev := &series.SeriesDeletedEvent{Series: nil}
	if err := blocklisthandlers.HandleSeriesDeleted(t.Context(), d, ev); err != nil {
		t.Errorf("empty Series: %v", err)
	}
	if d.calls != 0 {
		t.Errorf("empty Series: deleter should not be called")
	}
}

func TestHandleSeriesDeleted_PassesIDsThrough(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{affected: 4}
	ev := &series.SeriesDeletedEvent{
		Series: []series.Series{{ID: 5}, {ID: 9}, {ID: 13}},
	}
	if err := blocklisthandlers.HandleSeriesDeleted(t.Context(), d, ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.calls != 1 {
		t.Errorf("expected 1 deleter call, got %d", d.calls)
	}
	if got := d.gotIDs; len(got) != 3 || got[0] != 5 || got[1] != 9 || got[2] != 13 {
		t.Errorf("ids = %v, want [5 9 13]", got)
	}
}

func TestHandleSeriesDeleted_DeleterErrorPropagates(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{err: errors.New("repo down")}
	ev := &series.SeriesDeletedEvent{Series: []series.Series{{ID: 1}}}
	err := blocklisthandlers.HandleSeriesDeleted(t.Context(), d, ev)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, d.err) {
		t.Errorf("error chain missing deleter err: %v", err)
	}
}
