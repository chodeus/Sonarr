// Boot-time adapter wiring the download-completion arm of
// downloadprocessing.Service (J449) to the event bus. Satisfies
// downloadprocessing.CompletionPublisher by publishing the
// trackeddownloads.DownloadCompletedEvent on the shared bus — the same
// event consumed by the already-subscribed downloadhistory handler
// (HandleDownloadCompleted → DownloadImported audit row, main.go:1108).
// Mirrors downloadFailedPublisher (J400).
package main

import (
	"context"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// downloadCompletedPublisher adapts *messaging.Bus to the narrow
// downloadprocessing.CompletionPublisher consumer interface.
type downloadCompletedPublisher struct {
	bus *messaging.Bus
}

func (p downloadCompletedPublisher) PublishDownloadCompleted(ctx context.Context, ev trackeddownloads.DownloadCompletedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}

// downloadsProcessedPublisher adapts *messaging.Bus to the narrow
// downloadprocessing.ProcessedPublisher interface (J454) — publishes the
// end-of-pass DownloadsProcessedEvent consumed by the metadata writer's
// CreateAfterEpisodesImported batch.
type downloadsProcessedPublisher struct {
	bus *messaging.Bus
}

func (p downloadsProcessedPublisher) PublishDownloadsProcessed(ctx context.Context, ev trackeddownloads.DownloadsProcessedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}

// downloadCanBeRemovedPublisher adapts *messaging.Bus to the narrow
// downloadprocessing.CanBeRemovedPublisher interface (J458) — publishes
// the RemoveCompletedDownloads sweep's DownloadCanBeRemovedEvent consumed
// by the DownloadEventHub deferred-removal arm.
type downloadCanBeRemovedPublisher struct {
	bus *messaging.Bus
}

func (p downloadCanBeRemovedPublisher) PublishDownloadCanBeRemoved(ctx context.Context, ev trackeddownloads.DownloadCanBeRemovedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}
