package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/series"
)

// seriesLookupAdapter satisfies the importlists.SyncService
// `seriesLookup` narrow interface (AllTvdbIDs) via the existing
// [*series.Service.AllSeriesTvdbIDs] method. Used by slice 5.J183
// to wire the SyncService without polluting series.Service with a
// short-name alias.
type seriesLookupAdapter struct {
	svc *series.Service
}

// AllTvdbIDs forwards to AllSeriesTvdbIDs. Names diverge because the
// SyncService interface defines the shorter `AllTvdbIDs` while
// series.Service uses the longer C#-parity name `AllSeriesTvdbIDs`.
func (a seriesLookupAdapter) AllTvdbIDs(ctx context.Context) ([]int, error) {
	return a.svc.AllSeriesTvdbIDs(ctx)
}

// seriesAdderAdapter satisfies the importlists.SyncService
// `seriesAdder` narrow interface (AddSeries batch) by looping
// [*series.Service.AddSeries] per row. Per-row failures are absorbed
// with a warn log — matches the C# `IAddSeriesService.AddSeries
// (seriesToAdd, ignoreErrors: true)` posture at
// `ImportListSyncService.cs:261`.
//
// Slice 5.J185 upgraded the per-row delegation from
// `*series.Service.Insert` (a pure repo pass-through with no event
// publish) to `*series.Service.AddSeries` (which re-fetches the
// inserted row + publishes `series.SeriesAddedEvent` on the bus).
// This activates the SeriesAddedEvent-driven folder-creation
// subscriber wired in `cmd/sonarr/seriesadded_folder_subscriber.go`
// — the first piece of the C# AddSeriesService side-effect pipeline
// the Go-port mirrors (folder creation when `CreateEmptySeriesFolders
// = true` + `DeleteEmptyFolders = false`). The remaining
// side-effects from the C# pipeline (TVDB lookup-then-merge, image
// download, season-pass seeding, ImportListExclusion clearing) stay
// deferred until the matching subsystem ports (SkyHook M3, image
// cache, episode seeding) land — but the event plumbing is now
// in place so each follow-up just adds its own subscriber.
type seriesAdderAdapter struct {
	svc *series.Service
	log *slog.Logger
}

// AddSeries loops *series.Service.AddSeries per row, logging
// per-row failures at warn level + continuing the batch. Returns
// nil unless the batch is nil/empty (which short-circuits with
// nil). Each successful add publishes a SeriesAddedEvent on the
// bus — downstream subscribers (folder creation, future refresh
// pipeline) react per row.
func (a seriesAdderAdapter) AddSeries(ctx context.Context, batch []series.Series) error {
	for i := range batch {
		row := batch[i]
		if _, err := a.svc.AddSeries(ctx, &row); err != nil {
			a.log.LogAttrs(ctx, slog.LevelWarn,
				"importlistsync: AddSeries: per-row AddSeries failed (batch continues)",
				slog.Int("tvdbId", row.TvdbID),
				slog.String("title", row.Title),
				slog.Any("err", err))
		}
	}
	return nil
}
