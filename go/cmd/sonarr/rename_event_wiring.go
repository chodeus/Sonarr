// Boot-time adapter wiring the rename-path event publication (J453) to
// the event bus. Satisfies handlers.RenameEventPublisher by publishing
// the per-file episodefiles.EpisodeFileRenamedEvent (consumed by the
// historyhandlers "renamed" row, main.go:1056) and the per-series
// episodefiles.SeriesRenamedEvent (consumed by the metadata writer's
// MoveFilesAfterRename, metadata_subscriptions.go) on the shared bus.
package main

import (
	"context"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/messaging"
)

// renameEventPublisher adapts *messaging.Bus to the narrow
// handlers.RenameEventPublisher consumer interface.
type renameEventPublisher struct {
	bus *messaging.Bus
}

func (p renameEventPublisher) PublishEpisodeFileRenamed(ctx context.Context, ev episodefiles.EpisodeFileRenamedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}

func (p renameEventPublisher) PublishSeriesRenamed(ctx context.Context, ev episodefiles.SeriesRenamedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}

// PublishRenameCompleted publishes the once-per-command
// episodefiles.RenameCompletedEvent (J521) → notifications ProcessQueue drain.
func (p renameEventPublisher) PublishRenameCompleted(ctx context.Context, ev episodefiles.RenameCompletedEvent) error {
	return messaging.Publish(ctx, p.bus, ev)
}
