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

	"sonarr.tv/sonarr/internal/episodefiles"
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

// manualInteractionPublisher adapts *messaging.Bus to the narrow
// downloadprocessing.ManualInteractionPublisher interface (J519) —
// publishes the ManualInteractionRequiredEvent the import-blocked state
// machine fires, consumed by notifications.Service's async
// HandleManualInteractionRequiredEvent (J518 → OnManualInteractionRequired).
type manualInteractionPublisher struct {
	bus *messaging.Bus
}

func (p manualInteractionPublisher) PublishManualInteractionRequired(ctx context.Context, ev trackeddownloads.ManualInteractionRequiredEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}

// deleteCompletedBusPublisher adapts *messaging.Bus to the
// episodeFileDeleteAdapter's deleteCompletedPublisher (J522) —
// publishes episodefiles.DeleteCompletedEvent → notifications
// ProcessQueue drain.
type deleteCompletedBusPublisher struct {
	bus *messaging.Bus
}

func (p deleteCompletedBusPublisher) PublishDeleteCompleted(ctx context.Context, ev episodefiles.DeleteCompletedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}
