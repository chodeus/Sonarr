package main

import (
	"context"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/organizer"
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
	svc       *series.Service
	namingCfg *namingconfig.Service
	validator *series.AddValidator // optional; nil skips pre-insert validation
	log       *slog.Logger
	now       func() time.Time // overridable for deterministic tests
}

// folderNameForSeries loads the SeriesFolderFormat from
// namingconfig.Service and runs the result through
// organizer.GetSeriesFolder — bundles the C# `IBuildFileNames.
// GetSeriesFolder(newSeries)` call site at
// `AddSeriesService.cs:146`. Returns empty when the config load
// fails or the format produces an empty literal; the caller
// (series.PrepareForAdd) treats both as "don't compute a Path"
// and leaves Series.Path empty — matches the C# `Path.IsNullOrWhiteSpace`
// guard at `:144` letting the row persist without a folder.
//
// Errors from GetConfig degrade to a warn log + empty return; the
// add-series batch keeps running with whatever folder names later
// rows produce. Pinning the namingconfig getter inline avoids a
// per-row round-trip cost in the typical case (the *Service
// caches its row internally — repeat GetConfig calls hit the
// cache, no DB round-trip per row).
func (a seriesAdderAdapter) folderNameForSeries(ctx context.Context) func(*series.Series) string {
	cfg, err := a.namingCfg.GetConfig(ctx)
	if err != nil || cfg == nil {
		if err != nil {
			a.log.LogAttrs(ctx, slog.LevelWarn,
				"importlistsync: AddSeries: namingconfig.GetConfig failed (Path will stay empty for this batch)",
				slog.Any("err", err))
		}
		return nil
	}
	opts := organizer.FolderOptions{
		SeriesFolderFormat:   cfg.SeriesFolderFormat,
		SeasonFolderFormat:   cfg.SeasonFolderFormat,
		SpecialsFolderFormat: cfg.SpecialsFolderFormat,
		Clean:                organizer.CleanNameOptions{ReplaceIllegalCharacters: true},
	}
	return func(s *series.Series) string {
		return organizer.GetSeriesFolder(s, opts)
	}
}

// AddSeries loops *series.Service.AddSeries per row, running
// [series.PrepareForAdd] beforehand to set CleanTitle / SortTitle /
// Added / Path (mirrors C# `AddSeriesService.SetPropertiesAndValidate`
// at `:142-167`). Per-row failures absorbed via warn-log-and-continue
// (matches C# `ignoreErrors=true` at `ImportListSyncService.cs:261`).
// Each successful add publishes SeriesAddedEvent on the bus —
// downstream subscribers (J185 folder creation, future refresh
// pipeline) react per row.
//
// The folder-name builder is constructed ONCE per batch by
// folderNameForSeries — repeated namingconfig.GetConfig calls
// would hit the cache, but pre-resolving avoids per-row indirection.
func (a seriesAdderAdapter) AddSeries(ctx context.Context, batch []series.Series) error {
	folderName := a.folderNameForSeries(ctx)
	nowFn := a.now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	for i := range batch {
		row := batch[i]
		series.PrepareForAdd(&row, nowFn(), folderName)
		if a.validator != nil {
			if err := a.validator.Validate(ctx, &row); err != nil {
				// Matches C# `AddSeriesService.AddSeries(List<Series>,
				// ignoreErrors: true)` posture: log + skip rather than
				// abort the batch. Mirrors the catch(ValidationException)
				// arm at `AddSeriesService.cs:97-107`.
				a.log.LogAttrs(ctx, slog.LevelWarn,
					"importlistsync: AddSeries: pre-insert validation failed (row skipped)",
					slog.Int("tvdbId", row.TvdbID),
					slog.String("title", row.Title),
					slog.String("path", row.Path),
					slog.String("titleSlug", row.TitleSlug),
					slog.Any("err", err))
				continue
			}
		}
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
