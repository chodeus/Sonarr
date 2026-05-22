# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J192` — refreshseries SeriesNotFound soft-delete arm closes the most visible RefreshSeries follow-up. Recent commits: J192 SeriesNotFound→Status=Deleted (this slice); J191 RefreshSeries real port; J190 episode seeding via SkyHook. Audit B.3 deferred sub-arcs remaining: RefreshEpisodeService, DiskScanService rescan, UpdateTags, all-series sweep, SeriesRefreshStarting/Complete events, CommandResult.Indeterminate, path-normalisation. Other audit rows B.2/B.5/B.6 closed.

## Closed this loop-run

- **J187** — `series.AddValidator` (path + title-slug duplicate checks) + `Repo.FindByTitleSlug` + wired into `seriesAdderAdapter` for import-list batch with warn+skip on conflict; 11 unit tests; all hard gates clean.
- **J188** — `Series.ApplyChanges(other)` + `addSkyHookData` per-row TVDB-merge helper + `hasSeasons` JSON guard + wired `metadataSourceProxy` into `seriesAdderAdapter.seriesInfo`; merge fires BEFORE PrepareForAdd; 10 unit tests; all hard gates clean.
- **J189** — `MonitorType` enum (14 ordinals frozen to C# order) + `AddOptions` struct + `DecodeAddOptions` helper + custom `MonitorType.UnmarshalJSON` + `PrepareForAdd` Monitor=None override arm; 13 unit tests; all hard gates clean.
- **J190** — `addSkyHookData` now returns the `[]episodes.Episode` slice GetSeriesInfo emits; `seriesAdderAdapter` grows narrow `episodeSeeder` interface satisfied by `*episodes.Repo`; post-AddSeries loop stamps SeriesID and bulk-inserts via InsertMany; failures warn+continue; 2 new tests; all hard gates clean.
- **J191** — new `internal/refreshseries.Service` ports C# `RefreshSeriesService.RefreshSeriesInfo`; handler at `internal/commands/handlers/refreshseries.go` promoted from J166 stub to real per-id dispatcher; 17 unit tests; closes audit row B.3.
- **J192** — new `series.SeriesUpdatedEvent` (distinct from SeriesEditedEvent) + refreshseries.Service grows `*messaging.Bus` dep + `handleSeriesNotFound` flips Status to Deleted on TheTVDB 404 then publishes the event; 4 new tests; all hard gates clean.

## Next candidates

1. **RefreshSeries follow-up: all-series sweep mode** (~150 LOC) — port C# `:249-282` for the empty-SeriesIds path. Iterate `series.Service.GetAllSeries`, sort by SortTitle, call ShouldRefresh predicate (defer to "always true" pending the predicate port), dispatch per-id. Unlocks the 12h scheduled-task tick.
2. **RefreshSeries follow-up: SeriesRefreshStarting/Complete events** (~80 LOC) — port C# `:215, :284`. Add the two event types to series/events.go, publish at entry/exit of the handler-driven batch. Subscribers exist in C# (UI progress bar wires off the Starting event); Go-port port adds the wire-shape for future subscribers.
3. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service` to fetch poster/fanart on add via `EnsureCovers`/`DownloadCover`. Activates the HTTP-gated arm previously deferred under FAIL-CLOSED #7.
