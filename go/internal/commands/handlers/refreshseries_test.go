package handlers_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
)

var refreshSeriesFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

// stubRefreshRunner captures the per-id Refresh calls + RefreshAll
// invocations so tests can assert the handler dispatches correctly.
type stubRefreshRunner struct {
	got        []int
	err        error
	refreshAll int // count of RefreshAll calls
	allErr     error
}

func (s *stubRefreshRunner) Refresh(_ context.Context, id int) error {
	s.got = append(s.got, id)
	return s.err
}

func (s *stubRefreshRunner) RefreshAll(_ context.Context) error {
	s.refreshAll++
	return s.allErr
}

func TestRefreshSeries_NilRunner_FallsBackToStubLog(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewRefreshSeries(nil, logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "stub Execute (RefreshSeriesRunner not wired)") {
		t.Errorf("expected stub-fallback log; got: %q", buf.String())
	}
}

func TestRefreshSeries_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewRefreshSeries(nil, nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}

func TestRefreshSeries_DispatchesPerID(t *testing.T) {
	t.Parallel()
	runner := &stubRefreshRunner{}
	h := handlers.NewRefreshSeries(runner, slog.Default())
	if err := h.Execute(t.Context(), `{"seriesIds":[1,2,3]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(runner.got) != 3 || runner.got[0] != 1 || runner.got[1] != 2 || runner.got[2] != 3 {
		t.Errorf("runner calls: got %v, want [1 2 3]", runner.got)
	}
}

func TestRefreshSeries_PerIDErrorAbsorbed(t *testing.T) {
	t.Parallel()
	runner := &stubRefreshRunner{err: errors.New("not found")}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h := handlers.NewRefreshSeries(runner, logger)
	if err := h.Execute(t.Context(), `{"seriesIds":[1,2]}`); err != nil {
		t.Fatalf("Execute should absorb per-id errors: %v", err)
	}
	if len(runner.got) != 2 {
		t.Errorf("batch should continue after error: got %d calls", len(runner.got))
	}
	if !strings.Contains(buf.String(), "per-id refresh failed") {
		t.Errorf("expected per-id failure warn log; got %q", buf.String())
	}
}

// J193 — empty body now fires RefreshAll (the all-series sweep) instead
// of a stub-log no-op. The 12h scheduled-task tick pushes RefreshSeries
// with no body, so this is the path the periodic refresh actually
// exercises.
func TestRefreshSeries_EmptyBody_FiresRefreshAll(t *testing.T) {
	t.Parallel()
	runner := &stubRefreshRunner{}
	h := handlers.NewRefreshSeries(runner, slog.Default())
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.refreshAll != 1 {
		t.Errorf("RefreshAll call count: got %d, want 1", runner.refreshAll)
	}
	if len(runner.got) != 0 {
		t.Errorf("per-id Refresh should not be called on empty body; got %d", len(runner.got))
	}
}

// J193 — empty seriesIds list also fires RefreshAll, matching C#
// Execute :249-282 where SeriesIds.Any() == false jumps to the
// all-series branch.
func TestRefreshSeries_EmptySeriesIDs_FiresRefreshAll(t *testing.T) {
	t.Parallel()
	runner := &stubRefreshRunner{}
	h := handlers.NewRefreshSeries(runner, slog.Default())
	if err := h.Execute(t.Context(), `{"seriesIds":[]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.refreshAll != 1 {
		t.Errorf("RefreshAll call count: got %d, want 1", runner.refreshAll)
	}
}

func TestRefreshSeries_MalformedBody_DegradesSilently(t *testing.T) {
	t.Parallel()
	runner := &stubRefreshRunner{}
	h := handlers.NewRefreshSeries(runner, slog.Default())
	if err := h.Execute(t.Context(), `{not json`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(runner.got) != 0 {
		t.Errorf("malformed body should not call runner; got %d calls", len(runner.got))
	}
}

func TestRefreshSeries_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, refreshSeriesFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		HealthCheck:  emptyHealthSvc(t),
		RescanSeries: noopRescanScanner{},
		Log:          slog.Default(),
	}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.RefreshSeriesName); !ok {
		t.Errorf("RefreshSeries not registered under name %q", handlers.RefreshSeriesName)
	}
}

func TestRefreshSeries_DefaultScheduledTasks_Has12HourInterval(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.RefreshSeriesName {
			if task.Interval != 12*60 {
				t.Errorf("RefreshSeries Interval: %d minutes, want %d (12h)", task.Interval, 12*60)
			}
			return
		}
	}
	t.Errorf("RefreshSeries not in DefaultScheduledTasks")
}
