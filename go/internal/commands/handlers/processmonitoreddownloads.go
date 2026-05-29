// ProcessMonitoredDownloads handler. Ports
// `DownloadProcessingService.Execute(ProcessMonitoredDownloadsCommand)`
// (`src/NzbDrone.Core/Download/DownloadProcessingService.cs`): auto-import
// every completed tracked download. The orchestration lives in
// downloadprocessing.Service; this handler dispatches into it.

package handlers

import (
	"context"
	"errors"
	"log/slog"
)

// ProcessMonitoredDownloadsName is the registry key + Commands.Name
// (C# `ProcessMonitoredDownloadsCommand` → `ProcessMonitoredDownloads`).
const ProcessMonitoredDownloadsName = "ProcessMonitoredDownloads"

// MonitoredDownloadProcessor auto-imports completed tracked downloads.
// *downloadprocessing.Service satisfies it via ProcessTrackedDownloads.
type MonitoredDownloadProcessor interface {
	ProcessTrackedDownloads(ctx context.Context) int
}

// ProcessMonitoredDownloads is the IExecute<ProcessMonitoredDownloadsCommand>
// port.
type ProcessMonitoredDownloads struct {
	processor MonitoredDownloadProcessor
	log       *slog.Logger
}

// NewProcessMonitoredDownloads builds the handler. processor MUST be non-nil.
func NewProcessMonitoredDownloads(processor MonitoredDownloadProcessor, log *slog.Logger) (*ProcessMonitoredDownloads, error) {
	if processor == nil {
		return nil, errors.New("handlers: NewProcessMonitoredDownloads: nil MonitoredDownloadProcessor")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ProcessMonitoredDownloads{processor: processor, log: log}, nil
}

// Execute runs the auto-import pass. The command body is ignored.
func (h *ProcessMonitoredDownloads) Execute(ctx context.Context, _ string) error {
	h.processor.ProcessTrackedDownloads(ctx)
	return nil
}
