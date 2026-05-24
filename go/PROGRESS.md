# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J207` — RefreshEpisodeService anime absolute-episode-number renumbering ported (C# `:43-46, :228-251`) + `AbsoluteEpisodeNumberAdded` transient flag (Episode.cs:49 + TableMapping.cs:132 ignore mirrored as `json:"-"` field on `episodes.Episode`). Closes the second-to-last B.3 sub-arc. All three gates clean.

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
- **J205** — HTML template substitution middleware (`injectHTMLTemplates` for `__URL_BASE__`, `_THEME_`, `__MINI_PROFILER__`); `WithTheme` option; chained between static handler and `injectBaseHref`; 6 tests.
- **J206** — SPA fallback exclusions in both `staticHandlerFS` + `staticHandler`; route gaps now 404 instead of leaking the SPA HTML; 4 test groups including regression guards for existing-file-with-dot + bare-SPA-route preservation.
- **J207** — Anime absolute-episode-number renumbering: `mapAbsoluteEpisodeNumbers` filter (highest-season-wins on abs collisions) + `orderEpisodes` (abs ASC then non-abs by (season,episode) for anime; (season,episode) ASC for standard) + `AbsoluteEpisodeNumberAdded` transient flag on `episodes.Episode` (json:"-"); wired into `RefreshEpisodeInfo` between dedupe + reconcile; episodesPerSeason + hasExistingFinaleInLatestSeason now consume the ordered list; 6 tests.

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

1. **C.1 upstream/v5-develop catch-up** — `chodeus/Sonarr v5-develop` force-synced to upstream HEAD `f0f59b95c` on 2026-05-24 (56 upstream commits); `go-port` is now 58 behind. New row in AUDIT_FIX_BACKLOG.md `Open` section with 10 sub-items (C.1.a–C.1.j). Sequencing: C.1.a merge first, then small warm-ups (C.1.h CancellationToken parity check, C.1.j refresh-log line), then single-area behaviour ports (C.1.f Pushover E2E, C.1.g Localhost auth bypass), then new v5 endpoint family (C.1.b CustomFormat → C.1.c ImportList → C.1.d ImportList Settings → C.1.e AutoTagging), then C.1.i contract-replay re-baseline. **Per-slice protocol: one sub-item = one slice.**
2. **Frontend smoke pass 2** — boot the rebuilt Go binary, point a browser at `:8989`, click through every nav target. Identify the NEXT layer of issues (likely: SignalR connect/subscribe drift, mutation paths that touch unported services, asset URL rewriter when urlBase is non-empty). Now also needs to validate the React Router v7 upgrade (came in via C.1.a merge) renders correctly when served by the Go binary.
3. **Path-normalisation via GetActualCasing** (~100 LOC) — port C# `RefreshSeriesService.cs:118-119`. Final B.3 sub-arc; with J207 closed this is the only remaining B.3 follow-up.
4. **EpisodeRefreshedService port** — downstream consumer of `EpisodeInfoRefreshedEvent` + `AbsoluteEpisodeNumberAdded` flag (C# `EpisodeRefreshedService.cs`). Triggers re-search for renumbered episodes. ~150 LOC; subscribes to the event J200 already publishes.
5. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service.EnsureCovers`.
