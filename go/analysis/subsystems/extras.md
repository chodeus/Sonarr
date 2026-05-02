# Extras

**Source:** `src/NzbDrone.Core/Extras/**`
**Target Go package:** `go/internal/extras/` (with sub-packages `metadata/`, `metadata/consumers/<name>/`, `subtitles/`, `others/`, `files/`)
**Status:** drafted

## Purpose
"Extras" are every non-video sidecar file that Sonarr produces or imports next to an `EpisodeFile`: media-server NFO/`.plexmatch` metadata, poster/banner/fanart images, episode thumbnails, and subtitle tracks (`.srt`, `.ass`, `.idx`, …). The subsystem has two complementary jobs. **Producer:** when a metadata writer is enabled, generate per-series, per-season, and per-episode metadata + images on disk and remember them in the DB so renames can move them and uninstalls can clean them. **Consumer:** during episode import (download-folder scan and series rescan) match arbitrary on-disk sidecar files to their owning `EpisodeFile`, copy/move them into the library next to the renamed video, and persist a row so that future deletes/renames keep the sidecar in lock-step. The producer side is provider-pluggable (`IMetadata` via the `ThingiProvider`/`IProviderFactory` framework — see `notifications.md` for that pattern); the consumer side is split into three hard-coded extra-file *families* (`MetadataFile`, `SubtitleFile`, `OtherExtraFile`) each with its own `ExtraFileManager`, `ExtraFileService`, and `IImportExistingExtraFiles` importer.

The subsystem hangs off MediaFiles — see `mediafiles.md` for the import / rescan / rename flow that produces the events `ExtraService` listens to (`SeriesScannedEvent`, `EpisodeFolderCreatedEvent`, `SeriesRenamedEvent`, `MediaCoversUpdatedEvent`, `DownloadsProcessedEvent`) and for the `ImportApprovedEpisodes` call site that drives the per-episode `ImportEpisode` and `MoveFilesAfterRename` entry points.

## Architecture

### Top-level orchestration

| Type | File | Kind | Notes |
|------|------|------|-------|
| `IExtraService` / `ExtraService` | `Extras/ExtraService.cs` | event hub + façade | Two public methods: `ImportEpisode(localEpisode, episodeFile, isReadOnly)` and `MoveFilesAfterRename(series, episodeFile)`. Event handler for 5 events. Holds `List<IManageExtraFiles> _extraFileManagers`, sorted by `Order` (`Metadata=0` → `Subtitle=1` → `Other=2`, `ExtraService.cs:50`). Maintains a `Dictionary<int, Series> _seriesWithImportedFiles` lock-protected buffer that fans out post-import metadata creation when `DownloadsProcessedEvent` arrives. |
| `IExistingExtraFiles` / `ExistingExtraFileService` | `Extras/ExistingExtraFileService.cs` | event handler | `ImportExtraFiles(series, possibleExtraFiles, fileNameBeforeRename)` — runs every `IImportExistingExtraFiles` in `Order` (`Metadata=0` → `Subtitle=1` → `Other=2`). Handles `SeriesScannedEvent` (`ExistingExtraFileService.cs:44`). Each importer returns its imports + previously-imported list; the orchestrator concatenates `Path.Combine(series.Path, RelativePath)` so subsequent importers see the union as "already taken". |
| `IImportExistingExtraFiles` | `Extras/IImportExistingExtraFiles.cs` | interface | Just `int Order` + `ProcessFiles(series, filesOnDisk, importedFiles, fileNameBeforeRename) → IEnumerable<ExtraFile>`. The `fileNameBeforeRename` param toggles "keep existing entries" semantics in the base class (`ImportExistingExtraFilesBase.cs:24`). |
| `ImportExistingExtraFilesBase<TExtraFile>` | `Extras/ImportExistingExtraFilesBase.cs` | abstract base | Provides `FilterAndClean(series, filesOnDisk, importedFiles, keepExistingEntries)` that returns an `ImportExistingExtraFileFilterResult<TExtraFile>` carrying `PreviouslyImported` + filtered `FilesOnDisk`. Two modes: `keepExistingEntries=true` (script-import / post-rename) — wipes only the rows that match files-on-disk so the new importer can rebuild them; `keepExistingEntries=false` (regular series scan) — `Clean()` deletes rows for files that vanished from disk and rows for already-imported paths. |
| `ImportExistingExtraFileFilterResult<TExtraFile>` | `Extras/ImportExistingExtraFileFilterResult.cs` | DTO | Two lists; constructor only. |

`ExtraService.ImportEpisode` (`ExtraService.cs:54-59`):
1. `ImportExtraFiles(localEpisode, episodeFile, isReadOnly)` — reads `Sonarr.ImportExtraFiles` toggle (`:63`). If on: parses `Sonarr.ExtraFileExtensions` (CSV, dot-trim then re-prefixed with `.`, `:70-73`), enumerates `_diskProvider.GetFiles(parentFolder, recursive=localEpisode.FolderEpisodeInfo != null)`, and for each file with a wanted extension dispatches to the **first** `IManageExtraFiles.CanImportFile` that returns true — note `Metadata` always returns `false`, `Subtitle` returns true iff extension is in `SubtitleFileExtensions.Extensions`, and `Other` returns `true` unconditionally (`MetadataService.cs:233`, `SubtitleService.cs:107`, `OtherExtraService.cs:84`). The break makes this a winner-take-all dispatch; subtitle has higher priority than other.
2. `CreateAfterEpisodeImport(series, episodeFile)` — calls each `IManageExtraFiles.CreateAfterEpisodeImport`. Only `MetadataService` does any work here (writes per-episode NFO + thumb); subtitle and other return empty.
3. Adds the series to `_seriesWithImportedFiles` so `DownloadsProcessedEvent` later triggers `CreateAfterEpisodesImported(series)` per provider — only `MetadataService` does work (re-renders the series-level NFO once per batch).

`ExtraService.MoveFilesAfterRename(series, episodeFile)` (`ExtraService.cs:152-160`) — single-file variant called when import script renamed the file. Fans out to all managers' `MoveFilesAfterRename(series, [episodeFile])`.

`ExtraService` event subscriptions (`ExtraService.cs:26-30`):

| Event | Producer | Effect |
|-------|----------|--------|
| `MediaCoversUpdatedEvent` | `MediaCoverService` (`MediaCoverService.cs:237` — only fired when `Updated == true`) | `CreateAfterMediaCoverUpdate(series)` per manager — only `MetadataService` does work (re-syncs series + season images from the just-refreshed media-cover cache). |
| `EpisodeFolderCreatedEvent` | `EpisodeFileMovingService.EnsureEpisodeFolder` (`EpisodeFileMovingService.cs:200`) | `CreateAfterEpisodeFolder(series, seriesFolder, seasonFolder)` — `MetadataService` writes the series NFO + series images if `seriesFolder` is non-empty, season images if `seasonFolder` is non-empty (`MetadataService.cs:158-188`). Lets a brand-new season folder pick up `season01-poster.jpg` immediately. |
| `SeriesScannedEvent` | `DiskScanService.CompletedScanning` (`DiskScanService.cs:199`) | `CreateAfterSeriesScan(series, episodeFiles)` — `MetadataService` rebuilds **everything**: cleans missing rows (`CleanExtraFileService.Clean`), writes series NFO, series images, season images, then for every episode file the per-episode NFO + thumb. Subtitle and Other do nothing. |
| `SeriesRenamedEvent` | `RenameEpisodeFileService.RenameFiles` (`RenameEpisodeFileService.cs:175`) | `MoveFilesAfterRename(series, episodeFiles)` per manager. |
| `DownloadsProcessedEvent` | `DownloadProcessingService` (`DownloadProcessingService.cs:80`) | Drains `_seriesWithImportedFiles` (lock-protected) and calls `CreateAfterEpisodesImported(series)` once per series. Only `MetadataService` reacts (re-renders series NFO with `SeriesMetadataReason.EpisodesImported`, which the Xbmc consumer treats as a no-op — `XbmcMetadata.cs:143`). |

### `IManageExtraFiles` and `ExtraFileManager<TExtraFile>`

Defined in `Extras/Files/ExtraFileManager.cs`. The interface (`:15-26`) is the producer side:

```
int Order;
IEnumerable<ExtraFile> CreateAfterMediaCoverUpdate(Series);
IEnumerable<ExtraFile> CreateAfterSeriesScan(Series, List<EpisodeFile>);
IEnumerable<ExtraFile> CreateAfterEpisodesImported(Series);
IEnumerable<ExtraFile> CreateAfterEpisodeImport(Series, EpisodeFile);
IEnumerable<ExtraFile> CreateAfterEpisodeFolder(Series, seriesFolder, seasonFolder);
IEnumerable<ExtraFile> MoveFilesAfterRename(Series, List<EpisodeFile>);
bool CanImportFile(LocalEpisode, EpisodeFile, path, extension, readOnly);
IEnumerable<ExtraFile> ImportFiles(LocalEpisode, EpisodeFile, files, isReadOnly);
```

Three concrete subclasses: `MetadataService` (`Order=0`), `SubtitleService` (`Order=1`), `OtherExtraService` (`Order=2`). The base class supplies two helpers (`ExtraFileManager.cs:57-126`):

- **`ImportFile(series, episodeFile, sourcePath, readOnly, extension, fileNameSuffix=null)`** — computes target = `<episodeFile.RelativePath dir>/<episodeFile basename><suffix><extension>`, then `_diskTransferService.TransferFile` with `TransferMode.Move` (or `HardLinkOrCopy`/`Copy` when `readOnly` per `Sonarr.CopyUsingHardlinks`). Returns a populated `TExtraFile` (un-saved). Subtitle and Other call this; Metadata writers do **not** (they download/write directly via `MetadataService.SaveMetadataFile` / `DownloadImage`).
- **`MoveFile(series, episodeFile, extraFile, fileNameSuffix=null)`** — recomputes the target path and `_diskProvider.MoveFile` if it differs. Logs and returns null on failure (does not throw).

### `ExtraFile` model & `ExtraFileService<TExtraFile>`

| Field (`Extras/Files/ExtraFile.cs`) | Type | Notes |
|-------------------------------------|------|-------|
| `SeriesId` | `int` | FK to `Series` (no DB FK constraint, just cleaned by `CleanupOrphanedExtraFiles` housekeeper). |
| `EpisodeFileId` | `int?` | Nullable: `MetadataType.SeriesMetadata`/`SeriesImage`/`SeasonImage` rows have it null. |
| `SeasonNumber` | `int?` | Nullable: series-level rows have it null. |
| `RelativePath` | `string` | Relative to `Series.Path`. |
| `Added`, `LastUpdated` | `DateTime` | UTC. `Upsert` sets `LastUpdated = UtcNow`; `Added` only set on insert (`ExtraFileService.cs:74-82`). |
| `Extension` | `string` | Stored with leading `.` in normal flow but see migration 108 (`108_fix_extra_file_extension.cs:38-50`) which patched legacy rows. |

`IExtraFileService<TExtraFile>` (`Extras/Files/ExtraFileService.cs`) — generic CRUD wrapper around `IExtraFileRepository<TExtraFile>` plus two event handlers:

- `IHandleAsync<SeriesDeletedEvent>` → `_repository.DeleteForSeriesIds(message.Series.Select(.Id))` (`:98-102`). Note **async** handler — runs on the thread-pool, after the event burst.
- `IHandle<EpisodeFileDeletedEvent>` (sync) — branches on `DeleteMediaFileReason`:
  - `NoLinkedEpisodes` (the orphan-cleanup case) → log only, **don't** touch disk; just delete the row.
  - any other reason → enumerate `GetFilesByEpisodeFile`, for each existing path send to `IRecycleBinProvider.DeleteFile(path, subfolder)` (recoverable — see `mediafiles.md` notes on recycle bin), then delete the row.

`IExtraFileRepository<TExtraFile>` (`Extras/Files/ExtraFileRepository.cs`) — generic Dapper repo with extension methods `DeleteForSeriesIds`, `DeleteForSeason`, `DeleteForEpisodeFile`, `GetFilesBySeries/BySeason/ByEpisodeFile`, `FindByPath(seriesId, path)`.

### Three families and their tables

`TableMapping.cs:148-150` registers the three concrete subclasses:

```
Mapper.Entity<MetadataFile>("MetadataFiles").RegisterModel();
Mapper.Entity<SubtitleFile>("SubtitleFiles").RegisterModel();
Mapper.Entity<OtherExtraFile>("ExtraFiles").RegisterModel();
```

Each family has its own service / repository / importer / manager. Note `OtherExtraFile`'s table is **`ExtraFiles`** (legacy name preserved by the migration history — see `099_extra_and_subtitle_files.cs`).

| Family | Subclass | Manager (Order) | Service | Repository | Importer | DB table |
|--------|----------|-----------------|---------|------------|----------|----------|
| Metadata | `MetadataFile : ExtraFile` (`Files/MetadataFile.cs`) — adds `Hash`, `Consumer`, `Type` (`MetadataType` enum) | `MetadataService` (0) | `MetadataFileService` | `MetadataFileRepository` | `ExistingMetadataImporter` (Order 0) | `MetadataFiles` |
| Subtitles | `SubtitleFile : ExtraFile` (`Subtitles/SubtitleFile.cs`) — adds `Language`, `LanguageTags : List<string>`, `Title`, `Copy : int`. Computed `AggregateString = Language + Title + LanguageTagsAsString + Extension` used to group sidecars per video. | `SubtitleService` (1) | `SubtitleFileService` | `SubtitleFileRepository` | `ExistingSubtitleImporter` (Order 1) | `SubtitleFiles` |
| Others | `OtherExtraFile : ExtraFile` (`Others/OtherExtraFile.cs`) — no extra columns | `OtherExtraService` (2) | `OtherExtraFileService` | `OtherExtraFileRepository` | `ExistingOtherExtraImporter` (Order 2) | `ExtraFiles` |

The numeric `Order` doubles as the priority when matching files in `ExtraService.ImportExtraFiles`: a `.srt` file must fall to `SubtitleService.CanImportFile` because `MetadataService.CanImportFile` is hard-coded `false`; a `.nfo` file falls through to `OtherExtraService` because Subtitle rejects it.

### `MetadataType` enum & `MetadataSectionType` constants

`Extras/Metadata/MetadataType.cs` — used to discriminate `MetadataFile` rows by what part of a series they describe:

```
Unknown=0, SeriesMetadata=1, EpisodeMetadata=2, SeriesImage=3, SeasonImage=4, EpisodeImage=5
```

These integers are persisted (see migrations 41/44/184 below). `MetadataSectionType` (`Metadata/MetadataSectionType.cs`) is just two UI-grouping string constants `"metadata"` / `"image"` used by the `[FieldDefinition(..., Section=...)]` attributes on the `*Settings` classes.

## Metadata producer

### `IMetadata` interface (`Extras/Metadata/IMetadata.cs`)

```
string GetFilenameAfterMove(Series, EpisodeFile, MetadataFile);
MetadataFile FindMetadataFile(Series, path);
MetadataFileResult SeriesMetadata(Series, SeriesMetadataReason reason);
MetadataFileResult EpisodeMetadata(Series, EpisodeFile);
List<ImageFileResult> SeriesImages(Series);
List<ImageFileResult> SeasonImages(Series, Season);
List<ImageFileResult> EpisodeImages(Series, EpisodeFile);
```

`SeriesMetadataReason` enum has three values: `Scan`, `EpisodeFolderCreated`, `EpisodesImported`. Only the Kodi/XBMC consumer treats them differently — it returns `null` for `EpisodesImported` (`XbmcMetadata.cs:143-146`) so a download burst doesn't churn the series NFO.

`MetadataFileResult` (`Files/MetadataFileResult.cs`) is `(RelativePath, Contents)` — pure in-memory; it's the orchestrator that writes to disk via `IDiskProvider.WriteAllText` and `IMediaFileAttributeService.SetFilePermissions` (`MetadataService.cs:507-511`).

`ImageFileResult` (`Files/ImageFileResult.cs`) is `(RelativePath, Url)`. The orchestrator's `DownloadImage` (`MetadataService.cs:468-505`) treats `Url` polymorphically: if it starts with `http`, `IHttpClient.DownloadFile`; if it's an existing local file (the media-cover cache), `IDiskProvider.CopyFile`; otherwise log a warn. Wraps in `try/catch (HttpException|WebException|Exception)`.

`MetadataBase<TSettings>` (`Metadata/MetadataBase.cs`) — abstract base. `Test()` returns empty `ValidationResult` (no per-provider connectivity test). Default `GetFilenameAfterMove` strips the metadata file's existing extension and re-applies it to the renamed video's path (`:30-37`); Roksbox/Wdtv/Xbmc override this for their type-specific filename rules.

`IMetadataFactory` / `MetadataFactory` (`Metadata/MetadataFactory.cs`) — `ProviderFactory<IMetadata, MetadataDefinition>`. Reflection-bootstraps a row per `IMetadata` instance with `Enable=false` on first run (`:25-48`); never deletes orphaned rows. `Enabled()` filters by `MetadataDefinition.Enable`. `MetadataDefinition` has no provider-specific fields (`Metadata/MetadataDefinition.cs`).

`MetadataRepository` (`Metadata/MetadataRepository.cs`) — `ProviderRepository<MetadataDefinition>` against the `Metadata` table.

### `MetadataService` orchestration (`Metadata/MetadataService.cs`)

`Order = 0`. Implements `ExtraFileManager<MetadataFile>`. Key behaviors:

- **`CreateAfterMediaCoverUpdate`** (`:60-83`) — `_cleanMetadataService.Clean(series)` (`Files/CleanMetadataFileService.cs` removes rows whose file is gone), then for each enabled consumer rebuild **series + season images only**.
- **`CreateAfterSeriesScan`** (`:85-116`) — full rebuild: series NFO, series images, season images, then per-episode NFO + thumb. Skips silently if `series.Path` doesn't exist (`:90-94`).
- **`CreateAfterEpisodesImported`** (`:118-141`) — only the **series**-level NFO with `reason=EpisodesImported` (Xbmc no-ops; Roksbox/Wdtv don't write series metadata at all; Plex re-emits `.plexmatch`). This is the only handler that uses `SeriesMetadataReason` distinct from the others.
- **`CreateAfterEpisodeImport`** (`:143-156`) — per-episode NFO + thumb across all enabled consumers.
- **`CreateAfterEpisodeFolder`** (`:158-188`) — picks series-level outputs when a series-folder is being created, season-level when a season-folder is being created. Does **not** rebuild per-episode metadata.
- **`MoveFilesAfterRename`** (`:190-229`) — uses `IMetadata.GetFilenameAfterMove` (provider-specific because Xbmc thumbs are `<base>-thumb.jpg` not `<base>.jpg`); skips per-file errors with a warn. Note: the comment at `:195` says "TODO: Move EpisodeImage and EpisodeMetadata metadata files, instead of relying on consumers to do it". For consumers iteration uses **`GetAvailableProviders()`** (not just `Enabled()`) so disabled providers' rows still get moved with their video.
- **`CanImportFile` always returns false** (`:231-234`) — `MetadataService` never claims a file from `ImportEpisode`'s dispatch loop.

`ProcessSeriesMetadata` / `ProcessEpisodeMetadata` (`:246-340`) hash the generated content with `SHA256Hash()` and skip the disk write when it matches the existing row's `Hash` — that's why the `Hash` column was added in migration 50 (`050_add_hash_to_metadata_files.cs`).

`ProcessSeriesImages`, `ProcessSeasonImages`, `ProcessEpisodeImages` — for each `ImageFileResult`, *skip if the file already exists on disk* (`:350-355` and equivalents). Calls `IOtherExtraFileRenamer.RenameOtherExtraFile` first (see "Path conflicts" below).

`GetMetadataFile` (`:513-535`) — when the lookup finds **multiple** existing rows for the same `(series, type, key)`, keeps the first and recycles the rest from disk **and** the DB. This is the de-dup logic that runs on every regeneration.

### `CleanExtraFileService` / `ICleanMetadataService`

`Metadata/Files/CleanMetadataFileService.cs:13-43`. The class is named `CleanExtraFileService` but the interface is `ICleanMetadataService` (mismatch is intentional — DI binds them). `Clean(series)`: enumerate `MetadataFile` rows, drop any whose file is gone from disk. Called at the start of every `MetadataService.CreateAfterX` except `CreateAfterEpisodeImport` and `CreateAfterEpisodeFolder`.

### Concrete `IMetadata` consumers

There are **four** classes registered, all under `Extras/Metadata/Consumers/`. The `Plex` consumer is a `.plexmatch` writer, not an XML/NFO writer; `Wdtv` and `Roksbox` predate Kodi's NFO format; `XbmcMetadata` covers Kodi/XBMC and Emby/Jellyfin (the class `Name` is literally `"Kodi (XBMC) / Emby"`, `XbmcMetadata.cs:49`). A fifth historical consumer `MediaBrowserMetadata` was deleted in migration 124 (`124_remove_media_browser_metadata.cs`).

| Consumer | File | `Name` | Writes (file shapes per setting) | Per-OS / quirks |
|----------|------|--------|----------------------------------|------------------|
| **Kodi/Emby (XBMC)** | `Consumers/Xbmc/XbmcMetadata.cs` | `"Kodi (XBMC) / Emby"` | **Series metadata** `tvshow.nfo` (XML root `<tvshow>`, fields: `title, rating, plot, mpaa, id, uniqueid type=tvdb default=true, uniqueid type=imdb, uniqueid type=tmdb, uniqueid type=tvmaze, genre*, tag*, status, premiered, enddate, studio, actor*, episodeguide` JSON). Optional `SeriesMetadataUrl` appends a TheTVDB.com URL line after the XML. **Episode metadata** `<filename>.nfo` (`<episodedetails>` per linked episode, with `streamdetails/video, audio*, subtitle*` from `MediaInfo`, plus `hdrtype` mapped from `HdrFormat` — `dolbyvision`/`hdr10`/`hlg`/empty). **Series images** `<covertype>.<jpg|png>` per `MediaCoverTypes` (poster/banner/fanart/clearart/clearlogo/keyart/series). **Season images** `season{NN}-{covertype}.jpg` (specials = `season-specials-{covertype}.jpg`). **Episode images** `<filename minus ext>-thumb.jpg`. | `XbmcNfoDetector.IsXbmcNfoFile` (`Xbmc/XbmcNfoDetector.cs`) reads the file and regex-matches `<(movie\|tvshow\|episodedetails\|artist\|album\|musicvideo)>` to disambiguate Kodi NFOs from arbitrary `.nfo`. Skips files >10 MB. `episodeguide` JSON shape in `KodiEpisodeGuide.cs` (note: bug-by-comment, line 31 assigns `TvMazeId` to the `TvRage` field). `GetExistingWatchedStatus` scans the prior NFO so re-writes preserve `<watched>true</watched>`. Writes UTF-8 with declaration. |
| **Roksbox** | `Consumers/Roksbox/RoksboxMetadata.cs` | `"Roksbox"` | **No series metadata.** **Episode metadata** `Path.ChangeExtension(file, "xml")` — `<video>` root with `title, year, genre, actors (max 3), description, length, mpaa` (validated against a 16-rating allow-list at `:35`; falls back to `UNRATED`). **Series image** `<seriesFolderName>.jpg` at series root, sourced from `IMapCoversToLocal.GetCoverPath(series, Poster)`. **Season images** `<seasonFolderName>.jpg` inside each season folder. **Episode images** `Path.ChangeExtension(file, "jpg")` from the episode screenshot. | Season folder discovery via `^(season (?<season>\d+))\|(?<specials>specials)` regex, case-insensitive (`:34`). Season folder name is what the filename is derived from — relies on the user's existing folder naming. Migration 184 (`184_remove_invalid_roksbox_metadata_images.cs`) deleted rows where Roksbox episode images had been mistakenly placed inside a `metadata/` subfolder. |
| **WDTV** | `Consumers/Wdtv/WdtvMetadata.cs` | `"WDTV"` | **No series metadata.** **Episode metadata** `Path.ChangeExtension(file, "xml")` — `<details>` root with `id, title, series_name, episode_name, season_number(00), episode_number(00), firstaired, genre, actor, overview` (TODOs for credits/director). **Series image** `folder.jpg` at series root. **Season images** `folder.jpg` inside each season folder. **Episode images** `Path.ChangeExtension(file, "metathumb")`. | Same `^(season N)\|specials` regex as Roksbox (`:34`). `FindMetadataFile` switch — collisions between series-`folder.jpg` and season-`folder.jpg` are resolved by parent-folder regex. |
| **Plex** | `Consumers/Plex/PlexMetadata.cs` | `"Plex"` | **Series metadata** `.plexmatch` at series root: plain-text `Title:`, `Year:`, `TvdbId:`, `ImdbId:` lines. Optional `EpisodeMappings` adds `Episode: S{NN}{E…}: {relPath}` per episode file (special seasons → `SP{NN}`, multi-episode files concatenated like `S03E01-E02`). **No `.nfo` output. No series/season/episode images.** | `FindMetadataFile` only matches the literal relative path `.plexmatch`. Defaults: `SeriesPlexMatchFile=true`, `EpisodeMappings=false`. |

Settings classes (each is an `IProviderConfig` with `[FieldDefinition]` attributes that drive the UI):

- `XbmcMetadataSettings.cs` — `SeriesMetadata, SeriesMetadataEpisodeGuide (Advanced), SeriesMetadataUrl (Advanced), EpisodeMetadata, EpisodeImageThumb (Advanced), SeriesImages, SeasonImages, EpisodeImages` (defaults: all true except the two Advanced flags and `EpisodeImageThumb`).
- `RoksboxMetadataSettings.cs` — `EpisodeMetadata, SeriesImages, SeasonImages, EpisodeImages` (defaults all true). Help-texts contain literal example filenames (`Season##\filename.xml`, etc.).
- `WdtvMetadataSettings.cs` — same field set as Roksbox, all true by default.
- `PlexMetadataSettings.cs` — `SeriesPlexMatchFile=true, EpisodeMappings=false`.

All four use empty `AbstractValidator<T>` (no constraints).

## Subtitles

`Extras/Subtitles/SubtitleService.cs` — `Order = 1`. All five `CreateAfterX` methods return `Enumerable.Empty<SubtitleFile>` — subtitles are import-only, never generated. Only `MoveFilesAfterRename` and `ImportFiles` do work.

**Extension list** (`SubtitleFileExtensions.cs:11-29`) — case-insensitive `HashSet<string>` containing exactly: `.aqt .ass .idx .jss .psb .rt .smi .srt .ssa .sub .txt .utf .utf8 .utf-8 .vtt`. Note `.txt` is included; the importer relies on the filename matching the episode video to avoid grabbing notes / readme files.

**`CanImportFile`** (`:107-110`) — returns true iff the lower-cased extension is in the list.

**`ImportFiles`** (`:112-239`):
1. Split the candidate list into "matching" by two rules: (a) filename starts with the source file's basename (case-insensitive); (b) parsed-from-path `ParsedEpisodeInfo.SeasonNumber + EpisodeNumbers` equals the local episode's. Try (a) first.
2. **Single-video fallback** (`:155-186`): if no matches and the source folder has one *non-sample* video file (`IDetectSample.IsSample`), accept *all* filtered subs and warn `"Imported any available subtitle file for episode"`. Folders with >2 videos are skipped entirely.
3. For each match, build a `SubtitleFile` with `Language = LanguageParser.ParseSubtitleLanguage(file)`, `LanguageTags = LanguageParser.ParseLanguageTags(file)`, `Extension`, `RelativePath`. (Title/Copy come from the AggregationService when this is a script-imported run — see below.)
4. Group by `AggregateString` to detect "multiple copies of the same lang+tags+title" and assign `Copy = 1..N` to disambiguate.
5. For each, call base `ImportFile(... fileNameSuffix=GetSuffix(...))` to actually move the file with the computed suffix.

**`GetSuffix(language, copy, tags, multipleCopies, title=null)`** (`:241-275`) — produces e.g. `.<title>.<copy>.<two-letter-lang>.<tag>.<tag>`:

```
.{Title}            if title != null
[.{Copy}]            if multipleCopies (after title or alone)
.{IsoLang.TwoLetter} if Language != Unknown
.{tag1}.{tag2}…       if any LanguageTags
```

So a French SDH that's the second copy: `<base>.2.fr.sdh.srt`.

**`MoveFilesAfterRename`** (`:71-105`) regroups per-episode subs by `AggregateString`, recomputes `Copy` for groups with >1 entry, and uses the same `GetSuffix` to produce target names. The `Copy` value is sticky-preserved and re-issued sequentially when needed.

**`AggregateSubtitleInfo`** (`MediaFiles/EpisodeImport/Aggregation/Aggregators/AggregateSubtitleInfo.cs`) — populates `LocalEpisode.SubtitleInfo` (with `Language`, `LanguageTags`, `Title`, `Copy`) when the path being imported is itself a subtitle (this is the script-import case where a sub file goes through the normal episode-import pipeline, with `localEpisode.Path` actually being the `.srt`).

**`ImportedSubtitleFiles`** (`Subtitles/ImportedSubtitleFiles.cs`) — DTO with `SourceFiles : List<string>`, `SubtitleFiles : List<ExtraFile>`. Currently unused by anything in `Extras/` — *grep finds no consumer in `src/NzbDrone.Core` outside the file itself.* See EXTRAS-Q1.

**`ExistingSubtitleImporter`** (`Subtitles/ExistingSubtitleImporter.cs`) — `Order = 1`. Filters by extension (`:43`), then per file builds a `LocalEpisode` and runs `IAggregationService.Augment` to look up the matching episode (`AggregateEpisodes` etc. — see `mediafiles.md`). Skips files that match >1 episode-file. Pulls `SubtitleInfo` (from `AggregateSubtitleInfo`) for `Language/LanguageTags/Title/Copy` (`:83-87`).

## Others

`Extras/Others/OtherExtraService.cs` — `Order = 2`. All `CreateAfterX` are no-ops; this is import-only. **`CanImportFile` returns true unconditionally** (`:84-87`) — so it's the catch-all for any extension still in `Sonarr.ExtraFileExtensions` after Subtitle has had its turn.

**`ImportFiles`** (`:89-157`):
- Filter dup `.nfo` files: **only one** `.nfo` per episode, regardless of name (`hasNfo` flag at `:97`). This is the de-dup that prevents Sonarr from importing both a release-group `.nfo` and the rip's own `.nfo`.
- Filename-prefix match OR season+episode-number match (same logic as `SubtitleService` but no single-video fallback).
- Move via base `ImportFile(... extension=Path.GetExtension(file))`, `IMediaFileAttributeService.SetFilePermissions`, `Upsert`.

**`MoveFilesAfterRename`** (`:64-82`) is the simple move-with-same-extension flow.

**`OtherExtraFileRenamer`** (`Others/OtherExtraFileRenamer.cs`) — `IOtherExtraFileRenamer.RenameOtherExtraFile(series, path)`. Called from `MetadataService.ProcessEpisodeMetadata`/`ProcessSeriesImages`/`ProcessSeasonImages`/`ProcessEpisodeImages` *before* writing a metadata file/image. If the target path is currently tracked as an `OtherExtraFile`, rename it (and its DB row) to `<path>-orig`. Recycles a pre-existing `<path>-orig` first. **This is Sonarr's path-conflict resolution policy** (see "Path conflicts" below).

**`ExistingOtherExtraImporter`** (`Others/ExistingOtherExtraImporter.cs`) — `Order = 2`. Like the subtitle importer but accepts every extension (`:43-46`); skips files where Augmentation can't pin down a single episode-file.

## Existing-extra discovery flow

When a series is rescanned (`SeriesScannedEvent` from `DiskScanService.CompletedScanning`):

1. `DiskScanService` collects `possibleExtraFiles` — every non-video file under the series path (see `mediafiles.md` for the `GetNonVideoFiles` implementation).
2. `ExistingExtraFileService.Handle(SeriesScannedEvent)` (`:44-51`) calls `ImportExtraFiles(series, possibleExtraFiles, fileNameBeforeRename=null)`.
3. For each importer in Order [Metadata→Subtitle→Other]: `ProcessFiles(series, filesOnDisk, importedFiles, fileNameBeforeRename)` runs `FilterAndClean`:
   - `keepExistingEntries = (fileNameBeforeRename != null)` (`ImportExistingExtraFilesBase.cs:41`). For the `SeriesScannedEvent` case it's `null` → false → **`Clean` mode**.
   - `Clean` path: deletes rows whose path is also in `importedFiles` (already claimed by an earlier importer) AND rows whose file is missing from `filesOnDisk` (deleted off-disk).
   - Returns `previouslyImported` (rows pinning paths that we've already seen) + filtered `filesOnDisk` (paths the current importer should consider new).
4. The orchestrator concatenates each importer's returned files into `importedFiles` so the next importer's `Filter` sees them as taken.

The script-import call site (`ImportApprovedEpisodes.cs:158`) is the other path — invoked when a custom import script handles the move and Sonarr is just picking up the resulting on-disk shape. There `fileNameBeforeRename` is non-null, so importers run in **`keepExistingEntries=true`** mode: only rows whose path matches a file currently on disk get wiped, allowing the importer to rebuild them.

Inside each importer:
- **`ExistingMetadataImporter`** — skips `SubtitleFileExtensions` extensions for speed (`:47`). Tries every `IMetadata.FindMetadataFile`; for matches with `Type ∈ {EpisodeImage, EpisodeMetadata}`, parses an episode info from the path and uses `IAggregationService.Augment` to discover the owning `EpisodeFileId` + `SeasonNumber` (rejects files matching >1 episode-file).
- **`ExistingSubtitleImporter`** — same Augment pattern for `EpisodeFileId` / season; pulls `Language/Tags/Title/Copy` from `localEpisode.SubtitleInfo` (populated by `AggregateSubtitleInfo`).
- **`ExistingOtherExtraImporter`** — same pattern, no language / hash bookkeeping.

## DB tables touched

- `MetadataFiles` — schema (after all migrations, see also `migrations/ledger.md`):
  - `Id` (PK), `SeriesId NOT NULL`, `Consumer NOT NULL` (e.g. `"XbmcMetadata"`, `"PlexMetadata"`, `"WdtvMetadata"`, `"RoksboxMetadata"`), `Type INT NOT NULL` (`MetadataType` enum), `RelativePath NOT NULL`, `LastUpdated NOT NULL`, `SeasonNumber NULL`, `EpisodeFileId NULL`. Added later: `Added NULL` (m99), `Extension NOT NULL` (m99 backfilled then NOT NULL), `Hash NULL` (m50). Postgres timestamps `WITH TIME ZONE` per m188.
- `SubtitleFiles` — `Id, SeriesId, SeasonNumber, EpisodeFileId, RelativePath, Extension, Added, LastUpdated, Language INT NOT NULL` (m99); `LanguageTags STRING NULL` (m170, JSON-serialized list); `Title STRING NULL` (m198); `Copy INT DEFAULT 0` (m198).
- `ExtraFiles` (the **OtherExtraFile** table — name preserved from m99) — `Id, SeriesId, SeasonNumber, EpisodeFileId, RelativePath, Extension, Added, LastUpdated`.
- `Metadata` (the provider-definition table, m39) — `Enable, Name, Implementation, Settings (JSON), ConfigContract`. Standard `ProviderDefinition` columns.

Cross-cite: the housekeepers `CleanupOrphanedMetadataFiles`, `CleanupOrphanedSubtitleFiles`, `CleanupOrphanedExtraFiles`, `CleanupExtraFilesInExcludedFolders`, `CleanupAbsolutePathMetadataFiles`, `CleanupDuplicateMetadataFiles`, `DeleteBadMediaCovers` all live in `Housekeeping/Housekeepers/` and are documented in `system.md` (`system.md:383-405`).

Notable migrations affecting these tables:

| File | What it does |
|------|--------------|
| `039_add_metadata_tables.cs` | Initial `Metadata` + `MetadataFiles` schemas. |
| `041_fix_xbmc_season_images_metadata.cs` | Re-classifies XbmcMetadata rows where `SeasonNumber IS NOT NULL` from any prior `Type` to `Type=4` (SeasonImage). |
| `044_fix_xbmc_episode_metadata.cs` | Splits XbmcMetadata `EpisodeFileId IS NOT NULL` rows formerly tagged Type=4 into Type=2 (`.nfo`) vs Type=5 (anything else). |
| `050_add_hash_to_metadata_files.cs` | Adds `Hash` column. |
| `099_extra_and_subtitle_files.cs` | Creates `ExtraFiles` and `SubtitleFiles` tables; backfills `MetadataFiles.Extension` from `RelativePath`. |
| `103_fix_metadata_file_extensions.cs` | Trims `Extension` column to last `.`-suffix only. |
| `108_fix_extra_file_extension.cs` | Drops `ExtraFiles` rows with empty `Extension`; same trimming for `ExtraFiles` and `SubtitleFiles`. |
| `109_import_extra_files.cs` | Auto-enables `ImportExtraFiles=True` if user already had `ExtraFileExtensions` set. |
| `110_fix_extra_files_config.cs` | Normalizes `importextrafiles` value to `'True'`; defaults `extrafileextensions = 'srt'` when empty. |
| `124_remove_media_browser_metadata.cs` | Hard-deletes the `MediaBrowserMetadata` provider rows + its file rows. |
| `170_add_language_tags_to_subtitle_files.cs` | Adds `SubtitleFiles.LanguageTags`. |
| `184_remove_invalid_roksbox_metadata_images.cs` | Deletes Roksbox `Type=5` rows whose `RelativePath` is under a `metadata/` subfolder. SQLite vs PG path-separator branching. |
| `188_postgres_update_timestamp_columns_to_with_timezone.cs` | Adds TZ to PG timestamps across many tables, including these three. |
| `195_parse_language_tags_from_existing_subtitle_files.cs` | Backfills `LanguageTags` for existing `SubtitleFiles` rows from path. |
| `198_parse_titles_from_existing_subtitle_files.cs` | Adds `Title`, `Copy`; backfills both from path + episode file path. |

## Events raised

None. `ExtraService` and the family services are pure consumers (and DB writers).

## Events consumed

| Event | Handler | Behavior |
|-------|---------|----------|
| `MediaCoversUpdatedEvent` | `ExtraService.Handle` (`:118`) | `CreateAfterMediaCoverUpdate` per manager — rewrites images. |
| `EpisodeFolderCreatedEvent` | `ExtraService.Handle` (`:142`) | `CreateAfterEpisodeFolder` per manager. |
| `SeriesScannedEvent` | `ExtraService.Handle` (`:131`) AND `ExistingExtraFileService.Handle` (`:44`) | Two-handler split: existing-importer claims pre-existing on-disk extras; producer regenerates writer outputs. |
| `SeriesRenamedEvent` | `ExtraService.Handle` (`:162`) | `MoveFilesAfterRename` per manager. |
| `DownloadsProcessedEvent` | `ExtraService.Handle` (`:173`) | Drains the post-import queue. |
| `SeriesDeletedEvent` | `ExtraFileService<TExtraFile>.HandleAsync` (`:98`) for each of the 3 families | Bulk-deletes rows for the deleted series ids. **No disk cleanup** — assumes the series folder is being removed by `SeriesService` per `Series.Path` deletion (or that the user wanted the files kept). |
| `EpisodeFileDeletedEvent` | `ExtraFileService<TExtraFile>.Handle` (`:104`) for each of the 3 families | Recycles the sidecar files to the recycle bin and deletes the rows — except when `Reason == NoLinkedEpisodes` (orphan cleanup) which only deletes rows. |

## Config keys

Stored in the `Config` table; surfaced in `IConfigService` (`Configuration/ConfigService.cs:237-249`):

- **`ImportExtraFiles`** (boolean, default `false`) — master toggle for the consumer side. When false, `ExtraService.ImportExtraFiles` early-returns (`:63-66`). Existing-extra discovery on rescan still runs (it's gated by event subscription, not this flag). Surfaced via `Sonarr.Api.V3/Config/MediaManagementConfigResource.cs:30` and `Sonarr.Api.V5/Settings/MediaManagementSettingsResource.cs:30`.
- **`ExtraFileExtensions`** (string CSV, default `"srt"`) — comma-separated extensions to consider during import. Each token is `.`-trimmed and re-prefixed with a leading `.` before comparison (`ExtraService.cs:70-73`). Surfaced via the same V3/V5 resources.
- **`CopyUsingHardlinks`** — used by `ExtraFileManager.ImportFile` (`:74`) to choose `HardLinkOrCopy` vs `Copy` when `readOnly` is set. Owned by MediaFiles config.
- Each enabled metadata writer's settings live as JSON in the `Metadata.Settings` column (`MetadataDefinition`).

There is **no** per-extension allow-list for subtitles; `SubtitleFileExtensions` is a hard-coded constant.

## Jobs / commands

The Extras subsystem has no `IExecute<TCommand>` handlers — its work is event-driven only. Indirectly:

- `RescanSeriesCommand` (Tv) → `DiskScanService` → `SeriesScannedEvent` → both `ExtraService` and `ExistingExtraFileService`.
- `RenameSeriesCommand` (MediaFiles) → `RenameEpisodeFileService.RenameFiles` → `SeriesRenamedEvent` → `ExtraService`.
- `RefreshMonitoredDownloadsCommand` (Download) → eventually `DownloadsProcessedEvent` → `ExtraService` drains queue.
- `RefreshMediaCoversCommand` (MediaCover) → `MediaCoversUpdatedEvent` → `ExtraService`.
- `HousekeepingCommand` (system) → `CleanupOrphanedMetadataFiles`, `CleanupOrphanedSubtitleFiles`, `CleanupOrphanedExtraFiles`, `CleanupExtraFilesInExcludedFolders`, `CleanupAbsolutePathMetadataFiles`, `CleanupDuplicateMetadataFiles`, `DeleteBadMediaCovers`. (See `system.md`.)

## External services

None directly. `MetadataService.DownloadImage` does HTTP downloads via `IHttpClient` (`MetadataService.cs:475-477`) but the URLs come from `IMapCoversToLocal.GetCoverPath` / `Image.RemoteUrl` populated by the MediaCover and Skyhook subsystems. The Xbmc consumer hardcodes `https://www.thetvdb.com/?tab=series&id=<TvdbId>` as a fallback URL line in the NFO when `SeriesMetadataUrl` is enabled (`XbmcMetadata.cs:272`).

## Path conflicts / overwrite policy

Three different rules:

1. **Metadata producer vs `OtherExtraFile`** — when `MetadataService` is about to write to a path that's tracked as an `OtherExtraFile` row, `OtherExtraFileRenamer.RenameOtherExtraFile` renames it to `<path>-orig` and updates the DB row's `RelativePath` and `Extension` (extension becomes `.<ext>-orig`). If a `<path>-orig` already exists tracked, the prior `-orig` is recycled to the recycle bin (not just overwritten). (`Others/OtherExtraFileRenamer.cs:36-77`.) **Sonarr never silently overwrites a tracked extra file** — it always preserves the prior version under `-orig`.
2. **Metadata producer vs untracked file on disk** — `ProcessSeriesImages`/`ProcessSeasonImages`/`ProcessEpisodeImages` call `_diskProvider.FileExists(fullPath)` and *skip* if true. (`MetadataService.cs:350`, `:387`, `:425`.) So a hand-placed `poster.jpg` is never overwritten.
3. **Metadata content unchanged** — `Hash` comparison short-circuits the disk write (`MetadataService.cs:265-275`, `:329-332`).

The duplicate-row resolver (`MetadataService.GetMetadataFile`) keeps the **first** matching row and recycle-bins the rest along with their files (`:513-535`).

The `MetadataService.ProcessEpisodeImages` path has a subtle bug: when an existing row's `RelativePath` differs from the freshly-computed one, it returns a 1-element list instead of continuing to add more — see EXTRAS-Q4.

## Per-OS quirks

- **Path comparisons** consistently use `PathExtensions.PathNotEquals` / `PathEqualityComparer.Instance` (e.g. `MetadataService.cs:209`, `ImportExistingExtraFilesBase.cs:30,44-46`) — these come from `NzbDrone.Common` and use OS-aware normalization (case-insensitive on Windows). The Go port needs an equivalent.
- **No direct Mono / Windows interop** — search for `IExtraFile*`, `MetadataFile`, `SubtitleFile`, `OtherExtraFile` in `NzbDrone.Mono`/`NzbDrone.Windows` returns nothing. Indirect dependence: `IDiskTransferService` (handles hardlinks per OS), `IMediaFileAttributeService.SetFilePermissions` (chmod on Linux), `IRecycleBinProvider` (uses `~/.Sonarr/Backup/Recycle` style folder).
- **Migration 184** branches SQLite vs Postgres on path-separator escaping (`%\\\\metadata\\\\%` vs `%\metadata\%`).
- **Subtitle extension matching** — `SubtitleFileExtensions.Extensions` is `StringComparer.OrdinalIgnoreCase` (`SubtitleFileExtensions.cs:12`) so `.SRT` matches `.srt`. `ExtraService` lower-cases inside `CanImportFile` calls (`SubtitleService.cs:109`).

## Test files (the port must keep passing equivalents)

Source: `src/NzbDrone.Core.Test/Extras/`.

- `ExtraServiceFixture.cs`
- `Others/OtherExtraServiceFixture.cs`
- `Subtitles/SubtitleServiceFixture.cs`
- `Metadata/Consumers/Roksbox/FindMetadataFileFixture.cs`
- `Metadata/Consumers/Wdtv/FindMetadataFileFixture.cs`
- `Metadata/Consumers/Xbmc/FindMetadataFileFixture.cs`

Notable cross-subsystem test concerns: episode/file aggregation tests under `MediaFiles/EpisodeImport/Aggregation/` (subtitle title parsing fixtures) and the `Housekeeping` fixtures for the orphan cleaners.

## Notable quirks / TODOs / HACKs found in source

- `MetadataService.cs:195` — `// TODO: Move EpisodeImage and EpisodeMetadata metadata files, instead of relying on consumers to do it (Xbmc's EpisodeImage is more than just the extension)`. The current code asks each `IMetadata.GetFilenameAfterMove` to compute the move target; the comment proposes centralizing it. Port should preserve current behavior verbatim.
- `KodiEpisodeGuide.cs:31` — `TvRage = series.TvRageId > 0 ? series.TvMazeId.ToString() : null;` — looks like a bug (uses `TvMazeId` for the `TvRage` field). The Go port should *replicate this exactly* unless we explicitly decide to change writer output (which would require user-visible NFO churn). See EXTRAS-Q5.
- `OtherExtraService.cs:97-112` — only one `.nfo` per import burst; the dedup is per-call, not per-episode-file. If multiple episodes import in the same call this still drops the second-and-later `.nfo`. See EXTRAS-Q2.
- `SubtitleService.cs:155-186` — single-video-fallback "import any sub": only triggers when `videoFiles.Count == 1` after sample filtering, but the precondition `videoFiles.Count > 2` early-returns. The 2-video case is silently dropped without import.
- `ExtraFileService<T>.Handle(EpisodeFileDeletedEvent)` (`:104-130`) — recycles to the recycle bin even when the file lives in a different parent than the series root; subfolder is computed from the parent of the parent, which can produce odd recycle paths if the extra is nested deep. See EXTRAS-Q3.
- `MetadataDefinition` has zero unique columns and no `Tags`/`OnX` toggles — unlike `NotificationDefinition`. The `Enable` flag is the only switch; per-event handling is implicit in which `CreateAfterX` runs.
- `MetadataService.MoveFilesAfterRename` iterates `GetAvailableProviders()` (not `Enabled()`) — disabled providers' rows still get moved. This is intentional so a user toggling Xbmc off doesn't strand the existing NFOs.
- `ExtraService.ImportExtraFiles` recursion: `folderSearchOption = localEpisode.FolderEpisodeInfo != null` (`:68`). I.e. if the import was a single-file (download is the video itself, not a folder), only the immediate parent of the source video is scanned; if the import was a folder, recursion into subfolders happens. The semantics are inherited from `IDiskProvider.GetFiles(path, recursive)`.
- `OtherExtraFileRenamer` only takes action if a row in `OtherExtraFile` already tracks the target path. Untracked files (e.g. user-placed `poster.jpg` that was never imported) are *skipped*, leaving the producer's `FileExists` check to decline the write.
- The `Metadata` provider table is **never deleted from** by the factory's `InitializeProviders` — only inserts. Removed providers persist as orphan rows until a manual SQL migration (cf. m124 for MediaBrowser).
- `MetadataFile.Hash` is `null` for `MetadataType.SeriesImage`/`SeasonImage`/`EpisodeImage` rows — only set for `SeriesMetadata`/`EpisodeMetadata`. Image files are de-duped purely by `RelativePath` + `disk-exists` check.

## API surface (V3 + V5)

- `Sonarr.Api.V3/Metadata/MetadataController.cs` + `MetadataResource.cs` — provider CRUD against `IMetadataFactory`.
- `Sonarr.Api.V5/Metadata/MetadataController.cs` + `MetadataResource.cs` + `MetadataBulkResource.cs`.
- `Sonarr.Api.V3/Config/MediaManagementConfigResource.cs:30,31,63,64` and `Sonarr.Api.V5/Settings/MediaManagementSettingsResource.cs:30,31,63,64` — `ImportExtraFiles` + `ExtraFileExtensions` round-trip.

There is no public REST endpoint for direct `MetadataFile`/`SubtitleFile`/`OtherExtraFile` rows; they're internal accounting.

## Open questions

- **EXTRAS-Q1** — `Subtitles/ImportedSubtitleFiles.cs` defines `SourceFiles` + `SubtitleFiles` but no callers exist anywhere in `src/NzbDrone.Core` (verified by full-tree grep). Was this DTO retired? Should the Go port omit it? Status: unresolved (blocks Phase 4 / cleanup pass).
- **EXTRAS-Q2** — `OtherExtraService.ImportFiles` uses a single `hasNfo` flag for the entire `files` list, not keyed by episode. When multiple episodes are imported in the same burst, only the first gets a `.nfo`. Is this intended, or should de-dup be per `EpisodeFile`? Status: unresolved (blocks Phase 1 — affects test parity).
- **EXTRAS-Q3** — `ExtraFileService<T>.Handle(EpisodeFileDeletedEvent)` computes the recycle subfolder as `_diskProvider.GetParentFolder(series.Path).GetRelativePath(_diskProvider.GetParentFolder(path))` (`:123`). For an extra deeply nested under a season folder, this yields the season folder's name; for one at series root, it yields the series folder name. The asymmetry is undocumented. Status: unresolved (blocks Phase 4 / parity check).
- **EXTRAS-Q4** — `MetadataService.ProcessEpisodeImages` (`:417-466`) has `return new List<MetadataFile> { existingMetadata };` inside the per-image loop when an existing row's path differs (`:444`). This short-circuits the rest of the consumer's image list. Bug or intentional? Status: unresolved (blocks Phase 1 — port should preserve unless we file a bug).
- **EXTRAS-Q5** — `KodiEpisodeGuide.cs:31` populates `TvRage` from `series.TvMazeId`. Replicate exactly, or fix in the Go port (which would change emitted NFOs)? Status: unresolved (blocks Phase 1 — user-visible output change).
- **EXTRAS-Q6** — `MetadataFactory.InitializeProviders` never deletes orphan rows; obsolete writers (cf. `MediaBrowserMetadata` deleted by m124) require a manual migration. Should the Go port handle obsoleting at the factory level (e.g. by `Implementation`-name allow-list at startup)? Status: unresolved (blocks Phase 2 — affects migration strategy).
- **EXTRAS-Q7** — `SubtitleService.ImportFiles` "single-video fallback" silently skips folders with exactly 2 videos (line `:162` early-returns on `> 2` but the `== 2` case lands in the `videoFiles.Count == 1` test which fails). Is the 2-video case intentional drop or a bug? Status: unresolved (blocks Phase 4 / parity).
- **EXTRAS-Q8** — `Sonarr.ExtraFileExtensions` default of `"srt"` is redundant with the hard-coded `SubtitleFileExtensions` list (subtitles get matched twice — once by the user's CSV gate, once by `SubtitleService.CanImportFile`). Documented intent? Or vestigial from before subtitles split out into their own family? Status: unresolved (blocks Phase 2 — affects config UX).
- **EXTRAS-Q9** — `ExistingExtraFileService.Handle(SeriesScannedEvent)` and `ExtraService.Handle(SeriesScannedEvent)` are **separate** handlers on the same event. Order between handlers across services is not guaranteed by `IEventAggregator` (cf. `common.md` if it covers ordering). If `ExtraService` rebuilds metadata before `ExistingExtraFileService` claims the on-disk NFOs, a race could leave duplicate rows that `MetadataService.GetMetadataFile` then has to recycle. Status: unresolved (blocks Phase 1 — port must reproduce or fix).
- **EXTRAS-Q10** — `MetadataFile.Consumer` is stored as the C# class name (e.g. `"XbmcMetadata"`, `"PlexMetadata"`). When the port renames consumer types in Go, an upgrade migration must rewrite this column. Capture the canonical mapping in `migrations/ledger.md`. Status: unresolved (blocks Phase 2 — migration ledger entry).
