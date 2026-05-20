package handlers_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
)

func TestCheckForFinishedDownloads_Stub_LogsAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewCheckForFinishedDownloads(nil, logger)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "checkforfinisheddownloads: stub Execute") {
		t.Errorf("expected stub log line; got: %q", logged)
	}
}

func TestCheckForFinishedDownloads_DelegatesAndLogsDeprecation(t *testing.T) {
	t.Parallel()
	r := &stubRefresher{items: 7}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h := handlers.NewCheckForFinishedDownloads(r, logger)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 {
		t.Errorf("Refresh calls: got %d, want 1", r.calls)
	}
	logged := buf.String()
	if !strings.Contains(logged, "deprecated command") {
		t.Errorf("expected deprecation warn; got: %q", logged)
	}
}

func TestCheckForFinishedDownloads_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewCheckForFinishedDownloads(nil, nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}
