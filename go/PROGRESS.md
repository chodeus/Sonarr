# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J188` — TVDB-merge via `metadatasource.SeriesInfoProvider.GetSeriesInfo` now runs per-row in the import-list `seriesAdderAdapter`. Recent commits: J188 `Series.ApplyChanges` + `addSkyHookData` merge wired (this slice); J187 `series.AddValidator`; J186 `PrepareForAdd`. Open audit rows: B.3 RefreshSeries handler still stubbed — SkyHook M3 keystone is now exercised end-to-end through the import-list flow, so RefreshSeries's "blocked-on-SkyHook" unblock is fully past. B.2/B.5/B.6 all closed.

## Closed this loop-run

- **J187** — `series.AddValidator` (path + title-slug duplicate checks) + `Repo.FindByTitleSlug` + wired into `seriesAdderAdapter` for import-list batch with warn+skip on conflict; 11 unit tests; all hard gates clean.
- **J188** — `Series.ApplyChanges(other)` + `addSkyHookData` per-row TVDB-merge helper + `hasSeasons` JSON guard + wired `metadataSourceProxy` into `seriesAdderAdapter.seriesInfo`; merge fires BEFORE PrepareForAdd so SkyHook authoritative fields flow forward + user 11-field overlay carries; 10 unit tests; all hard gates clean.

## Next candidates

1. **RefreshSeries handler real port** (~250 LOC, multi-slice) — replace J166-era stub at `internal/commands/handlers/refreshseries.go` with a real handler that delegates into a new `refreshseries.Service` using `metadatasource.Proxy.GetSeriesInfo` + episode reconciliation against `episodes.Repo`. Closes audit row B.3. Highest leverage now that SkyHook is wired end-to-end.
2. **AddSeriesService AddOptions.Monitor=None override** (~80 LOC) — port the small `if (AddOptions.Monitor == MonitorTypes.None) Monitored = false` arm from `AddSeriesService.cs:154-157` into `series.PrepareForAdd`. Requires parsing the `AddOptions` JSON blob. Small but completes the C# `SetPropertiesAndValidate` parity surface.
3. **Image-download subscriber off SeriesAddedEvent** (~200 LOC) — adds a third subscriber alongside J185's folder-creation that wires `mediacover.Service` to fetch poster/fanart on add. Needs activating the HTTP-gated arm of `internal/mediacover` (`EnsureCovers`/`DownloadCover`) — design pass needed before slicing.
