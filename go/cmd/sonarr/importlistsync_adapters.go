package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/importlistitems"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/metadatasource"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/series"
)

// importListItemPersister satisfies the importlists.SyncService
// `itemPersister` narrow interface (queue [I4c]). It maps the sync
// orchestrator's transient [importlists.ItemInfo] candidates onto
// persisted [importlistitems.Item] rows and delegates the per-list
// insert/update/delete diff to [*importlistitems.Service.SyncSeriesForList].
//
// The mapping mirrors C# `ImportListItemInfo`: the nullable external
// ids (Year/TmdbId/ImdbId/MalId/AniListId) and ReleaseDate are stored
// as NULL when the provider left them unset (zero / "" / zero-time),
// matching the Go importlistitems.Item *pointer* model.
type importListItemPersister struct {
	svc *importlistitems.Service
}

// SyncSeriesForList maps the fetched candidate set and reconciles it.
func (a importListItemPersister) SyncSeriesForList(ctx context.Context, items []importlists.ItemInfo, listID int) (int, error) {
	rows := make([]importlistitems.Item, 0, len(items))
	for i := range items {
		it := items[i]
		row := importlistitems.Item{
			ImportListID: listID,
			Title:        it.Title,
			TvdbID:       it.TvdbID,
		}
		if it.Year != 0 {
			y := it.Year
			row.Year = &y
		}
		if it.TmdbID != 0 {
			v := it.TmdbID
			row.TmdbID = &v
		}
		if it.ImdbID != "" {
			v := it.ImdbID
			row.ImdbID = &v
		}
		if it.MalID != 0 {
			v := it.MalID
			row.MalID = &v
		}
		if it.AniListID != 0 {
			v := it.AniListID
			row.AniListID = &v
		}
		if !it.ReleaseDate.IsZero() {
			v := it.ReleaseDate
			row.ReleaseDate = &v
		}
		rows = append(rows, row)
	}
	return a.svc.SyncSeriesForList(ctx, rows, listID)
}

// episodeSeeder is the narrow slice of [*episodes.Repo] the
// import-list adder reaches for to bulk-insert episode rows freshly
// returned by the SkyHook GetSeriesInfo call. Defined here as an
// interface so the test double can stub without dragging the whole
// episodes.Repo construction surface into the adapter test.
type episodeSeeder interface {
	InsertMany(ctx context.Context, list []*episodes.Episode) error
}

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
	svc        *series.Service
	namingCfg  *namingconfig.Service
	validator  *series.AddValidator              // optional; nil skips pre-insert validation
	seriesInfo metadatasource.SeriesInfoProvider // optional; nil skips SkyHook TVDB-merge
	episodes   episodeSeeder                     // optional; nil skips post-insert episode seeding
	log        *slog.Logger
	now        func() time.Time // overridable for deterministic tests
}

// hasSeasons reports whether the JSON-encoded Seasons blob represents
// a non-empty array. Mirrors the C# `newSeries.Seasons != null &&
// newSeries.Seasons.Any()` guard at `AddSeriesService.cs:131`. Empty
// string, "null", and "[]" all count as no-seasons; any successfully-
// decoded non-empty array counts as has-seasons. Malformed JSON is
// treated as no-seasons (the SkyHook payload takes over) — keeps the
// merge resilient against junk inputs from custom import lists.
func hasSeasons(seasons string) bool {
	trimmed := strings.TrimSpace(seasons)
	if trimmed == "" || trimmed == "null" || trimmed == "[]" {
		return false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		return false
	}
	return len(arr) > 0
}

// addSkyHookData ports the C# `AddSeriesService.AddSkyhookData` flow
// at `src/NzbDrone.Core/Tv/AddSeriesService.cs:111-138`. Resolves the
// upstream TVDB id via [metadatasource.SeriesInfoProvider], preserves
// caller-supplied Seasons when non-empty (otherwise lets SkyHook seed
// them), and overlays the 11 user-supplied fields onto the SkyHook
// shape via [series.Series.ApplyChanges]. Returns the merged row by
// value so the caller can drop it straight into its loop variable.
//
// On [metadatasource.ErrSeriesNotFound]: C# throws a
// ValidationException at `AddSeriesService.cs:120-124`; we propagate
// the original error and let the batch loop's ignoreErrors=true
// posture handle it (warn + skip row).
//
// On [metadatasource.ErrServiceUnavailable]: C# lets the exception
// bubble (it wraps as a 503 at the API boundary, or aborts the
// import-list batch). For the batch path we follow the same
// per-row absorb-and-continue stance so one SkyHook hiccup doesn't
// pollute the whole batch — the row is skipped with a warn log.
//
// ctx cancellation propagates verbatim.
//
// J190 — second return value carries the episode slice SkyHook
// emitted alongside the Series. The C# AddSeriesService discards
// these (RefreshSeriesService re-fetches them later when the
// RefreshSeriesCommand fires). The Go-port shortcut: AddSeries's
// caller seeds them inline post-insert via [episodes.Repo.InsertMany]
// to avoid the double SkyHook hit while the real RefreshSeries
// handler is still stubbed. When the RefreshSeries port lands
// this seam stays in place — the seeder becomes idempotent (rows
// the import-list path inserted skip the RefreshSeries reconcile),
// or the inline seed gets removed in favour of the command pipe.
func addSkyHookData(ctx context.Context, provider metadatasource.SeriesInfoProvider, row series.Series) (series.Series, []episodes.Episode, error) {
	merged, eps, err := provider.GetSeriesInfo(ctx, row.TvdbID)
	if err != nil {
		return series.Series{}, nil, err
	}
	if merged == nil {
		// Defensive: a provider that returns (nil, nil, nil) is buggy
		// but we'd rather skip-with-warn than panic on the deref below.
		return series.Series{}, nil, errors.New("addSkyHookData: provider returned nil series")
	}
	// Preserve caller-supplied Seasons when non-empty; otherwise the
	// freshly-mapped SkyHook shape (which seeds Seasons from the
	// upstream response) wins.
	if !hasSeasons(row.Seasons) {
		row.Seasons = merged.Seasons
	}
	merged.ApplyChanges(&row)
	return *merged, eps, nil
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
		var pendingEpisodes []episodes.Episode
		if a.seriesInfo != nil {
			merged, eps, err := addSkyHookData(ctx, a.seriesInfo, row)
			if err != nil {
				// Matches the C# `AddSeriesService.AddSeries(List<Series>,
				// ignoreErrors:true)` catch-and-continue arm at
				// `AddSeriesService.cs:97-107` — a SeriesNotFoundException
				// or SkyHookException turns into a per-row skip rather
				// than aborting the whole batch.
				a.log.LogAttrs(ctx, slog.LevelWarn,
					"importlistsync: AddSeries: SkyHook merge failed (row skipped)",
					slog.Int("tvdbId", row.TvdbID),
					slog.String("title", row.Title),
					slog.Any("err", err))
				continue
			}
			row = merged
			pendingEpisodes = eps
		}
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
		stored, err := a.svc.AddSeries(ctx, &row)
		if err != nil {
			a.log.LogAttrs(ctx, slog.LevelWarn,
				"importlistsync: AddSeries: per-row AddSeries failed (batch continues)",
				slog.Int("tvdbId", row.TvdbID),
				slog.String("title", row.Title),
				slog.Any("err", err))
			continue
		}
		// Episode seeding — J190 short-circuit to avoid the double
		// SkyHook fetch the RefreshSeriesService would otherwise do.
		// Each captured episode gets SeriesID stamped from the
		// freshly-inserted row before bulk-insert. Errors warn and
		// continue — episode rows can be backfilled by a future
		// RefreshSeries handler run if any of the inserts fail.
		if a.episodes != nil && stored != nil && len(pendingEpisodes) > 0 {
			seedList := make([]*episodes.Episode, 0, len(pendingEpisodes))
			for j := range pendingEpisodes {
				ep := pendingEpisodes[j]
				ep.SeriesID = stored.ID
				seedList = append(seedList, &ep)
			}
			if err := a.episodes.InsertMany(ctx, seedList); err != nil {
				a.log.LogAttrs(ctx, slog.LevelWarn,
					"importlistsync: AddSeries: episode seeding failed (batch continues)",
					slog.Int("seriesId", stored.ID),
					slog.Int("episodeCount", len(seedList)),
					slog.Any("err", err))
			}
		}
	}
	return nil
}
