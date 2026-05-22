# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J189` — C# `AddSeriesService.SetPropertiesAndValidate` parity now complete except for the AddSeriesValidator path-shape checks (still waiting on rootfolders.Service + portable path-shape oracle). Recent commits: J189 `AddOptions.Monitor=None` override + `MonitorType` enum + `DecodeAddOptions` (this slice); J188 `Series.ApplyChanges` + `addSkyHookData` merge; J187 `series.AddValidator`. Open audit rows: B.3 RefreshSeries handler still stubbed — SkyHook is fully wired so the unblock is past. B.2/B.5/B.6 closed.

## Closed this loop-run

- **J187** — `series.AddValidator` (path + title-slug duplicate checks) + `Repo.FindByTitleSlug` + wired into `seriesAdderAdapter` for import-list batch with warn+skip on conflict; 11 unit tests; all hard gates clean.
- **J188** — `Series.ApplyChanges(other)` + `addSkyHookData` per-row TVDB-merge helper + `hasSeasons` JSON guard + wired `metadataSourceProxy` into `seriesAdderAdapter.seriesInfo`; merge fires BEFORE PrepareForAdd; 10 unit tests; all hard gates clean.
- **J189** — `MonitorType` enum (14 ordinals frozen to C# order) + `AddOptions` struct + `DecodeAddOptions` helper + custom `MonitorType.UnmarshalJSON` accepting both camelCase name AND integer ordinal + `PrepareForAdd` Monitor=None override arm; 13 unit tests; all hard gates clean.

## Next candidates

1. **RefreshSeries handler real port** (~250 LOC, multi-slice) — replace J166-era stub at `internal/commands/handlers/refreshseries.go` with a real handler that delegates into a new `refreshseries.Service` using `metadatasource.Proxy.GetSeriesInfo` + episode reconciliation against `episodes.Repo`. Closes audit row B.3. Highest leverage now that SkyHook is wired end-to-end.
2. **Episode-seeding subscriber off SeriesAddedEvent** (~200 LOC) — wire the `[]episodes.Episode` slice returned by `GetSeriesInfo` (currently dropped at the floor in J188's `addSkyHookData`) into a subscriber that bulk-inserts via `episodes.Repo`. Mirrors C# RefreshSeriesService's episode-reconcile half. Complements the future RefreshSeries handler.
3. **Generalize gap-detection capture to all 10 indexers** (~150 LOC) — log-warning fix per the B.7 follow-up, unblocked.
