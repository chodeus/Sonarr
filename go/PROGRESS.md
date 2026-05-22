# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J199` — `CommandResult.Indeterminate` reporter ported via the same ctx.Value pattern (`commands.ReportResult`). Dispatcher attaches a mutable `resultSlot`; handlers stamp via `ReportResult`; dispatcher reads back and sets `c.Result` before `Complete`/`Fail` fire. Also fixes a pre-existing bug — `repo.End` SQL UPDATE was missing the `Result` column, so reported results never landed. refreshseries per-id error path stamps `Indeterminate` matching C# `Execute :235, :245`. All three gates clean.

## Closed this loop-run

- **J197** — `refreshseries.Rescanner` + `RescanConfig` interfaces, `SetRescanner` setter, `rescanSeries` helper wired into RefreshSeriesInfo + RefreshAll skipped branch; 6 tests.
- **J198** — `commands.WithTrigger` / `TriggerFromContext` helpers, dispatcher attaches trigger to ctx, refreshseries handler+service thread `isManual` through; bypasses ShouldRefresh on manual + AfterManual config now fires correctly; 8 tests + ~25 mechanical call-site updates.
- **J199** — `commands.ReportResult` helper + `resultSlot` ctx wiring + `repo.End` Result column UPDATE; refreshseries per-id error path stamps `Indeterminate`; 6 tests including E2E dispatcher harness.

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

1. **RefreshEpisodeService episode-reconcile** (~250 LOC, MULTI-SLICE) — port C# `RefreshEpisodeService.RefreshEpisodeInfo` at `:53-135`. Reconciles SkyHook episode list against persisted rows: insert new, update changed, delete removed. The post-J190 episode-seeding path covers the cold-start case but subsequent refreshes don't reconcile the episode list yet. Closing this drops B.3's open sub-arc list to just one (path-normalisation via GetActualCasing).
2. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service.EnsureCovers`. HTTP-gated arm previously deferred under FAIL-CLOSED #7.
3. **Generalize gap-detection capture to all 10 indexers** — log-warning, not a correctness bug. Lower-value but unblocked.
4. **Phase 6 readiness** — lower the contract-replay diff CLI known-failing baseline by 3 (J179/J180/J181 closed those goldens). Audit `go/test/contract/` for any CI gates that hardcode the old count.
