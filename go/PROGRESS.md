# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J195` — ShouldRefresh predicate ported and wired; the 12h scheduled-task tick now refreshes only stale rows instead of every row. Recent commits: J195 ShouldRefresh predicate + sweep gating (this slice); J194 SeriesRefreshStarting/Complete events; J193 all-series sweep. Audit B.3 remaining deferred sub-arcs: RefreshEpisodeService, DiskScanService rescan, UpdateTags, CommandResult.Indeterminate, path-normalisation, ManualTrigger-from-wire-shape. B.2/B.5/B.6 closed.

## Closed this loop-run

- **J187** — `series.AddValidator` (path + title-slug duplicate checks); 11 tests.
- **J188** — `Series.ApplyChanges` + `addSkyHookData` TVDB-merge in `seriesAdderAdapter`; 10 tests.
- **J189** — `MonitorType` enum + `AddOptions` + `PrepareForAdd` Monitor=None override; 13 tests.
- **J190** — episode seeding via SkyHook payload + `episodes.Repo.InsertMany`; 2 tests.
- **J191** — `internal/refreshseries.Service` real port; handler real per-id dispatcher; 17 tests; closes audit B.3.
- **J192** — `series.SeriesUpdatedEvent` + `*messaging.Bus` dep + `handleSeriesNotFound` Status=Deleted; 4 tests.
- **J193** — `Service.RefreshAll(ctx)` sweep + handler routes empty-body to RefreshAll; 4 tests.
- **J194** — `SeriesRefreshStartingEvent` + `SeriesRefreshCompleteEvent` types + handler publishes around every batch; 3 tests.
- **J195** — `refreshseries.ShouldRefresh` 6-branch predicate + `Service.SetEpisodeLookup` + RefreshAll per-row gating; 12 tests.

## Next candidates

1. **RefreshSeries follow-up: ManualTrigger from wire shape** (~80 LOC) — port the `Trigger` field on `RefreshSeriesCommand` so the handler can pass the real trigger value into `SeriesRefreshStartingEvent.ManualTrigger`. C# also uses ManualTrigger to bypass ShouldRefresh in the all-series sweep (`:256`: trigger==Manual || ShouldRefresh).
2. **RefreshSeries follow-up: UpdateTags integration** (~80 LOC) — port C# `:201-209`. After each RefreshSeriesInfo, call autotagging.Service.GetTagChanges and persist via series.Service.UpdateTags. Refresh has been computing changes; this writes them back.
3. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service` to fetch poster/fanart on add via `EnsureCovers`/`DownloadCover`. Activates the HTTP-gated arm previously deferred under FAIL-CLOSED #7.
