package handlers_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
)

type recordingProcessor struct{ calls int }

func (r *recordingProcessor) ProcessTrackedDownloads(context.Context) int { r.calls++; return 0 }

func TestProcessMonitoredDownloads_NilProcessor(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewProcessMonitoredDownloads(nil, nil); err == nil {
		t.Fatal("expected error from nil processor")
	}
}

func TestProcessMonitoredDownloads_Dispatches(t *testing.T) {
	t.Parallel()
	p := &recordingProcessor{}
	h, _ := handlers.NewProcessMonitoredDownloads(p, slog.Default())
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if p.calls != 1 {
		t.Errorf("processor calls = %d, want 1", p.calls)
	}
}
