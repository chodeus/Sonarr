package handlers_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/mediafiles"
)

// trackingScanner records every Execute call so the body-parse tests
// can assert what command shape reached the scanner.
type trackingScanner struct {
	calls atomic.Int64
	last  mediafiles.RescanSeriesCommand
	err   error
}

func (s *trackingScanner) Execute(_ context.Context, cmd mediafiles.RescanSeriesCommand) error {
	s.calls.Add(1)
	s.last = cmd
	return s.err
}

// TestRescanSeries_NewWithNilScanner_Errors pins the constructor's
// nil-guard.
func TestRescanSeries_NewWithNilScanner_Errors(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewRescanSeries(nil, nil); err == nil {
		t.Errorf("NewRescanSeries(nil): expected error")
	}
}

// TestRescanSeries_EmptyBody pins that empty / "{}" body parses to
// nil-SeriesID (= scan all).
func TestRescanSeries_EmptyBody(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "{}", "  ", "  {}  "} {
		t.Run(body, func(t *testing.T) {
			scanner := &trackingScanner{}
			h, err := handlers.NewRescanSeries(scanner, slog.Default())
			if err != nil {
				t.Fatalf("NewRescanSeries: %v", err)
			}
			if err := h.Execute(t.Context(), body); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if scanner.calls.Load() != 1 {
				t.Errorf("scanner calls: %d want 1", scanner.calls.Load())
			}
			if scanner.last.SeriesID != nil {
				t.Errorf("SeriesID: got %v want nil", scanner.last.SeriesID)
			}
		})
	}
}

// TestRescanSeries_PopulatedBody pins that `{"seriesId": 42}` parses
// to a populated *int.
func TestRescanSeries_PopulatedBody(t *testing.T) {
	t.Parallel()
	scanner := &trackingScanner{}
	h, _ := handlers.NewRescanSeries(scanner, slog.Default())
	if err := h.Execute(t.Context(), `{"seriesId":42}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if scanner.last.SeriesID == nil || *scanner.last.SeriesID != 42 {
		t.Errorf("SeriesID: got %v want *42", scanner.last.SeriesID)
	}
}

// TestRescanSeries_GarbageBodyErrors pins that a body parse failure
// surfaces an error rather than silently scanning everything — a
// programming bug in the API caller shouldn't trigger a full rescan.
func TestRescanSeries_GarbageBodyErrors(t *testing.T) {
	t.Parallel()
	scanner := &trackingScanner{}
	h, _ := handlers.NewRescanSeries(scanner, slog.Default())
	err := h.Execute(t.Context(), "not json")
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if scanner.calls.Load() != 0 {
		t.Errorf("scanner should not be called on parse error; got %d calls", scanner.calls.Load())
	}
}

// TestRescanSeries_PropagatesScannerError pins error pass-through.
func TestRescanSeries_PropagatesScannerError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("scan boom")
	scanner := &trackingScanner{err: wantErr}
	h, _ := handlers.NewRescanSeries(scanner, slog.Default())
	if err := h.Execute(t.Context(), "{}"); !errors.Is(err, wantErr) {
		t.Errorf("err: got %v want %v", err, wantErr)
	}
}

// TestRescanSeries_RegisteredInRegistry pins that RegisterAll wires
// the handler under the canonical Commands.Name.
func TestRescanSeries_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, checkHealthFixedTime).svc
	deps := allDeps(t, cmdSvc)
	deps.RescanSeries = &trackingScanner{}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.RescanSeriesName); !ok {
		t.Errorf("RescanSeries not registered under %q", handlers.RescanSeriesName)
	}
}

// TestRescanSeries_NotInDefaultScheduledTasks pins the C# behaviour
// at TaskManager.cs:67-135 — RescanSeries fires only from API or as
// a follow-on to RefreshSeries; never on a fixed schedule. Drift
// here would silently put a periodic full-library rescan on the
// default cron.
func TestRescanSeries_NotInDefaultScheduledTasks(t *testing.T) {
	t.Parallel()
	for _, task := range handlers.DefaultScheduledTasks() {
		if task.TypeName == handlers.RescanSeriesName {
			t.Fatalf("RescanSeries appears in DefaultScheduledTasks (Interval=%d) — should be on-demand only", task.Interval)
		}
	}
}

// TestRescanSeries_Name pins the constant.
func TestRescanSeries_Name(t *testing.T) {
	t.Parallel()
	if handlers.RescanSeriesName != "RescanSeries" {
		t.Errorf("RescanSeriesName: %q want RescanSeries", handlers.RescanSeriesName)
	}
}
