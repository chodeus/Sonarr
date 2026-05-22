# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J190` — import-list adds now produce a populated episodes row set per series instead of an empty episodes table. Recent commits: J190 episode seeding via SkyHook payload (this slice); J189 AddOptions.Monitor=None override; J188 Series.ApplyChanges + addSkyHookData merge. Open audit rows: B.3 RefreshSeries handler still stubbed — the inline episode-seed in J190 is documented-divergent from C# topology; the real RefreshSeries port will reconcile. B.2/B.5/B.6 closed.

## Closed this loop-run

- **J187** — `series.AddValidator` (path + title-slug duplicate checks) + `Repo.FindByTitleSlug` + wired into `seriesAdderAdapter` for import-list batch with warn+skip on conflict; 11 unit tests; all hard gates clean.
- **J188** — `Series.ApplyChanges(other)` + `addSkyHookData` per-row TVDB-merge helper + `hasSeasons` JSON guard + wired `metadataSourceProxy` into `seriesAdderAdapter.seriesInfo`; merge fires BEFORE PrepareForAdd; 10 unit tests; all hard gates clean.
- **J189** — `MonitorType` enum (14 ordinals frozen to C# order) + `AddOptions` struct + `DecodeAddOptions` helper + custom `MonitorType.UnmarshalJSON` + `PrepareForAdd` Monitor=None override arm; 13 unit tests; all hard gates clean.
- **J190** — `addSkyHookData` now returns the `[]episodes.Episode` slice GetSeriesInfo emits; `seriesAdderAdapter` grows narrow `episodeSeeder` interface satisfied by `*episodes.Repo`; post-AddSeries loop stamps SeriesID and bulk-inserts via InsertMany; failures warn+continue; 2 new tests; all hard gates clean.

## Next candidates

1. **RefreshSeries handler real port** (~250 LOC, multi-slice) — replace J166-era stub at `internal/commands/handlers/refreshseries.go` with a real handler that delegates into a new `refreshseries.Service` using `metadatasource.Proxy.GetSeriesInfo` + episode reconcile (Update existing + Add new + Soft-delete removed). Closes audit row B.3. Highest leverage now.
2. **Image-download subscriber off SeriesAddedEvent** (~200 LOC, needs design pass) — wire `mediacover.Service` to fetch poster/fanart on add via `EnsureCovers`/`DownloadCover`. Activates the HTTP-gated arm previously deferred under FAIL-CLOSED #7. Medium leverage.
3. **Generalize gap-detection capture to all 10 indexers** (~150 LOC) — log-warning fix per the B.7 follow-up. Unblocked; lower value.
