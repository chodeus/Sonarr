package handlers_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
)

type recordingBlocklistClearer struct {
	calls int
	n     int64
	err   error
}

func (r *recordingBlocklistClearer) Clear(_ context.Context) (int64, error) {
	r.calls++
	return r.n, r.err
}

func TestClearBlocklist_NilClearer(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewClearBlocklist(nil, nil); err == nil {
		t.Fatal("expected error from nil clearer")
	}
}

func TestClearBlocklist_Dispatches(t *testing.T) {
	t.Parallel()
	c := &recordingBlocklistClearer{n: 3}
	h, _ := handlers.NewClearBlocklist(c, slog.Default())
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if c.calls != 1 {
		t.Errorf("Clear calls = %d, want 1", c.calls)
	}
}

func TestClearBlocklist_PropagatesError(t *testing.T) {
	t.Parallel()
	c := &recordingBlocklistClearer{err: errors.New("boom")}
	h, _ := handlers.NewClearBlocklist(c, slog.Default())
	if err := h.Execute(t.Context(), ""); err == nil {
		t.Fatal("expected error to propagate")
	}
}
