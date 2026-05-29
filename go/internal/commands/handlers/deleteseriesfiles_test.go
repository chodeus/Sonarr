package handlers_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
)

type recordingSeriesFilesDeleter struct {
	calls  int
	gotIDs []int
	retErr error
}

func (r *recordingSeriesFilesDeleter) DeleteSeriesFiles(_ context.Context, ids []int) error {
	r.calls++
	r.gotIDs = ids
	return r.retErr
}

func TestDeleteSeriesFiles_NilDeleter(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewDeleteSeriesFiles(nil, nil); err == nil {
		t.Fatal("expected error from nil deleter")
	}
}

func TestDeleteSeriesFiles_DispatchesIDs(t *testing.T) {
	t.Parallel()
	d := &recordingSeriesFilesDeleter{}
	h, _ := handlers.NewDeleteSeriesFiles(d, slog.Default())
	if err := h.Execute(t.Context(), `{"name":"DeleteSeriesFiles","seriesIds":[3,7]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if d.calls != 1 || len(d.gotIDs) != 2 || d.gotIDs[0] != 3 || d.gotIDs[1] != 7 {
		t.Errorf("deleter got calls=%d ids=%v, want 1 [3 7]", d.calls, d.gotIDs)
	}
}

func TestDeleteSeriesFiles_EmptyIDs_NoCall(t *testing.T) {
	t.Parallel()
	d := &recordingSeriesFilesDeleter{}
	h, _ := handlers.NewDeleteSeriesFiles(d, slog.Default())
	for _, body := range []string{``, `{}`, `{"seriesIds":[]}`} {
		if err := h.Execute(t.Context(), body); err != nil {
			t.Fatalf("Execute(%q): %v", body, err)
		}
	}
	if d.calls != 0 {
		t.Errorf("deleter should not be called for empty ids; calls=%d", d.calls)
	}
}

func TestDeleteSeriesFiles_MalformedBody(t *testing.T) {
	t.Parallel()
	d := &recordingSeriesFilesDeleter{}
	h, _ := handlers.NewDeleteSeriesFiles(d, slog.Default())
	if err := h.Execute(t.Context(), `not-json`); err == nil {
		t.Fatal("expected parse error")
	}
}
