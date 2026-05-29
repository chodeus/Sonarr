// Boot-time adapter wiring the download-failure arm of
// downloadprocessing.Service (J400) to the event bus. Satisfies
// downloadprocessing.FailurePublisher by publishing the
// trackeddownloads.DownloadFailedEvent on the shared bus — the same
// event the queue-delete blocklist arm publishes (J396), consumed by
// the already-subscribed blocklist + history handlers.
package main

import (
	"context"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// downloadFailedPublisher adapts *messaging.Bus to the narrow
// downloadprocessing.FailurePublisher consumer interface.
type downloadFailedPublisher struct {
	bus *messaging.Bus
}

func (p downloadFailedPublisher) PublishDownloadFailed(ctx context.Context, ev trackeddownloads.DownloadFailedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}
