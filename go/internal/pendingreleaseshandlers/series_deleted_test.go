package pendingreleaseshandlers_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/pendingreleaseshandlers"
	"sonarr.tv/sonarr/internal/series"
)

type stubSeriesDeleter struct {
	calls [][]int
	err   error
}

func (s *stubSeriesDeleter) DeleteBySeriesIDs(_ context.Context, ids []int) error {
	s.calls = append(s.calls, append([]int(nil), ids...))
	return s.err
}

func TestHandleSeriesDeleted_NilDeleterErrors(t *testing.T) {
	t.Parallel()
	if err := pendingreleaseshandlers.HandleSeriesDeleted(t.Context(), nil, &series.SeriesDeletedEvent{}); err == nil {
		t.Error("nil deleter should error")
	}
}

func TestHandleSeriesDeleted_NilEventIsNoop(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{}
	if err := pendingreleaseshandlers.HandleSeriesDeleted(t.Context(), d, nil); err != nil {
		t.Errorf("nil event err: %v", err)
	}
	if len(d.calls) != 0 {
		t.Errorf("expected zero calls, got %v", d.calls)
	}
}

func TestHandleSeriesDeleted_EmptySeriesIsNoop(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{}
	if err := pendingreleaseshandlers.HandleSeriesDeleted(t.Context(), d, &series.SeriesDeletedEvent{}); err != nil {
		t.Errorf("empty series err: %v", err)
	}
	if len(d.calls) != 0 {
		t.Errorf("expected zero calls, got %v", d.calls)
	}
}

func TestHandleSeriesDeleted_BulkDispatchSingleCall(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{}
	ev := &series.SeriesDeletedEvent{
		Series: []series.Series{
			{ID: 7, Title: "A"},
			{ID: 11, Title: "B"},
			{ID: 23, Title: "C"},
		},
	}
	if err := pendingreleaseshandlers.HandleSeriesDeleted(t.Context(), d, ev); err != nil {
		t.Fatalf("HandleSeriesDeleted: %v", err)
	}
	if len(d.calls) != 1 {
		t.Fatalf("calls = %d, want 1 bulk", len(d.calls))
	}
	want := []int{7, 11, 23}
	for i, id := range d.calls[0] {
		if id != want[i] {
			t.Errorf("call[0][%d] = %d, want %d", i, id, want[i])
		}
	}
}

func TestHandleSeriesDeleted_DeleterErrorPropagates(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{err: errors.New("EIO")}
	ev := &series.SeriesDeletedEvent{Series: []series.Series{{ID: 1}}}
	if err := pendingreleaseshandlers.HandleSeriesDeleted(t.Context(), d, ev); err == nil {
		t.Error("deleter error should propagate")
	}
}
