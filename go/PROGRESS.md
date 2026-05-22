# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J193` — the 12h RefreshSeries scheduled-task tick now does useful work via `Service.RefreshAll(ctx)`. Recent commits: J193 all-series sweep (this slice); J192 SeriesNotFound soft-delete; J191 RefreshSeries real port. Audit B.3 deferred sub-arcs remaining: RefreshEpisodeService, DiskScanService rescan, UpdateTags, ShouldRefresh predicate, SeriesRefreshStarting/Complete events, CommandResult.Indeterminate, path-normalisation. Audit B.2/B.5/B.6 closed.

## Closed this loop-run

- **J187** — `series.AddValidator` (path + title-slug duplicate checks) + `Repo.FindByTitleSlug` + wired into `seriesAdderAdapter` for import-list batch with warn+skip on conflict; 11 unit tests; all hard gates clean.
- **J188** — `Series.ApplyChanges(other)` + `addSkyHookData` per-row TVDB-merge helper + `hasSeasons` JSON guard + wired `metadataSourceProxy` into `seriesAdderAdapter.seriesInfo`; 10 unit tests; all hard gates clean.
- **J189** — `MonitorType` enum (14 ordinals frozen to C# order) + `AddOptions` struct + `DecodeAddOptions` + `MonitorType.UnmarshalJSON` + `PrepareForAdd` Monitor=None override arm; 13 unit tests; all hard gates clean.
- **J190** — `addSkyHookData` now returns the `[]episodes.Episode` slice; `seriesAdderAdapter` gains `episodeSeeder` interface satisfied by `*episodes.Repo`; post-AddSeries bulk InsertMany seeds episode rows; 2 new tests; all hard gates clean.
- **J191** — new `internal/refreshseries.Service` ports C# `RefreshSeriesService.RefreshSeriesInfo`; handler promoted from J166 stub; 17 unit tests; closes audit row B.3.
- **J192** — new `series.SeriesUpdatedEvent` + `*messaging.Bus` dep on `refreshseries.Service` + `handleSeriesNotFound` flips Status=Deleted on TheTVDB 404; 4 new tests.
- **J193** — `Service.RefreshAll(ctx)` iterates GetAllSeries in SortTitle order with warn+continue; handler routes empty-body/empty-seriesIds to RefreshAll; 4 new tests + 2 renamed.

## Next candidates

1. **RefreshSeries follow-up: SeriesRefreshStarting/Complete events** (~80 LOC) — port C# `:215, :284`. Add the two event types to series/events.go and publish them around the handler-driven batch (per-id + all-series). Subscribers gate UI progress + post-refresh follow-ups.
2. **RefreshSeries follow-up: ShouldRefresh predicate port** (~150 LOC) — port C# `CheckIfSeriesShouldBeRefreshed.cs` predicate that gates per-row refresh in the all-series sweep based on age + ended-status. Cuts the periodic tick's load from "refresh every row" to "refresh stale rows only".
3. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service` to fetch poster/fanart on add via `EnsureCovers`/`DownloadCover`. Activates the HTTP-gated arm previously deferred under FAIL-CLOSED #7.
