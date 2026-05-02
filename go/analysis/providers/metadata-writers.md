# Metadata writers provider matrix

Family-level orchestration is documented in `subsystems/extras.md` (the "Metadata producer" section); canonical ThingiProvider plumbing in `subsystems/thingiprovider.md`. Sonarr ships **4 concrete `IMetadata` writers**: Kodi/Emby (XbmcMetadata), Plex (PlexMetadata), Roksbox (RoksboxMetadata), WDTV (WdtvMetadata). A historical fifth (`MediaBrowserMetadata`) was hard-deleted by mig 124 (`124_remove_media_browser_metadata.cs`) — not in scope.

This is the most ThingiProvider-conformant family, with one notable twist: **`MetadataFactory.InitializeProviders` seeds the `Metadata` table with one row per registered class on first run** (`MetadataFactory.cs:25-48`) — there's no "Add new metadata" UI; users see all four rows and toggle `Enable`. Per `subsystems/extras.md`: orphaned rows are NOT deleted on factory init (only inserts), so retiring a writer requires a manual SQL migration (cross-cite EXTRAS-Q6 / mig 124).

## Family-level contract

| Aspect | Value |
|--------|-------|
| Provider interface | `IMetadata : IProvider` (`src/NzbDrone.Core/Extras/Metadata/IMetadata.cs`) — 7 producer methods (`GetFilenameAfterMove`, `FindMetadataFile`, `SeriesMetadata`, `EpisodeMetadata`, `SeriesImages`, `SeasonImages`, `EpisodeImages`) |
| Settings interface | `IProviderConfig` (no metadata-specific extension) |
| Abstract base | `MetadataBase<TSettings>` (`src/NzbDrone.Core/Extras/Metadata/MetadataBase.cs`) — empty `Test()`, default `GetFilenameAfterMove` strips ext + reapplies |
| Definition type | `MetadataDefinition : ProviderDefinition` (`src/NzbDrone.Core/Extras/Metadata/MetadataDefinition.cs`) — **no provider-specific fields** beyond the canonical 6 ProviderDefinition columns |
| Factory | `MetadataFactory : ProviderFactory<IMetadata, MetadataDefinition>` (`src/NzbDrone.Core/Extras/Metadata/MetadataFactory.cs`) — overrides `InitializeProviders` to seed the table (the only factory in the codebase that does); `Active()` filters by `Enable` |
| Repository | `MetadataRepository : ProviderRepository<MetadataDefinition>` (`src/NzbDrone.Core/Extras/Metadata/MetadataRepository.cs`) |
| DB tables | `Metadata` (mig 039; `Settings` is **not nullable**) — provider-definition rows. `MetadataFiles` (mig 039 + 050 `Hash` + 099 `Extension/Added` + 188 PG TZ) — per-output sidecar tracking (one row per emitted NFO/image/`.plexmatch`/etc.) |
| Status tracking | **None.** Metadata writers don't fail in a way that needs back-off — disk failures are logged and skipped; there's no `MetadataStatus` table |
| Test action contract | `Test()` returns empty `ValidationResult` (no per-provider connectivity test); `MetadataFactory` does not override `Test` to record status |
| Producer orchestration | `MetadataService : ExtraFileManager<MetadataFile>` (`src/NzbDrone.Core/Extras/Metadata/MetadataService.cs`) is the single orchestrator that calls each enabled `IMetadata.SeriesMetadata`/`EpisodeMetadata`/etc. and writes to disk via `IDiskProvider.WriteAllText` + `IMediaFileAttributeService.SetFilePermissions` |
| Hash de-dup | `MetadataFile.Hash` (`SHA256Hash` of generated content) — `ProcessSeriesMetadata`/`ProcessEpisodeMetadata` skip the disk write when content hash matches existing row (`MetadataService.cs:265-275, :329-332`). `Hash` is NULL for image rows; image dedup is by `RelativePath` + disk-exists |
| Path-conflict policy | Three rules per `subsystems/extras.md` "Path conflicts" section: (1) tracked `OtherExtraFile` collisions → renamed to `<path>-orig`; (2) untracked file on disk → skip (never overwrite); (3) hash unchanged → skip |
| Move-on-rename | `MetadataService.MoveFilesAfterRename` iterates `GetAvailableProviders()` (NOT `Enabled()`) so disabled writers' rows still get moved with their video — intentional, prevents orphans |
| `MetadataType` enum | `Unknown=0, SeriesMetadata=1, EpisodeMetadata=2, SeriesImage=3, SeasonImage=4, EpisodeImage=5` (`src/NzbDrone.Core/Extras/Metadata/MetadataType.cs`); persisted in `MetadataFiles.Type` |
| `MetadataSectionType` | `"metadata"` / `"image"` UI grouping for `[FieldDefinition(..., Section=...)]` |

## Concrete providers

### XbmcMetadata (Kodi / Emby / Jellyfin)

| Field | Value |
|-------|-------|
| `Implementation` | `XbmcMetadata` |
| `ConfigContract` | `XbmcMetadataSettings` |
| Source file | `src/NzbDrone.Core/Extras/Metadata/Consumers/Xbmc/XbmcMetadata.cs` |
| Settings class | `src/NzbDrone.Core/Extras/Metadata/Consumers/Xbmc/XbmcMetadataSettings.cs` |
| Provider `Name` | `"Kodi (XBMC) / Emby"` (`XbmcMetadata.cs:49`) |
| Outputs | **Series metadata** `tvshow.nfo` (XML root `<tvshow>`: title, rating, plot, mpaa, id, uniqueid type=tvdb default=true, uniqueid type=imdb, uniqueid type=tmdb, uniqueid type=tvmaze, genre*, tag*, status, premiered, enddate, studio, actor*, episodeguide JSON). **Episode metadata** `<filename>.nfo` (`<episodedetails>` per linked episode + `streamdetails/video, audio*, subtitle*` from `MediaInfo`, plus `hdrtype` mapped from `HdrFormat` → `dolbyvision`/`hdr10`/`hlg`/empty). **Series images** `<covertype>.<jpg|png>` per `MediaCoverTypes` (poster/banner/fanart/clearart/clearlogo/keyart/series). **Season images** `season{NN}-{covertype}.jpg` (specials = `season-specials-{covertype}.jpg`). **Episode images** `<filename minus ext>-thumb.jpg` |
| Notable quirks | `XbmcNfoDetector.IsXbmcNfoFile` (`Xbmc/XbmcNfoDetector.cs`) reads file + regex-matches `<(movie|tvshow|episodedetails|artist|album|musicvideo)>` to disambiguate Kodi NFOs from arbitrary `.nfo` (skips files >10MB). `KodiEpisodeGuide.cs:31` populates `TvRage` from `series.TvMazeId` (looks like a bug — EXTRAS-Q5). `GetExistingWatchedStatus` scans the prior NFO so re-writes preserve `<watched>true</watched>`. UTF-8 with declaration. `SeriesMetadata(reason=EpisodesImported)` returns `null` (no churn on download burst — `XbmcMetadata.cs:143-146`) |
| Test fixtures | `src/NzbDrone.Core.Test/Extras/Metadata/Consumers/Xbmc/FindMetadataFileFixture.cs` |

**Settings fields** (defaults all true except the two Advanced and `EpisodeImageThumb`):

| C# property | C# type | `[FieldDefinition]` | Default | Privacy |
|-------------|---------|---------------------|---------|---------|
| `SeriesMetadata` | bool | Type=Checkbox, Section="metadata", Label="Series Metadata" | true | Normal |
| `SeriesMetadataEpisodeGuide` | bool | Type=Checkbox, Section="metadata", Advanced=true | false | Normal |
| `SeriesMetadataUrl` | bool | Type=Checkbox, Section="metadata", Advanced=true | false | Normal |
| `EpisodeMetadata` | bool | Type=Checkbox, Section="metadata" | true | Normal |
| `EpisodeImageThumb` | bool | Type=Checkbox, Section="metadata", Advanced=true | false | Normal |
| `SeriesImages` | bool | Type=Checkbox, Section="image" | true | Normal |
| `SeasonImages` | bool | Type=Checkbox, Section="image" | true | Normal |
| `EpisodeImages` | bool | Type=Checkbox, Section="image" | true | Normal |

Validator: empty `AbstractValidator<XbmcMetadataSettings>` (no constraints).

### PlexMetadata

| Field | Value |
|-------|-------|
| `Implementation` | `PlexMetadata` |
| `ConfigContract` | `PlexMetadataSettings` |
| Source file | `src/NzbDrone.Core/Extras/Metadata/Consumers/Plex/PlexMetadata.cs` |
| Settings class | `src/NzbDrone.Core/Extras/Metadata/Consumers/Plex/PlexMetadataSettings.cs` |
| Provider `Name` | `"Plex"` |
| Outputs | **Series metadata** `.plexmatch` at series root: plain-text `Title:`, `Year:`, `TvdbId:`, `ImdbId:` lines. Optional `EpisodeMappings` adds `Episode: S{NN}{E…}: {relPath}` per episode file (special seasons → `SP{NN}`; multi-episode files concatenated like `S03E01-E02`). **No `.nfo` output. No series/season/episode images** |
| Notable quirks | `FindMetadataFile` only matches the literal relative path `.plexmatch`. `EpisodesImported` reason re-emits `.plexmatch` (vs Xbmc skipping it). Deliberately minimal — Plex pulls metadata from its own remote agents |
| Test fixtures | none |

**Settings fields:**

| C# property | C# type | `[FieldDefinition]` | Default | Privacy |
|-------------|---------|---------------------|---------|---------|
| `SeriesPlexMatchFile` | bool | Type=Checkbox, Label="Series .plexmatch" | true | Normal |
| `EpisodeMappings` | bool | Type=Checkbox, Label="Episode Mappings" | false | Normal |

### RoksboxMetadata

| Field | Value |
|-------|-------|
| `Implementation` | `RoksboxMetadata` |
| `ConfigContract` | `RoksboxMetadataSettings` |
| Source file | `src/NzbDrone.Core/Extras/Metadata/Consumers/Roksbox/RoksboxMetadata.cs` |
| Settings class | `src/NzbDrone.Core/Extras/Metadata/Consumers/Roksbox/RoksboxMetadataSettings.cs` |
| Provider `Name` | `"Roksbox"` |
| Outputs | **No series metadata.** **Episode metadata** `Path.ChangeExtension(file, "xml")` — `<video>` root with `title, year, genre, actors (max 3), description, length, mpaa` (validated against 16-rating allow-list at `:35`; falls back to `UNRATED`). **Series image** `<seriesFolderName>.jpg` at series root, sourced from `IMapCoversToLocal.GetCoverPath(series, Poster)`. **Season images** `<seasonFolderName>.jpg` inside each season folder. **Episode images** `Path.ChangeExtension(file, "jpg")` |
| Notable quirks | Season folder discovery via `^(season (?<season>\d+))|(?<specials>specials)` regex (case-insensitive, `:34`). Filename derived from existing folder names — relies on user's folder naming. Mig 184 (`184_remove_invalid_roksbox_metadata_images.cs`) deleted rows where Roksbox episode images had been mistakenly placed inside a `metadata/` subfolder (SQLite vs PG path-separator branching) |
| Test fixtures | `src/NzbDrone.Core.Test/Extras/Metadata/Consumers/Roksbox/FindMetadataFileFixture.cs` |

**Settings fields** (defaults all true):

| C# property | C# type | `[FieldDefinition]` | Default | Privacy |
|-------------|---------|---------------------|---------|---------|
| `EpisodeMetadata` | bool | Type=Checkbox, Section="metadata" | true | Normal |
| `SeriesImages` | bool | Type=Checkbox, Section="image" | true | Normal |
| `SeasonImages` | bool | Type=Checkbox, Section="image" | true | Normal |
| `EpisodeImages` | bool | Type=Checkbox, Section="image" | true | Normal |

Help-text on each field contains literal example filenames (`Season##\filename.xml`, etc.).

### WdtvMetadata

| Field | Value |
|-------|-------|
| `Implementation` | `WdtvMetadata` |
| `ConfigContract` | `WdtvMetadataSettings` |
| Source file | `src/NzbDrone.Core/Extras/Metadata/Consumers/Wdtv/WdtvMetadata.cs` |
| Settings class | `src/NzbDrone.Core/Extras/Metadata/Consumers/Wdtv/WdtvMetadataSettings.cs` |
| Provider `Name` | `"WDTV"` |
| Outputs | **No series metadata.** **Episode metadata** `Path.ChangeExtension(file, "xml")` — `<details>` root with `id, title, series_name, episode_name, season_number(00), episode_number(00), firstaired, genre, actor, overview` (TODOs for credits/director). **Series image** `folder.jpg` at series root. **Season images** `folder.jpg` inside each season folder. **Episode images** `Path.ChangeExtension(file, "metathumb")` |
| Notable quirks | Same `^(season N)|specials` regex as Roksbox (`:34`). `FindMetadataFile` switch — collisions between series-`folder.jpg` and season-`folder.jpg` are resolved by parent-folder regex |
| Test fixtures | `src/NzbDrone.Core.Test/Extras/Metadata/Consumers/Wdtv/FindMetadataFileFixture.cs` |

**Settings fields** (identical shape to Roksbox, defaults all true):

| C# property | C# type | `[FieldDefinition]` | Default | Privacy |
|-------------|---------|---------------------|---------|---------|
| `EpisodeMetadata` | bool | Type=Checkbox, Section="metadata" | true | Normal |
| `SeriesImages` | bool | Type=Checkbox, Section="image" | true | Normal |
| `SeasonImages` | bool | Type=Checkbox, Section="image" | true | Normal |
| `EpisodeImages` | bool | Type=Checkbox, Section="image" | true | Normal |

## Open questions

- **PROV-Q38** — `MetadataFactory.InitializeProviders` is the **only** factory in the codebase that overrides `InitializeProviders` to seed the table (`MetadataFactory.cs:25-48`). The Go port must replicate this on first start: insert one row per registered `IMetadata` class with `Enable=false`, but **only if** the row doesn't already exist. Decide the upgrade story — does an existing C# install's `Metadata` table get respected (rows preserved with their `Enable` toggle) or rewritten? Recommend: upsert by `Implementation` string, preserve `Settings`/`Enable`. Cross-cite TP-Q1.
- **PROV-Q39** — Per EXTRAS-Q6: `MetadataFactory.InitializeProviders` never deletes orphan rows; obsolete writers (cf. `MediaBrowserMetadata` deleted by mig 124) require manual migration. Should the Go port handle this at the factory level — e.g. `Implementation`-name allow-list at startup, with a healthcheck for orphans? Recommend yes — flag obsoleted writers via a `HealthCheck` row instead of silent-delete.
- **PROV-Q40** — Per EXTRAS-Q5: `KodiEpisodeGuide.cs:31` populates `TvRage` field from `series.TvMazeId`. Replicate verbatim (preserves byte-equivalent NFO output) or fix in Go port (changes user-visible NFOs)? Recommend replicate — fixing is a behaviour delta that would force every Kodi user to re-rewrite their library.
- **PROV-Q41** — `MetadataFile.Consumer` column is stored as the C# class name (`"XbmcMetadata"`, `"PlexMetadata"`, `"WdtvMetadata"`, `"RoksboxMetadata"`). When the Go port renames consumer types, an upgrade migration must rewrite this column. Capture the canonical mapping in `migrations/ledger.md`. Cross-cite EXTRAS-Q10. Recommend: keep the C# names verbatim as the registry IDs (consistent with TP-Q1 — same string keys for `Implementation`, `ConfigContract`, and `Consumer`).
- **PROV-Q42** — `MetadataDefinition` has zero unique columns (no `Tags`/`OnX` toggles unlike `NotificationDefinition`). The Go port can keep this minimal, but consider whether to add a `Tags` field for parity with other ThingiProvider families. Currently no use case in C#; recommend leave alone.
- **PROV-Q43** — `MetadataBase.Test()` returns empty — there's no per-provider connectivity test. Should the Go port add a sanity test (e.g. write a temp file in the series root)? Behaviour delta — recommend leave alone, surface disk-permission failures as healthchecks instead.
- **PROV-Q44** — Per EXTRAS-Q4: `MetadataService.ProcessEpisodeImages` (`:417-466`) has `return new List<MetadataFile> { existingMetadata };` inside the per-image loop when an existing row's path differs (`:444`) — short-circuits the rest of the consumer's image list. Bug or intentional? Port must preserve unless we file a bug. Cross-cite EXTRAS-Q4.
- **PROV-Q45** — `Settings` JSON in `Metadata` table is **not nullable** (mig 039) while `Indexers`/`Notifications`/`ImportLists` allow nullable. Tighter contract — preserve. Cross-cite `subsystems/datastore.md`.
- **PROV-Q46** — All four writers' `*MetadataSettings` validators are empty `AbstractValidator<T>`. The Go port can drop the validator boilerplate entirely for these and only add per-field validation if/when constraints emerge. Behaviourally equivalent.
- **PROV-Q47** — Roksbox/Wdtv share an identical settings shape (4 Checkbox fields). Worth a shared `BasicMetadataSettings` struct in Go (with the two consumer-specific structs embedding it)? Code-quality only; doesn't affect the registry. Recommend yes — DRY and easy.
