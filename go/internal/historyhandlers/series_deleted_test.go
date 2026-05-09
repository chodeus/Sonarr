package historyhandlers_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/historyhandlers"
	"sonarr.tv/sonarr/internal/series"
)

// stubSeriesDeleter implements [historyhandlers.SeriesHistoryDeleter].
// Captures the supplied ids + lets tests inject a return / error.
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
	if err := historyhandlers.HandleSeriesDeleted(t.Context(), nil, ev); err == nil {
		t.Errorf("expected error on nil deleter")
	}
}

func TestHandleSeriesDeleted_NoOpOnNilEvent(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{}
	if err := historyhandlers.HandleSeriesDeleted(t.Context(), d, nil); err != nil {
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
	if err := historyhandlers.HandleSeriesDeleted(t.Context(), d, ev); err != nil {
		t.Errorf("empty Series: %v", err)
	}
	if d.calls != 0 {
		t.Errorf("empty Series: deleter should not be called; calls=%d", d.calls)
	}
}

func TestHandleSeriesDeleted_PassesIDsThrough(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{affected: 12}
	ev := &series.SeriesDeletedEvent{
		Series: []series.Series{{ID: 7}, {ID: 14}, {ID: 21}},
	}
	if err := historyhandlers.HandleSeriesDeleted(t.Context(), d, ev); err != nil {
		t.Fatalf("HandleSeriesDeleted: %v", err)
	}
	if d.calls != 1 {
		t.Errorf("expected 1 deleter call, got %d", d.calls)
	}
	if got := d.gotIDs; len(got) != 3 || got[0] != 7 || got[1] != 14 || got[2] != 21 {
		t.Errorf("ids = %v, want [7 14 21]", got)
	}
}

func TestHandleSeriesDeleted_SingleSeriesPassesSingletonList(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{}
	ev := &series.SeriesDeletedEvent{Series: []series.Series{{ID: 99}}}
	if err := historyhandlers.HandleSeriesDeleted(t.Context(), d, ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	if got := d.gotIDs; len(got) != 1 || got[0] != 99 {
		t.Errorf("ids = %v, want [99]", got)
	}
}

func TestHandleSeriesDeleted_DeleterErrorPropagates(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{err: errors.New("repo down")}
	ev := &series.SeriesDeletedEvent{Series: []series.Series{{ID: 1}}}
	err := historyhandlers.HandleSeriesDeleted(t.Context(), d, ev)
	if err == nil {
		t.Fatal("expected error from deleter")
	}
	if !errors.Is(err, d.err) {
		t.Errorf("error chain missing deleter err: %v", err)
	}
}

func TestHandleSeriesDeleted_DiscardsAffectedCount(t *testing.T) {
	t.Parallel()
	d := &stubSeriesDeleter{affected: 999}
	ev := &series.SeriesDeletedEvent{Series: []series.Series{{ID: 1}}}
	// Handler returns nil regardless of affected count — there's no
	// "no rows touched is an error" semantic.
	if err := historyhandlers.HandleSeriesDeleted(t.Context(), d, ev); err != nil {
		t.Errorf("err: %v", err)
	}
}
