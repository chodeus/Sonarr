# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J211` — SeriesScannedHandler partial port (C# `Tv/SeriesScannedHandler.cs:36-44` AddOptions==null branch). `episoderefreshed.Service.Search` now subscribed to BOTH `mediafiles.SeriesScannedEvent` and `mediafiles.SeriesScanSkippedEvent` at composition root. The mediafiles publishers already fired (completedScanning + publishSkipped) — pre-J211 those events had no subscriber driving Search. Closes the J207→J209→J210→J211 rescan-to-search dispatch chain end-to-end. AddOptions-set post-add-actions arm (C# `:47-73`) deferred. All three gates clean.

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
- **J208** — RefreshSeriesService path-canonicalisation: new `normalizePath` (filepath.Abs+Clean ≡ C# DirectoryInfo.FullName) + `getActualCasing` (Windows-only behaviour deferred — no-op on all platforms today, doc-comment points the next Windows-casing slice at the split-into-build-tagged-files migration); wired into `RefreshSeriesInfo` between `LastInfoSync` stamp and `mergeSeasons` with warn-log+continue error absorb (mirrors C# try/catch); 5 unit tests on the helpers + 2 integration tests on the wiring. **Closes the final B.3 sub-arc — audit row B.3 fully closed.**
- **J209** — EpisodeRefreshedService consumer: new `internal/episoderefreshed.Service` ports C# `Tv/EpisodeRefreshedService.cs` end-to-end. `Handle(EpisodeInfoRefreshedEvent)` subscriber filters Added (recently-aired + monitored, 14d/+1d window) + Updated (J207 AbsoluteEpisodeNumberAdded + same window) and buffers deduped IDs in a per-series cache. `Search(seriesID)` drains the cache, filters !HasFile via EpisodeLookup, dispatches via narrow EpisodeSearcher (nil-tolerant). AddOptions + series.Monitored gates honoured. Search call site (C# SeriesScannedHandler) DEFERRED — Go-port doesn't yet have a SeriesScannedEvent publisher; Search exposed as a public method for that future slice. Optional bus auto-subscribes Handle. 13 tests covering 8 Handle filter branches + 5 Search drain/dispatch paths.
- **J210** — `episoderefreshed.Service` wiring at composition root. New `episodeRefreshedSearcherAdapter` in `cmd/sonarr/main.go` wraps `*indexersearch.Service.EpisodeSearch` (per-ID `indexersearch.SearchSettings{}` call with warn-log absorb) into the slice-taking `EpisodeSearcher` shape — mirrors the loop+absorb pattern J39's `MissingEpisodeSearch` handler already uses internally. `episoderefreshed.NewService(bus, seriesSvc, episodesSvc, adapter, nil, logger)` constructed alongside the existing `refreshSeriesSvc` / `refreshEpisodesSvc` block. Handle auto-subscribes to the bus at boot.
- **J211** — SeriesScannedHandler partial port (C# `Tv/SeriesScannedHandler.cs:36-44` — the `AddOptions==null` branch of `HandleScanEvents`). Two new `messaging.Subscribe` calls in `cmd/sonarr/main.go` wire `episoderefreshed.Service.Search` to BOTH `mediafiles.SeriesScannedEvent` AND `mediafiles.SeriesScanSkippedEvent` — both publishers already existed (J197-era), only the subscriber side was missing. The discarded `_, err =` from J210 became a named `episodeRefreshedSvc, err :=` so the subscriber closures can call its Search. AddOptions-set post-add-actions arm at C# `:47-73` (episode-monitored override + SeriesAddCompletedEvent publish + per-add search command push + AddOptions clear) DEFERRED to its own slice — touches different dependencies. **Closes the J207→J209→J210→J211 rescan-to-search dispatch chain end-to-end.**

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
3. **SeriesScannedHandler post-add-actions arm** — port C# `Tv/SeriesScannedHandler.cs:47-73`, the AddOptions-set branch that J211 deliberately deferred. Needs: an episode-monitored override service (port of `EpisodeMonitoredService.SetEpisodeMonitoredStatus`), `SeriesAddCompletedEvent` type, per-add search command dispatchers (`SeriesSearchCommand` / `MissingEpisodeSearchCommand` / `CutoffUnmetEpisodeSearchCommand` — second and third both exist; first doesn't), and a `series.Service.RemoveAddOptions` mutation. Each may need to be its own sub-slice depending on scope.
5. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service.EnsureCovers`.
6. **Windows-only GetActualCasing impl** (~150 LOC) — split `pathcasing.go` into `pathcasing_windows.go` + `pathcasing_other.go` and implement the parent-walk casing recovery via Win32 FindFirstFileW. Low priority (Sonarr containers run Linux); useful for native Windows installers.
