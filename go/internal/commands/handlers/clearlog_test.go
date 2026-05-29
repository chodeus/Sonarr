package handlers_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
)

type recordingLogClearer struct {
	calls int
	n     int64
	err   error
}

func (r *recordingLogClearer) Purge(_ context.Context) (int64, error) {
	r.calls++
	return r.n, r.err
}

func TestClearLog_NilClearer(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewClearLog(nil, nil); err == nil {
		t.Fatal("expected error from nil clearer")
	}
}

func TestClearLog_Dispatches(t *testing.T) {
	t.Parallel()
	c := &recordingLogClearer{n: 7}
	h, _ := handlers.NewClearLog(c, slog.Default())
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if c.calls != 1 {
		t.Errorf("Purge calls = %d, want 1", c.calls)
	}
}

func TestClearLog_PropagatesError(t *testing.T) {
	t.Parallel()
	c := &recordingLogClearer{err: errors.New("boom")}
	h, _ := handlers.NewClearLog(c, slog.Default())
	if err := h.Execute(t.Context(), ""); err == nil {
		t.Fatal("expected error to propagate")
	}
}
