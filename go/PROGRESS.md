# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J204` — `/initialize.json` endpoint ported. Surfaced by the frontend integration smoke as the critical blocker preventing the React UI from booting. New `internal/host/initialize_json.go` returns the JSON shape the React entrypoint expects; wired Anonymous in loginRoutes so the bundle can fetch it before knowing the API key. 3 unit tests + live-verified against rebuilt binary. Two frontend-serving blockers remain (J205 HTML template substitution, J206 SPA-fallback overreach). All three gates clean.

## Closed this loop-run

- **J197** — `refreshseries.Rescanner` + `RescanConfig` interfaces, `SetRescanner` setter, `rescanSeries` helper wired into RefreshSeriesInfo + RefreshAll skipped branch; 6 tests.
- **J198** — `commands.WithTrigger` / `TriggerFromContext` helpers, dispatcher attaches trigger to ctx, refreshseries handler+service thread `isManual` through; bypasses ShouldRefresh on manual + AfterManual config now fires correctly; 8 tests + ~25 mechanical call-site updates.
- **J199** — `commands.ReportResult` helper + `resultSlot` ctx wiring + `repo.End` Result column UPDATE; refreshseries per-id error path stamps `Indeterminate`; 6 tests including E2E dispatcher harness.
- **J200** — `internal/refreshepisodes` package + `episodes.EpisodeInfoRefreshedEvent` + `refreshseries.SetEpisodeRefresher`; SkyHook episodes payload now reconciles into persistence; 11 tests.
- **J201** — `refreshepisodes` finale-detection heuristic (`maybeStampSeriesFinale` + 3 helpers + `now` clock); 6 tests.
- **J202** — `refreshepisodes.unmonitorReaddedEpisodes` helper + AddOptions guard + 14d/1d asymmetric thresholds; 5 tests.
- **J203** — `refreshepisodes.adjustMultiEpisodeAirTime` + `adjustDirectToDvdAirDate` + `sortByEpisodeNumber` helpers; 8 tests.
- **Frontend smoke** — booted Go binary against fresh DB + real React bundle; full REST API + SignalR negotiate work; three frontend-layer blockers identified (`/initialize.json` missing, HTML template tokens unreplaced, SPA fallback too greedy).
- **J204** — `/initialize.json` endpoint port (`host.InitializeJSONHandler`); React app can now fetch its bootstrap config; 3 tests.

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

1. **J205 — HTML template substitution** (~80 LOC) — port C# `Sonarr.Http/Frontend/Mappers/HtmlMapperBase.cs` + `IndexHtmlMapper.cs`. Replace `__URL_BASE__`, `_THEME_`, `__MINI_PROFILER__` tokens in index.html + login.html on the fly; regex-rewrite href/src attributes to prepend urlBase + cache-breaker hash (unless `data-no-hash`). UI will render but with broken styling/asset URLs until this lands.
2. **J206 — SPA fallback exclusions** (~30 LOC) — port C# `IndexHtmlMapper.CanHandle` exclusions: requests containing `.` OR starting with `/content`, `/mediacover`, `/login`, `/api`, `/signalr` should NOT fall through to index.html. Currently `/initialize.json` works (J204 explicit route) but `/api/v3/seasonpass` (unimplemented) returns HTML instead of 404, masking route gaps.
3. **RefreshEpisodeService anime absolute-episode-number renumbering** (~80 LOC) — port C# `:43-46, :228-236` + `AbsoluteEpisodeNumberAdded` flag. Smallest impact since anime is a minority of series.
4. **Path-normalisation via GetActualCasing** (~100 LOC) — port C# `RefreshSeriesService.cs:118-119`. Final B.3 sub-arc.
5. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service.EnsureCovers`.
