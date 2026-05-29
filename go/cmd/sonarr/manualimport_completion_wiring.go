// Boot-time adapter wiring the ManualImport completion arm (J450) to the
// event bus. Satisfies handlers.ManualImportCompletionPublisher by
// publishing the trackeddownloads.DownloadCompletedEvent (tracked
// imports) + trackeddownloads.UntrackedDownloadCompletedEvent (untracked
// imports) on the shared bus — the same DownloadCompletedEvent consumed
// by the already-subscribed downloadhistory handler (main.go:1108).
// Mirrors downloadCompletedPublisher (J449).
package main

import (
	"context"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// manualImportCompletionPublisher adapts *messaging.Bus to the narrow
// handlers.ManualImportCompletionPublisher consumer interface.
type manualImportCompletionPublisher struct {
	bus *messaging.Bus
}

func (p manualImportCompletionPublisher) PublishDownloadCompleted(ctx context.Context, ev trackeddownloads.DownloadCompletedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}

func (p manualImportCompletionPublisher) PublishUntrackedDownloadCompleted(ctx context.Context, ev trackeddownloads.UntrackedDownloadCompletedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}
