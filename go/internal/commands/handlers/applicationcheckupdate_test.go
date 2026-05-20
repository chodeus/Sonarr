package handlers_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
)

type stubUpdateChecker struct {
	version string
	err     error
}

func (s stubUpdateChecker) AvailableVersion(_ context.Context) (string, error) {
	return s.version, s.err
}

func TestApplicationCheckUpdate_Stub(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewApplicationCheckUpdate(nil, logger)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "stub Execute") {
		t.Errorf("expected stub log; got: %q", buf.String())
	}
}

func TestApplicationCheckUpdate_UpdateAvailable(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewApplicationCheckUpdate(stubUpdateChecker{version: "5.0.99"}, logger)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "update available") {
		t.Errorf("expected update-available log; got: %q", logged)
	}
	if !strings.Contains(logged, "5.0.99") {
		t.Errorf("expected version in log; got: %q", logged)
	}
}

func TestApplicationCheckUpdate_NoUpdate(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewApplicationCheckUpdate(stubUpdateChecker{version: ""}, logger)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "no update available") {
		t.Errorf("expected no-update log; got: %q", buf.String())
	}
}

func TestApplicationCheckUpdate_FeedFailureAbsorbed(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h := handlers.NewApplicationCheckUpdate(stubUpdateChecker{err: errors.New("DNS down")}, logger)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute: feed failure should be absorbed; got %v", err)
	}
	if !strings.Contains(buf.String(), "feed lookup failed") {
		t.Errorf("expected feed-failure warn; got: %q", buf.String())
	}
}
