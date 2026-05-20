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

var refreshMonitoredDownloadsFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func TestRefreshMonitoredDownloads_Execute_LogsAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewRefreshMonitoredDownloads(nil, logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "refreshmonitoreddownloads: stub Execute") {
		t.Errorf("expected stub log line; got: %q", logged)
	}
	if !strings.Contains(logged, "command=RefreshMonitoredDownloads") {
		t.Errorf("expected command=RefreshMonitoredDownloads attr; got: %q", logged)
	}
}

func TestRefreshMonitoredDownloads_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewRefreshMonitoredDownloads(nil, nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}

// stubRefresher implements handlers.Refresher with a configurable
// item count + error.
type stubRefresher struct {
	items int
	err   error
	calls int
}

func (s *stubRefresher) Refresh(_ context.Context) (int, error) {
	s.calls++
	return s.items, s.err
}

func TestRefreshMonitoredDownloads_DelegatesToRefresher(t *testing.T) {
	t.Parallel()
	r := &stubRefresher{items: 5}
	h := handlers.NewRefreshMonitoredDownloads(r, slog.Default())
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 {
		t.Errorf("Refresh calls: got %d, want 1", r.calls)
	}
}

func TestRefreshMonitoredDownloads_PartialErrorIsAbsorbed(t *testing.T) {
	t.Parallel()
	r := &stubRefresher{items: 2, err: errors.New("client X down")}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewRefreshMonitoredDownloads(r, logger)
	// Per-client poll failures absorbed into a warn log; Execute
	// returns nil so the scheduler doesn't classify the command as
	// failed.
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute: got %v, want nil (partial errors absorbed)", err)
	}
	if !strings.Contains(buf.String(), "Refresh completed with errors") {
		t.Errorf("expected partial-error warn log; got: %q", buf.String())
	}
}

func TestRefreshMonitoredDownloads_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, refreshMonitoredDownloadsFixedTime).svc
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
	if _, ok := registry.Lookup(handlers.RefreshMonitoredDownloadsName); !ok {
		t.Errorf("RefreshMonitoredDownloads not registered under name %q", handlers.RefreshMonitoredDownloadsName)
	}
}

func TestRefreshMonitoredDownloads_DefaultScheduledTasks_Has1MinInterval(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.RefreshMonitoredDownloadsName {
			if task.Interval != 1 {
				t.Errorf("RefreshMonitoredDownloads Interval: %d minutes, want 1", task.Interval)
			}
			return
		}
	}
	t.Errorf("RefreshMonitoredDownloads not in DefaultScheduledTasks")
}
