# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J194` — SeriesRefreshStarting/Complete event types ported and emitted around every RefreshSeries batch. Recent commits: J194 refresh-events (this slice); J193 all-series sweep; J192 SeriesNotFound soft-delete. Audit B.3 remaining deferred sub-arcs: RefreshEpisodeService, DiskScanService rescan, UpdateTags, ShouldRefresh predicate, CommandResult.Indeterminate, path-normalisation, ManualTrigger-from-wire-shape. B.2/B.5/B.6 closed.

## Closed this loop-run

- **J187** — `series.AddValidator` (path + title-slug duplicate checks); 11 tests.
- **J188** — `Series.ApplyChanges` + `addSkyHookData` TVDB-merge in `seriesAdderAdapter`; 10 tests.
- **J189** — `MonitorType` enum + `AddOptions` + `PrepareForAdd` Monitor=None override; 13 tests.
- **J190** — episode seeding via SkyHook payload + `episodes.Repo.InsertMany`; 2 tests.
- **J191** — `internal/refreshseries.Service` real port; handler real per-id dispatcher; 17 tests; closes audit B.3.
- **J192** — `series.SeriesUpdatedEvent` + `*messaging.Bus` dep + `handleSeriesNotFound` Status=Deleted; 4 tests.
- **J193** — `Service.RefreshAll(ctx)` sweep + handler routes empty-body to RefreshAll; 4 tests + 2 renamed.
- **J194** — `SeriesRefreshStartingEvent` + `SeriesRefreshCompleteEvent` types + handler publishes around every batch; 3 tests + 6 sig-updates.

## Next candidates

1. **RefreshSeries follow-up: ShouldRefresh predicate port** (~150 LOC) — port C# `CheckIfSeriesShouldBeRefreshed.cs`. Predicate gates per-row refresh on age + ended-status; cuts periodic-tick load from "refresh every row" to "refresh stale rows only".
2. **RefreshSeries follow-up: ManualTrigger from wire shape** (~80 LOC) — port the `Trigger` field on `RefreshSeriesCommand` so the handler can pass the real trigger value into `SeriesRefreshStartingEvent.ManualTrigger`. Requires plumbing the trigger through the commands.Service push path.
3. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service` to fetch poster/fanart on add via `EnsureCovers`/`DownloadCover`. Activates the HTTP-gated arm previously deferred under FAIL-CLOSED #7.
