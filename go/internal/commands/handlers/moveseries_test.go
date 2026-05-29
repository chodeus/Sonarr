package handlers_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
)

type recordingSeriesMover struct {
	moveCalls []moveCall
	bulkItems []handlers.BulkMoveSeriesItem
	bulkRoot  string
}

type moveCall struct {
	id           int
	source, dest string
}

func (r *recordingSeriesMover) MoveSeries(_ context.Context, id int, source, dest string) error {
	r.moveCalls = append(r.moveCalls, moveCall{id, source, dest})
	return nil
}

func (r *recordingSeriesMover) BulkMoveSeries(_ context.Context, items []handlers.BulkMoveSeriesItem, destRoot string) error {
	r.bulkItems = items
	r.bulkRoot = destRoot
	return nil
}

func TestMoveSeries_NilMover(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewMoveSeries(nil, nil); err == nil {
		t.Fatal("expected error from nil mover (MoveSeries)")
	}
	if _, err := handlers.NewBulkMoveSeries(nil, nil); err == nil {
		t.Fatal("expected error from nil mover (BulkMoveSeries)")
	}
}

func TestMoveSeries_Dispatches(t *testing.T) {
	t.Parallel()
	m := &recordingSeriesMover{}
	h, _ := handlers.NewMoveSeries(m, slog.Default())
	if err := h.Execute(t.Context(), `{"seriesId":5,"sourcePath":"/old/Show","destinationPath":"/new/Show"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(m.moveCalls) != 1 || m.moveCalls[0] != (moveCall{5, "/old/Show", "/new/Show"}) {
		t.Errorf("moveCalls = %+v", m.moveCalls)
	}
}

func TestMoveSeries_IncompleteBodyNoop(t *testing.T) {
	t.Parallel()
	m := &recordingSeriesMover{}
	h, _ := handlers.NewMoveSeries(m, slog.Default())
	for _, body := range []string{``, `{}`, `{"seriesId":5}`, `{"seriesId":5,"sourcePath":"/old"}`} {
		if err := h.Execute(t.Context(), body); err != nil {
			t.Fatalf("Execute(%q): %v", body, err)
		}
	}
	if len(m.moveCalls) != 0 {
		t.Errorf("no moves expected; got %+v", m.moveCalls)
	}
}

func TestBulkMoveSeries_Dispatches(t *testing.T) {
	t.Parallel()
	m := &recordingSeriesMover{}
	h, _ := handlers.NewBulkMoveSeries(m, slog.Default())
	body := `{"destinationRootFolder":"/new","series":[{"seriesId":1,"sourcePath":"/old/A"},{"seriesId":2,"sourcePath":"/old/B"}]}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if m.bulkRoot != "/new" || len(m.bulkItems) != 2 || m.bulkItems[1].SeriesID != 2 {
		t.Errorf("bulk root=%q items=%+v", m.bulkRoot, m.bulkItems)
	}
}

func TestBulkMoveSeries_IncompleteBodyNoop(t *testing.T) {
	t.Parallel()
	m := &recordingSeriesMover{}
	h, _ := handlers.NewBulkMoveSeries(m, slog.Default())
	for _, body := range []string{``, `{}`, `{"destinationRootFolder":"/new"}`, `{"series":[{"seriesId":1,"sourcePath":"/old"}]}`} {
		if err := h.Execute(t.Context(), body); err != nil {
			t.Fatalf("Execute(%q): %v", body, err)
		}
	}
	if m.bulkItems != nil {
		t.Errorf("no bulk move expected; got %+v", m.bulkItems)
	}
}
