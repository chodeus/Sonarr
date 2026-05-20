package downloadhistoryhandlers_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/downloadhistoryhandlers"
	"sonarr.tv/sonarr/internal/series"
)

// stubDeleter records every DeleteForSeries call.
type stubDeleter struct {
	got     []int
	err     error
	count   int64
	callCnt int
}

func (s *stubDeleter) DeleteForSeries(_ context.Context, ids []int) (int64, error) {
	s.callCnt++
	if s.err != nil {
		return 0, s.err
	}
	s.got = append([]int(nil), ids...)
	return s.count, nil
}

func TestHandleSeriesDeleted_NilDeleter(t *testing.T) {
	t.Parallel()
	if err := downloadhistoryhandlers.HandleSeriesDeleted(t.Context(), nil,
		&series.SeriesDeletedEvent{Series: []series.Series{{ID: 1}}}); err == nil {
		t.Errorf("expected error on nil deleter")
	}
}

func TestHandleSeriesDeleted_NilEventIsNoOp(t *testing.T) {
	t.Parallel()
	d := &stubDeleter{}
	if err := downloadhistoryhandlers.HandleSeriesDeleted(t.Context(), d, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if d.callCnt != 0 {
		t.Errorf("deleter should not be called; got %d", d.callCnt)
	}
}

func TestHandleSeriesDeleted_EmptySeriesIsNoOp(t *testing.T) {
	t.Parallel()
	d := &stubDeleter{}
	ev := &series.SeriesDeletedEvent{Series: nil}
	if err := downloadhistoryhandlers.HandleSeriesDeleted(t.Context(), d, ev); err != nil {
		t.Errorf("empty Series: %v", err)
	}
	if d.callCnt != 0 {
		t.Errorf("deleter should not be called for empty Series; got %d", d.callCnt)
	}
}

func TestHandleSeriesDeleted_PassesIDsThrough(t *testing.T) {
	t.Parallel()
	d := &stubDeleter{count: 5}
	ev := &series.SeriesDeletedEvent{Series: []series.Series{{ID: 4}, {ID: 9}, {ID: 16}}}
	if err := downloadhistoryhandlers.HandleSeriesDeleted(t.Context(), d, ev); err != nil {
		t.Fatalf("HandleSeriesDeleted: %v", err)
	}
	if len(d.got) != 3 || d.got[0] != 4 || d.got[1] != 9 || d.got[2] != 16 {
		t.Errorf("deleter got %v, want [4 9 16]", d.got)
	}
}

func TestHandleSeriesDeleted_DeleterErrorPropagates(t *testing.T) {
	t.Parallel()
	d := &stubDeleter{err: errors.New("repo down")}
	ev := &series.SeriesDeletedEvent{Series: []series.Series{{ID: 1}}}
	err := downloadhistoryhandlers.HandleSeriesDeleted(t.Context(), d, ev)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, d.err) {
		t.Errorf("error chain missing source: %v", err)
	}
}

// Compile-time assertion that *downloadhistory.Service satisfies the
// SeriesHistoryDeleter interface.
var _ downloadhistoryhandlers.SeriesHistoryDeleter = (*downloadhistory.Service)(nil)
