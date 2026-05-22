# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J191` — **audit row B.3 RefreshSeries CLOSED**. The handler now delegates to a real `refreshseries.Service` that ports `RefreshSeriesService.RefreshSeriesInfo` (SkyHook field-copy + Seasons merge + LastInfoSync + UpdateSeries). Recent commits: J191 RefreshSeries real port (this slice); J190 episode seeding via SkyHook; J189 AddOptions.Monitor=None. Open audit rows: B.2/B.3/B.5/B.6 all CLOSED — the `# Open` section now lists only doc-narrative; B.3's deferred sub-arcs are documented inline.

## Closed this loop-run

- **J187** — `series.AddValidator` (path + title-slug duplicate checks) + `Repo.FindByTitleSlug` + wired into `seriesAdderAdapter` for import-list batch with warn+skip on conflict; 11 unit tests; all hard gates clean.
- **J188** — `Series.ApplyChanges(other)` + `addSkyHookData` per-row TVDB-merge helper + `hasSeasons` JSON guard + wired `metadataSourceProxy` into `seriesAdderAdapter.seriesInfo`; merge fires BEFORE PrepareForAdd; 10 unit tests; all hard gates clean.
- **J189** — `MonitorType` enum (14 ordinals frozen to C# order) + `AddOptions` struct + `DecodeAddOptions` helper + custom `MonitorType.UnmarshalJSON` + `PrepareForAdd` Monitor=None override arm; 13 unit tests; all hard gates clean.
- **J190** — `addSkyHookData` now returns the `[]episodes.Episode` slice GetSeriesInfo emits; `seriesAdderAdapter` grows narrow `episodeSeeder` interface satisfied by `*episodes.Repo`; post-AddSeries loop stamps SeriesID and bulk-inserts via InsertMany; failures warn+continue; 2 new tests; all hard gates clean.
- **J191** — new `internal/refreshseries.Service` ports C# `RefreshSeriesService.RefreshSeriesInfo`; handler at `internal/commands/handlers/refreshseries.go` promoted from J166 stub to real per-id dispatcher; 17 unit tests; closes audit row B.3.

## Next candidates

1. **RefreshSeries follow-up: SeriesNotFound→Status=Deleted soft-delete arm** (~80 LOC) — port C# `:70-81`. When `metadatasource.ErrSeriesNotFound` bubbles, flip `Series.Status` to deleted, persist via UpdateSeries(SkipPublishUpdatedEvent:true), then publish SeriesUpdatedEvent. Small focused slice. Closes the most visible refresh sub-arc.
2. **RefreshSeries follow-up: all-series sweep mode** (~150 LOC) — port C# `:249-282` for the empty-SeriesIds path. Iterate `series.Service.GetAllSeries`, sort by SortTitle, call ShouldRefresh predicate (defer to "always true" pending the predicate port), dispatch per-id. Unlocks the 12h scheduled-task tick.
3. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service` to fetch poster/fanart on add via `EnsureCovers`/`DownloadCover`. Activates the HTTP-gated arm previously deferred under FAIL-CLOSED #7.
