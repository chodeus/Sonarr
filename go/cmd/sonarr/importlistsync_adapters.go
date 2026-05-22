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
// [*series.Service.Insert] per row. Per-row failures are absorbed
// with a warn log — matches the C# `IAddSeriesService.AddSeries
// (seriesToAdd, ignoreErrors: true)` posture at
// `ImportListSyncService.cs:261`.
//
// IMPORTANT: this adapter is a thin per-row Insert loop. The richer
// C# AddSeriesService pipeline (TVDB lookup-then-merge, folder
// creation via DiskProvider, season-pass seeding, image download,
// ImportListExclusion clearing) is NOT replicated — same gap as
// series.Service.Insert's doc-comment flags. The AddSeriesService
// port slice will replace this adapter with a delegation to the
// full pipeline once that lands.
type seriesAdderAdapter struct {
	svc *series.Service
	log *slog.Logger
}

// AddSeries loops Insert per row, logging per-row failures at warn
// level + continuing the batch. Returns nil unless the batch is
// nil/empty (which short-circuits with nil).
func (a seriesAdderAdapter) AddSeries(ctx context.Context, batch []series.Series) error {
	for i := range batch {
		row := batch[i]
		if _, err := a.svc.Insert(ctx, &row); err != nil {
			a.log.LogAttrs(ctx, slog.LevelWarn,
				"importlistsync: AddSeries: per-row Insert failed (batch continues)",
				slog.Int("tvdbId", row.TvdbID),
				slog.String("title", row.Title),
				slog.Any("err", err))
		}
	}
	return nil
}
