# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

**PAUSED — 10 consecutive slices shipped (J187-J196), stopping per the protocol's "pause for sanity-check sync with the user even if nothing is broken" rule.** HEAD at `J196` — UpdateTags integrated into the refresh batch (per-id + skipped-row paths). All gates clean across the whole run. No blocker; this is a scheduled checkpoint. Resume with another `/loop` when ready.

## Closed this loop-run

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

1. **ManualTrigger from wire shape** (~80 LOC) — **DESIGN DECISION REQUIRED**: the C# `RefreshSeriesCommand.Trigger` field is metadata on the row (persisted via Commands.Trigger), not in the JSON body. Plumbing it to the handler requires either changing the `Handler.Execute(ctx, body)` interface signature (cross-cutting — all handlers update) OR using a `ctx.Value` key for trigger metadata. Both are reasonable; the user should pick. Also bypasses ShouldRefresh in the all-series sweep on manual trigger (C# `:256`).
2. **DiskScanService.Rescan port** (~150 LOC, MULTI-SLICE) — port C# `:168-199` RescanSeries arm. Needs `internal/mediafiles.Service.Scan` to grow a per-series entry point + RescanAfterRefreshType config. Activates disk-rescan-after-refresh behaviour gated on the config.
3. **CommandResult.Indeterminate reporter integration** (~100 LOC) — port C# `ICommandResultReporter` so refreshseries can mark individual command-result-status (Successful / Indeterminate / Failed) on per-row outcomes. Requires growing the commands package surface.
4. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service.EnsureCovers`. HTTP-gated arm previously deferred under FAIL-CLOSED #7.
