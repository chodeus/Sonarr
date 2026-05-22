# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

**PAUSED after J197.** HEAD at `J197` — `DiskScanService.Rescan` integrated into `refreshseries.Service` via `Rescanner` + `RescanConfig` narrow interfaces (post-construction `SetRescanner` setter); per-id + skipped-row branches both call `rescanSeries` which ports the C# `:168-199` three-branch decision tree (Never → publish `mediafiles.SeriesScanSkippedEvent`, AfterManual+!isManual → publish skip with reason, else → call `Scan` with error-absorb). All three gates clean. **Stopped per the per-slice protocol**: the next-up candidate is `ManualTrigger from wire shape` which the protocol flags as requiring a design decision (Handler interface signature change vs `ctx.Value` for trigger metadata) — escalated to the user before starting.

## Closed this loop-run

- **J197** — `refreshseries.Rescanner` + `RescanConfig` interfaces, `SetRescanner` setter, `rescanSeries` helper wired into RefreshSeriesInfo + RefreshAll skipped branch; 6 tests.

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

1. **ManualTrigger from wire shape** (~80 LOC) — **DESIGN DECISION REQUIRED**: the C# `RefreshSeriesCommand.Trigger` field is metadata on the row (persisted via Commands.Trigger), not in the JSON body. Plumbing it to the handler requires either changing the `Handler.Execute(ctx, body)` interface signature (cross-cutting — all handlers update) OR using a `ctx.Value` key for trigger metadata. Both are reasonable; the user should pick. Also bypasses ShouldRefresh in the all-series sweep on manual trigger (C# `:256`); also lets refreshseries.rescanSeries honour the "AfterManual" config branch (today it always skips).
2. **CommandResult.Indeterminate reporter integration** (~100 LOC) — port C# `ICommandResultReporter` so refreshseries can mark individual command-result-status (Successful / Indeterminate / Failed) on per-row outcomes. Requires growing the commands package surface — needs `ProgressMessageContext`-equivalent (Go's context.Value works) + `IManageCommandQueue.SetResult`.
3. **RefreshEpisodeService episode-reconcile** (~250 LOC, MULTI-SLICE) — port C# `RefreshEpisodeService.RefreshEpisodeInfo` at `:53-135`. Reconciles SkyHook episode list against persisted rows: insert new, update changed, delete removed. The post-J190 episode-seeding path covers the cold-start case but subsequent refreshes don't reconcile the episode list yet.
4. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service.EnsureCovers`. HTTP-gated arm previously deferred under FAIL-CLOSED #7.
