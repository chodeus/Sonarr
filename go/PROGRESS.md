# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J201` — finale-detection heuristic ported (C# `RefreshEpisodeService.cs:95-107`). Ended shows that aired their finale in the last 14 days but lack a TheTVDB `FinaleType` now get one auto-stamped during the periodic refresh tick. Batch-wide `anyFinaleInSeason` guard ensures one finale per latest-season; the 8-condition guard mirrors C# exactly. 4 RefreshEpisodeService sub-arms + 1 path-normalisation sub-arm still deferred. All three gates clean.

## Closed this loop-run

- **J197** — `refreshseries.Rescanner` + `RescanConfig` interfaces, `SetRescanner` setter, `rescanSeries` helper wired into RefreshSeriesInfo + RefreshAll skipped branch; 6 tests.
- **J198** — `commands.WithTrigger` / `TriggerFromContext` helpers, dispatcher attaches trigger to ctx, refreshseries handler+service thread `isManual` through; bypasses ShouldRefresh on manual + AfterManual config now fires correctly; 8 tests + ~25 mechanical call-site updates.
- **J199** — `commands.ReportResult` helper + `resultSlot` ctx wiring + `repo.End` Result column UPDATE; refreshseries per-id error path stamps `Indeterminate`; 6 tests including E2E dispatcher harness.
- **J200** — `internal/refreshepisodes` package + `episodes.EpisodeInfoRefreshedEvent` + `refreshseries.SetEpisodeRefresher`; SkyHook episodes payload now reconciles into persistence; 11 tests.
- **J201** — `refreshepisodes` finale-detection heuristic (`maybeStampSeriesFinale` + 3 helpers + `now` clock); 6 tests.

## Previous loop-run (J187-J196)

- **J187** — `series.AddValidator` (path + title-slug duplicate checks); 11 tests.
- **J188** — `Series.ApplyChanges` + `addSkyHookData` TVDB-merge in `seriesAdderAdapter`; 10 tests.
- **J189** — `MonitorType` enum + `AddOptions` + `PrepareForAdd` Monitor=None override; 13 tests.
- **J190** — episode seeding via SkyHook payload + `episodes.Repo.InsertMany`; 2 tests.
- **J191** — `internal/refreshseries.Service` real port; handler real per-id dispatcher; 17 tests; **closes audit row B.3**.
- **J192** — `series.SeriesUpdatedEvent` + `*messaging.Bus` dep + `handleSeriesNotFound` Status=Deleted; 4 tests.
- **J193** — `Service.RefreshAll(ctx)` sweep + handler routes empty-body to RefreshAll; 4 tests.
- **J194** — `SeriesRefreshStartingEvent` + `SeriesRefreshCompleteEvent` types + handler publishes around every batch; 3 tests.
- **J195** — `refreshseries.ShouldRefresh` 6-branch predicate + `Service.SetEpisodeLookup` + RefreshAll per-row gating; 12 tests.
- **J196** — `SeriesStore.UpdateTags` interface grew + `Service.updateTags` helper + invoked on per-id + skipped-row paths; 3 tests.

## Next candidates (for next loop-run)

1. **RefreshEpisodeService UnmonitorReaddedEpisodes** (~80 LOC) — port C# `:157-189`. Gates the unmonitor of old-aired-on-cold-add via `series.AddOptions`. Needs the AddOptions field on the series row to be read by the refresher.
2. **RefreshEpisodeService AdjustMultiEpisodeAirTime + AdjustDirectToDvdAirDate** (~70 LOC combined) — ports C# `:191-226`. Same-day multi-episode air-time spread + ended-show air-date backfill from series.FirstAired.
3. **RefreshEpisodeService anime absolute-episode-number renumbering** (~80 LOC) — port C# `:43-46, :228-236` + `AbsoluteEpisodeNumberAdded` flag. Smallest impact since anime is a minority of series.
4. **Path-normalisation via GetActualCasing** (~100 LOC) — port C# `RefreshSeriesService.cs:118-119` (DirectoryInfo + GetActualCasing for case-resolution). Final B.3 sub-arc; needs the disk package to grow a portable case-resolver helper.
5. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service.EnsureCovers`. HTTP-gated arm previously deferred under FAIL-CLOSED #7.
