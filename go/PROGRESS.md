# Phase 5 Loop Dashboard

Resumption pointer for the /loop driver. Cheaper to read than STATUS.md (which is the authoritative append-log).

## Current state

HEAD at `J187` — `series.AddValidator` (path + title-slug duplicate checks) now composes with J186 `PrepareForAdd` in the import-list batch flow. Recent commits: J187 `series.AddValidator` + `Repo.FindByTitleSlug` wired into `seriesAdderAdapter` (this slice); J186 `PrepareForAdd` non-SkyHook subset of `SetPropertiesAndValidate`; J185 `SeriesAddedEvent` → folder-creation subscriber. Open audit rows: B.3 RefreshSeries handler still stubbed (SkyHook M3 keystone now substantially landed in `internal/metadatasource/` — RefreshSeries unblock-and-wire is the next big multi-slice arc). B.2/B.5/B.6 all closed.

## Closed this loop-run

- **J187** — `series.AddValidator` (path + title-slug duplicate checks) + `Repo.FindByTitleSlug` + wired into `seriesAdderAdapter` for import-list batch with warn+skip on conflict; 11 unit tests; all hard gates clean.

## Next candidates

1. **AddSeriesService TVDB-merge slice** (~150 LOC) — wire `metadatasource.Proxy.GetSeriesInfo` into the import-list adapter so each row's TVDB id resolves to the canonical Show shape pre-insert (mirrors C# `AddSkyhookData` at `AddSeriesService.cs:111-138`). Medium leverage — activates the SkyHook M3 keystone for the import-list flow.
2. **RefreshSeries handler real port** (~200 LOC, multi-slice) — replace the J166-era stub at `internal/commands/handlers/refreshseries.go` with a real handler that delegates into a new `refreshseries.Service` using `metadatasource.Proxy.GetSeriesInfo` + episode reconciliation. Closes audit row B.3. Higher leverage but bigger slice.
3. **Image-download subscriber off SeriesAddedEvent** (~150 LOC) — adds a third subscriber alongside J185's folder-creation that wires `mediacover.Service` to fetch poster/fanart on add. Blocked on activating the HTTP-gated arm of `internal/mediacover` (`EnsureCovers`/`DownloadCover`) — needs its own design pass.
