# MediaFiles

**Source:** `src/NzbDrone.Core/MediaFiles/**`
**Target Go package:** `go/internal/mediafiles/`
**Status:** drafted

## Purpose
Owns the on-disk lifecycle of every video file Sonarr manages. Discovers files
under a series root (`DiskScanService`), runs the import decision pipeline that
matches files to episodes, transfers files (move/copy/hardlink) into the
configured layout via `EpisodeFileMovingService`, persists the resulting
`EpisodeFile` rows, extracts media metadata via FFprobe
(`VideoFileInfoReader` → FFMpegCore), supports rename previews / batch renames,
and handles deletion (recycle-bin aware). It is the boundary between Sonarr's
metadata model and the filesystem; consumers include Organizer (for layout),
History, Extras, MediaCover, Notifications, and the V3/V5 REST controllers.

## Public types
| Type | File | Kind | Notes |
|------|------|------|-------|
| `EpisodeFile` | `EpisodeFile.cs` | model (`ModelBase`) | DB row. Fields: `Id`, `SeriesId`, `SeasonNumber`, `RelativePath`, `Path` (transient), `Size`, `DateAdded`, `OriginalFilePath`, `SceneName`, `ReleaseGroup`, `ReleaseHash`, `Quality` (`QualityModel`), `IndexerFlags`, `MediaInfo` (`MediaInfoModel`), `Episodes` (`LazyLoaded<List<Episode>>`), `Series` (`LazyLoaded<Series>`), `Languages` (`List<Language>`), `ReleaseType`. `GetSceneOrFileName()` helper. |
| `MediaInfoModel` | `MediaInfo/MediaInfoModel.cs` | `IEmbeddedDocument` | Stored as JSON column on EpisodeFile. Fields: `RawStreamData`, `SchemaRevision`, `ContainerFormat`, `VideoFormat`, `VideoCodecID`, `VideoProfile`, `VideoBitrate`, `VideoBitDepth`, `VideoColourPrimaries`, `VideoTransferCharacteristics`, `VideoHdrFormat` (`HdrFormat`), `Height`, `Width`, `RunTime` (`TimeSpan`), `VideoFps`, `AudioStreams`, `SubtitleStreams`, `ScanType`, `Title` (JsonIgnore). |
| `MediaInfoAudioStreamModel` | `MediaInfo/MediaInfoAudioStreamModel.cs` | embedded | `Language`, `Format`, `CodecId`, `Profile`, `Bitrate`, `Channels`, `ChannelPositions`, `Title`. |
| `MediaInfoSubtitleStreamModel` | `MediaInfo/MediaInfoSubtitleStreamModel.cs` | embedded | `Language`, `Format`, `Title`, `Forced`, `HearingImpaired`. |
| `HdrFormat` | `MediaInfo/HdrFormat.cs` | enum | `None, DolbyVision, DolbyVisionHdr10, DolbyVisionHdr10Plus, DolbyVisionSdr, DolbyVisionHlg, Hdr10, Hdr10Plus, Hlg10, Pq10`. |
| `DeleteMediaFileReason` | `DeleteMediaFileReason.cs` | enum | `MissingFromDisk, Manual, Upgrade, NoLinkedEpisodes, ManualOverride`. |
| `DeletedEpisodeFile` | `DeletedEpisodeFile.cs` | record | `EpisodeFile`, `RecycleBinPath`. |
| `EpisodeFileMoveResult` | `EpisodeFileMoveResult.cs` | DTO | `EpisodeFile`, `OldFiles: List<DeletedEpisodeFile>`. |
| `RenameEpisodeFilePreview` | `RenameEpisodeFilePreview.cs` | DTO | `SeriesId`, `SeasonNumber`, `EpisodeNumbers`, `EpisodeFileId`, `ExistingPath`, `NewPath`. |
| `RenamedEpisodeFile` | `RenamedEpisodeFile.cs` | DTO | `EpisodeFile`, `PreviousRelativePath`, `PreviousPath`. |
| `FileDateType` | `FileDateType.cs` | enum | `None, LocalAirDate, UtcAirDate`. |
| `SeasonPackUpgradeType` | `SeasonPackUpgradeType.cs` | enum | used by upgrade specification. |
| `ScriptImportDecision` | `ScriptImportDecision.cs` | enum | `MoveComplete, RenameRequested, DeferMove`. |
| `ScriptImportInfo` | `ScriptImportInfo.cs` | DTO | parsed script output. |
| `ImportDecision` | `EpisodeImport/ImportDecision.cs` | DTO | `LocalEpisode`, `Rejections`, `Approved` (computed). |
| `ImportRejection` / `ImportRejectionReason` | `EpisodeImport/ImportRejection*.cs` | enum/DTO | 30+ reason codes (see source). |
| `ImportSpecDecision` | `EpisodeImport/ImportSpecDecision.cs` | DTO | spec result wrapper. |
| `ImportResult` / `ImportResultType` | `EpisodeImport/ImportResult*.cs` | DTO/enum | `Imported, Rejected, Skipped`. |
| `ImportMode` | `EpisodeImport/ImportMode.cs` | enum | `Auto=0, Move=1, Copy=2`. |
| `DetectSampleResult` | `EpisodeImport/DetectSampleResult.cs` | enum | `Sample, NotSample, Indeterminate`. |
| `EpisodeTitleRequiredType` | `EpisodeImport/EpisodeTitleRequiredType.cs` | enum | spec config. |
| `ManualImportItem` | `EpisodeImport/Manual/ManualImportItem.cs` | DTO | UI row for manual import flow. |
| `ManualImportFile` | `EpisodeImport/Manual/ManualImportFile.cs` | DTO | command input. |
| `ManuallyImportedFile` | `EpisodeImport/Manual/ManuallyImportedFile.cs` | DTO | tracked-download result. |
| `MediaFileExtensions` | `MediaFileExtensions.cs` | static | extension → default Quality map (`webm`, `m4v`, `mkv` (HDTV720p), `m2ts` (Bluray720p), iso/img/vob = "Disk"). |
| `FileExtensions` | `FileExtensions.cs` | static | Archive / Dangerous / Executable extension sets and `RemoveFileExtension`. |
| `SameFilenameException` | `SameFilenameException.cs` | exception | source==destination on transfer. |
| `RootFolderNotFoundException` | `EpisodeImport/RootFolderNotFoundException.cs` | exception | abort upgrade if root missing. |
| `RecycleBinException` | `EpisodeImport/RecycleBinException.cs` | exception | failure routing to recycle bin. |
| `AggregationFailedException` / `AugmentingFailedException` | `EpisodeImport/Aggregation/AggregationFailedException.cs` | exception | parsing/aggregation failure. |

## Public services
| Type | Methods | Side effects | Notes |
|------|---------|--------------|-------|
| `IMediaFileService` / `MediaFileService` (`MediaFileService.cs`) | `Add`, `Update(single/list)`, `Delete(file, reason)`, `Get`, `GetFilesBySeries`, `GetFilesBySeriesIds`, `GetFilesBySeason`, `GetFiles(ids)`, `GetFilesWithoutMediaInfo`, `GetFilesWithRelativePath`, `FilterExistingFiles` | DB writes; raises `EpisodeFileAddedEvent`, `EpisodeFileDeletedEvent`. Handles `SeriesDeletedEvent` to bulk-delete rows. | `Delete` mutates `episodeFile.Path` to combined absolute path before publishing event (`MediaFileService.cs:60-68` — comment "Little hack"). |
| `IMediaFileRepository` / `MediaFileRepository` (`MediaFileRepository.cs`) | `GetFilesBySeries/Ids/Season`, `GetFilesWithoutMediaInfo`, `GetFilesWithRelativePath`, `DeleteForSeries` | SQL via `BasicRepository<EpisodeFile>`. | Standard Dapper repo. |
| `IDiskScanService` / `DiskScanService` (`DiskScanService.cs`) | `Scan(series)`, `GetVideoFiles(path,recursive)`, `GetNonVideoFiles`, `FilterPaths`. Executes `RescanSeriesCommand`. | Disk reads, calls `MediaFileTableCleanupService`, runs import decision pipeline, calls `UpdateMediaInfoService`. Raises `SeriesScannedEvent`, `SeriesScanSkippedEvent`. | Static excluded-folder regexes at `DiskScanService.cs:72-75`. |
| `IMakeImportDecision` / `ImportDecisionMaker` (`EpisodeImport/ImportDecisionMaker.cs`) | `GetImportDecisions(...)` (overloads), `GetDecision(localEpisode, downloadClientItem)` | Calls all `IImportDecisionEngineSpecification` instances. | Wraps `ParsePath`, `AggregationService.Augment`, sample-count, custom-format calc. |
| `IImportApprovedEpisodes` / `ImportApprovedEpisodes` (`EpisodeImport/ImportApprovedEpisodes.cs`) | `Import(decisions, newDownload, downloadClientItem, importMode)` | Calls `UpgradeMediaFileService`, `MediaFileService.Add`, `ExtraService`, `HistoryService`, raises `EpisodeImportedEvent` / `EpisodeImportFailedEvent`, may push `RescanSeriesCommand` on `DestinationAlreadyExistsException`. | Sorts by quality (per series quality profile), then size, then min episode #. |
| `IDownloadedEpisodesImportService` / `DownloadedEpisodesImportService` (`DownloadedEpisodesImportService.cs`) | `ProcessRootFolder`, `ProcessPath`, `ShouldDeleteFolder` | Calls `ParsingService`, `DiskScanService`, `ImportDecisionMaker`, `ImportApprovedEpisodes`. Strips `_UNPACK_`/`_FAILED_` from folder names. | Rejects dangerous/executable extensions, multi-season, locked files, `._` macOS files. |
| `DownloadedEpisodesCommandService` (`DownloadedEpisodesCommandService.cs`) | Executes `DownloadedEpisodesScanCommand` | Forwards to `DownloadedEpisodesImportService`, calls `CompletedDownloadService.VerifyImport`. | |
| `IMoveEpisodeFiles` / `EpisodeFileMovingService` (`EpisodeFileMovingService.cs`) | `MoveEpisodeFile(file, series)`, `MoveEpisodeFile(file, localEpisode)`, `CopyEpisodeFile(file, localEpisode)` | `IDiskTransferService.TransferFile`, calls `BuildFileNames`, `ScriptImportDecider.TryImport`, `MediaFileAttributeService`, raises `EpisodeFolderCreatedEvent`. | Throws `RootFolderNotFoundException`, `SameFilenameException`, `FileNotFoundException`. |
| `IUpgradeMediaFiles` / `UpgradeMediaFileService` (`UpgradeMediaFileService.cs`) | `UpgradeEpisodeFile(file, localEpisode, copyOnly)` | Recycle-bins prior file via `RecycleBinProvider`, calls `MediaFileService.Delete(..., Upgrade)`, then `EpisodeFileMover.Move/Copy`. | Uses `CopyUsingHardlinks` config to choose `HardLinkOrCopy`. |
| `IDeleteMediaFiles` / `MediaFileDeletionService` (`MediaFileDeletionService.cs`) | `DeleteEpisodeFile(series, file)`. Executes `DeleteSeriesFilesCommand`. Handles `SeriesDeletedEvent`, `EpisodeFileDeletedEvent`. | Routes to `RecycleBinProvider.DeleteFile/Folder`, raises `DeleteCompletedEvent`. Handler removes empty subfolders / series folder when `DeleteEmptyFolders=true`. | Rejects when root folder missing/empty (HTTP 409). |
| `IRecycleBinProvider` / `RecycleBinProvider` (`RecycleBinProvider.cs`) | `DeleteFile(path, subfolder)`, `DeleteFolder(path)`, `Empty()`, `Cleanup()`. Executes `CleanUpRecycleBinCommand`. | Either permanently deletes (no recycle-bin configured) or transfers to `RecycleBin` config path; preserves last-write-time; auto-renames on collision (`name_2.ext`). | `Cleanup` honours `RecycleBinCleanupDays` config. |
| `IMediaFileTableCleanupService` / `MediaFileTableCleanupService` (`MediaFileTableCleanupService.cs`) | `Clean(series, filesOnDisk)` | Deletes orphaned `EpisodeFile` rows (missing from disk, no linked episodes); zeros `Episode.EpisodeFileId` when DB references vanished file. | Path comparison via `PathEqualityComparer`. |
| `IMediaFileAttributeService` / `MediaFileAttributeService` (`MediaFileAttributeService.cs`) | `SetFilePermissions`, `SetFolderPermissions`, `SetFolderLastWriteTime` | Windows: `InheritFolderPermissions`. Non-Windows: chmod/chown if `SetPermissionsLinux`. | |
| `IUpdateEpisodeFileService` / `UpdateEpisodeFileService` (`UpdateEpisodeFileService.cs`) | `ChangeFileDateForFile(file, series, episodes)`. Handles `SeriesScannedEvent`. | Calls `IDiskProvider.FileSetLastWriteTime`. Has subsecond preservation hack referencing Sonarr issue #7228. | Floors to epoch on Linux for pre-1970 dates. |
| `IUpdateMediaInfo` / `UpdateMediaInfoService` (`MediaInfo/UpdateMediaInfoService.cs`) | `Update(file, series)`, `UpdateMediaInfo(file, series)`. Handles `SeriesScannedEvent`. | Reads via `VideoFileInfoReader`, persists via `MediaFileService.Update`. Re-runs when `SchemaRevision < MINIMUM`. | Gated by `EnableMediaInfo` config. |
| `IVideoFileInfoReader` / `VideoFileInfoReader` (`MediaInfo/VideoFileInfoReader.cs`) | `GetMediaInfo(filename)`, `GetRunTime(filename)` | Calls `FFProbe.Analyse(...)` from FFMpegCore (process spawn). Configures `GlobalFFOptions.BinaryFolder = AppDomain.BaseDirectory`. | Does PQ frame-probe pass for HDR detection. Skips `.iso/.img/.vob`. Schema revision const = 14. |
| `IRenameEpisodeFileService` / `RenameEpisodeFileService` (`RenameEpisodeFileService.cs`) | `GetRenamePreviews(seriesId)`, `(seriesId, season)`, `(seriesIds[])`. Executes `RenameFilesCommand`, `RenameSeriesCommand`. | Calls `BuildFileNames.BuildFilePath`, `EpisodeFileMover.MoveEpisodeFile`, `MediaFileService.Update`. Raises `EpisodeFileRenamedEvent`, `SeriesRenamedEvent`, `RenameCompletedEvent`. | |
| `IImportScript` / `ImportScriptService` (`ScriptImportDecider.cs`) | `TryImport(source, dest, localEpisode, episodeFile, mode)` | Spawns external script via `IProcessProvider.StartAndCapture`, marshals 30+ `Sonarr_*` env vars (series, episode, mediaInfo). Parses stdout via `OutputRegex`. | Returns `MoveComplete | RenameRequested | DeferMove`. Throws `ScriptImportException` on non-zero exit / invalid output. |
| `IAggregationService` / `AggregationService` (`EpisodeImport/Aggregation/AggregationService.cs`) | `Augment(localEpisode, downloadClientItem)` | Calls each `IAggregateLocalEpisode` augmenter in `Order`, calls `VideoFileInfoReader.GetMediaInfo` when applicable, computes `SceneName` via `SceneNameCalculator`. | Augmenters: `AggregateEpisodes`, `AggregateLanguage`, `AggregateQuality`, `AggregateReleaseGroup`, `AggregateReleaseHash`, `AggregateReleaseInfo`, `AggregateSubtitleInfo`. Plus language/quality augmenters under `Aggregators/Augmenters/`. |
| `IDetectSample` / `DetectSample` (`EpisodeImport/DetectSample.cs`) | `IsSample(series, path, isSpecial)`, `IsSample(localEpisode)` | Reads runtime via FFprobe; thresholds: 15s for ≤3min, 90s for ≤10min, 5min for ≤30min, else 10min. | Skips `.flv`, `.strm`, specials. |
| `ILocalEpisodeCustomFormatCalculationService` / `LocalEpisodeCustomFormatCalculationService` (`EpisodeImport/LocalEpisodeCustomFormatCalculationService.cs`) | `UpdateEpisodeCustomFormats`, `ParseEpisodeCustomFormats` | Calls custom formats engine. | |
| `ITorrentFileInfoReader` / `TorrentFileInfoReader` (`TorrentInfo/TorrentFileInfoReader.cs`) | `GetHashFromTorrentFile(bytes)` | Parses via MonoTorrent, returns hex hash. | (Lives in MediaFiles dir but really download-client utility.) |
| `IManualImportService` / `ManualImportService` (`EpisodeImport/Manual/ManualImportService.cs`) | `GetMediaFiles`, `ReprocessItem`, executes `ManualImportCommand` | Bridges UI → import pipeline; raises `UntrackedDownloadCompletedEvent` and `DownloadCompletedEvent`. | |

### Import decision specifications (`EpisodeImport/Specifications/*.cs`)
All implement `IImportDecisionEngineSpecification.IsSatisfiedBy(LocalEpisode, DownloadClientItem)`:
`AbsoluteEpisodeNumberSpecification`, `AlreadyImportedSpecification`, `EpisodeTitleSpecification`, `FreeSpaceSpecification`, `FullSeasonSpecification`, `HasAudioTrackSpecification`, `MatchesFolderSpecification`, `MatchesGrabSpecification`, `NotSampleSpecification`, `NotUnpackingSpecification`, `SameEpisodesImportSpecification`, `SplitEpisodeSpecification`, `UnverifiedSceneNumberingSpecification`, `UpgradeSpecification`. Each maps to one `ImportRejectionReason`.

## DB tables touched
- `EpisodeFiles` — read/write (`MediaFileRepository`). `MediaInfo` column = JSON (IEmbeddedDocument), `Quality`, `Languages`, `IndexerFlags`, `ReleaseType` likewise serialized.
- `Episodes` — write via `IEpisodeService.UpdateEpisode` when zeroing `EpisodeFileId` on missing file (`MediaFileTableCleanupService.cs:81`).
- `History` — read in `ImportApprovedEpisodes` via `IHistoryService.FindByDownloadId`.

## Events raised (`IEventAggregator`)
| Event | Payload | Producer | Consumers (within MediaFiles + cross-subsystem) |
|------|---------|----------|-------------------------------------------------|
| `EpisodeFileAddedEvent` | `EpisodeFile` | `MediaFileService.Add` | History, Notifications, MediaCover, Extras (cross-subsystem). |
| `EpisodeFileDeletedEvent` | `EpisodeFile`, `DeleteMediaFileReason` | `MediaFileService.Delete` | `MediaFileDeletionService.Handle` (empty-folder cleanup; `EventHandleOrder.Last`); History; Notifications; AutoTaggingService. |
| `EpisodeFileRenamedEvent` | `Series`, `EpisodeFile`, `OriginalPath` | `RenameEpisodeFileService.RenameFiles` | History, Notifications, Extras, MediaCover. |
| `EpisodeFolderCreatedEvent` | `Series`, `EpisodeFile`, `SeriesFolder?`, `SeasonFolder?`, `EpisodeFolder?` | `EpisodeFileMovingService.EnsureEpisodeFolder` | Permission/owner setting downstream. |
| `EpisodeImportedEvent` | `LocalEpisode`, `EpisodeFile`, `OldFiles[]`, `NewDownload`, `DownloadClientInfo`, `DownloadId` | `ImportApprovedEpisodes.Import` | History (Grabbed→Imported transition), Notifications (`OnDownload`/`OnUpgrade`), DownloadHistory, Extras. |
| `EpisodeImportFailedEvent` | `Exception`, `LocalEpisode`, `NewDownload`, `DownloadClientInfo`, `DownloadId` | `ImportApprovedEpisodes` | Notifications (`OnImportFailure`), History. |
| `SeriesScannedEvent` | `Series`, `PossibleExtraFiles` | `DiskScanService.CompletedScanning` | `UpdateMediaInfoService.Handle`, `UpdateEpisodeFileService.Handle`, ExtraService import, MediaCover. |
| `SeriesScanSkippedEvent` | `Series`, `SeriesScanSkippedReason` | `DiskScanService.Scan` | UI/log only. |
| `SeriesRenamedEvent` | `Series`, `RenamedEpisodeFile[]` | `RenameEpisodeFileService.RenameFiles` | Extras (move sidecars), Notifications. |
| `RenameCompletedEvent` | (none) | `RenameEpisodeFileService.Execute` | UI/SignalR. |
| `DeleteCompletedEvent` | (none) | `MediaFileDeletionService` (per file/series) | UI/SignalR. |

## Events consumed
| Event | Handler | Behavior |
|------|---------|----------|
| `SeriesDeletedEvent` | `MediaFileService.HandleAsync` | Bulk-delete rows for deleted series IDs. |
| `SeriesDeletedEvent` | `MediaFileDeletionService.HandleAsync` | When `DeleteFiles=true`, recycle-bin the series folder (skips paths that are parents/equals of another series). |
| `EpisodeFileDeletedEvent` | `MediaFileDeletionService.Handle` (`Last`) | Walk parents up to series root, remove empty subfolders; delete series folder if empty. Skipped when reason=`MissingFromDisk`. |
| `SeriesScannedEvent` | `UpdateMediaInfoService.Handle` | Re-read MediaInfo for files with null/outdated schema. |
| `SeriesScannedEvent` | `UpdateEpisodeFileService.Handle` | Apply `FileDate` config (set mtime to airdate). |

## Config keys (read via `IConfigService`)
- `CreateEmptySeriesFolders`, `DeleteEmptyFolders` — scan / cleanup behavior.
- `SetPermissionsLinux`, `ChmodFolder`, `ChownGroup` — non-Windows permission propagation.
- `EnableMediaInfo` — gate FFprobe analysis.
- `CopyUsingHardlinks` — picks `HardLinkOrCopy` over `Copy`.
- `RecycleBin`, `RecycleBinCleanupDays` — recycle-bin destination + retention.
- `FileDate` — `FileDateType` (None / LocalAirDate / UtcAirDate).
- `UseScriptImport`, `ScriptImportPath` — external import script.
- `UserRejectedExtensions` — comma list rejected at import.
- `ApplicationUrl`, instance name — env vars passed to import script.

## Jobs / commands (`IExecute<TCommand>`)
| Command | Handler | Trigger | Notes |
|---------|---------|---------|-------|
| `RescanSeriesCommand` (`Commands/RescanSeriesCommand.cs`) | `DiskScanService` | Manual UI / scheduled / push from `ImportApprovedEpisodes` on `DestinationAlreadyExistsException`. | Optional `SeriesId`; null = all series. `SendUpdatesToClient`. |
| `RenameFilesCommand` (`Commands/RenameFilesCommand.cs`) | `RenameEpisodeFileService` | Manual API. | `SeriesId`, `Files: List<int>`. `RequiresDiskAccess`. |
| `RenameSeriesCommand` (`Commands/RenameSeriesCommand.cs`) | `RenameEpisodeFileService`; also `DiskScanService` (sub-handler? no — only RenameEpisodeFileService) | Manual API. | `SeriesIds`. |
| `DownloadedEpisodesScanCommand` (`Commands/DownloadedEpisodesScanCommand.cs`) | `DownloadedEpisodesCommandService` | External API call (third-party clients), `CompletedDownloadService`. | `Path`, `DownloadClientId?`, `ImportMode`. `IsLongRunning`. |
| `DeleteSeriesFilesCommand` (`Commands/DeleteSeriesFilesCommand.cs`) | `MediaFileDeletionService` | Bulk delete from UI. | `SeriesIds`. |
| `CleanUpRecycleBinCommand` (`Commands/CleanUpRecycleBinCommand.cs`) | `RecycleBinProvider` | Scheduled. | Honors `RecycleBinCleanupDays`. |
| `ManualImportCommand` (`EpisodeImport/Manual/ManualImportCommand.cs`) | `ManualImportService` | UI manual-import flow. | |

## External services / native boundaries
- **FFprobe** via FFMpegCore (`FFProbe.Analyse`, `FFProbe.GetFrames`, `FFProbe.TryGetPixelFormat`). Binary expected at `AppDomain.BaseDirectory`. Used for media-info extraction and `DetectSample` runtime check.
- **MonoTorrent** for torrent file hash extraction (`TorrentFileInfoReader`).
- **External user script** (POSIX/PS): stdout protocol parsed by `OutputRegex` (`ScriptImportDecider.cs:50`): `[MediaFile] path`, `[ExtraFile] path`, `[MoveStatus] DeferMove|MoveComplete|RenameRequested`, `[PreventExtraImport]`.
- **`IDiskProvider` / `IDiskTransferService`** (NzbDrone.Common.Disk) — abstraction over the file system. `TransferMode { Move, Copy, HardLink, HardLinkOrCopy }`.

## File import pipeline (control flow, with citations)

### Series rescan (existing on-disk scan)
1. `DiskScanService.Scan(series)` (`DiskScanService.cs:77`):
   - Validates root folder exists / non-empty; emits `SeriesScanSkippedEvent` if not.
   - Optionally creates empty series folder (`CreateEmptySeriesFolders` && !`DeleteEmptyFolders`), applies permissions (`DiskScanService.cs:104-117`).
   - `GetVideoFiles(path)` (recursive) filtered by `MediaFileExtensions.Extensions` and `FilterPaths` (excludes `extras/extrafanart/...`, hidden dotfolders, `Thumbs.db`, `.DS_Store`) (`DiskScanService.cs:202`, `:232`).
2. `MediaFileTableCleanupService.Clean` (`DiskScanService.cs:193`) deletes rows whose file is missing or that have no linked episodes; zeroes `Episode.EpisodeFileId` for stale references (`MediaFileTableCleanupService.cs:31-83`).
3. `MediaFileService.FilterExistingFiles` removes paths already mapped to existing `EpisodeFile` rows (`DiskScanService.cs:137`).
4. `ImportDecisionMaker.GetImportDecisions(unmappedFiles, series, false)` for the residual set (`DiskScanService.cs:140`).
5. `ImportApprovedEpisodes.Import(decisions, false)` writes new files (no upgrade, no transfer) (`DiskScanService.cs:143`, `ImportApprovedEpisodes.cs:140-149`: deletes prior DB rows with same relative path then `_mediaFileService.Add`).
6. Existing files: re-read size; if changed and `UpdateMediaInfoService.Update` returns false, queue for batch update (`DiskScanService.cs:147-172`).
7. `RemoveEmptySeriesFolder` honours `DeleteEmptyFolders`. Possible extra files re-collected.
8. `CompletedScanning` raises `SeriesScannedEvent` (`DiskScanService.cs:196-200`).
9. `UpdateMediaInfoService.Handle(SeriesScannedEvent)` (`MediaInfo/UpdateMediaInfoService.cs:40`) re-runs FFprobe for any file with `MediaInfo == null` or `SchemaRevision < 14`.
10. `UpdateEpisodeFileService.Handle(SeriesScannedEvent)` applies `FileDate` if configured.

### Downloaded-episode import (post-grab)
1. Trigger: `DownloadedEpisodesScanCommand` or `CompletedDownloadService` → `DownloadedEpisodesCommandService.Execute` (`DownloadedEpisodesCommandService.cs:69`).
2. `DownloadedEpisodesImportService.ProcessPath(path, importMode, series?, downloadClientItem?)` (`:80`).
3. Folder branch (`ProcessFolder`, `:177`):
   - Strip `_UNPACK_`/`_FAILED_` from folder name; resolve series via `ParsingService.GetSeries(folder)` if not provided.
   - Reject if path is mapped to existing series folder; reject multi-season torrents.
   - For files without a download client item, reject locked files.
   - `DiskScanService.GetVideoFiles + FilterPaths` → `ImportDecisionMaker.GetImportDecisions(files, series, downloadClientItem, parsedDownloadInfo, parsedFolderInfo, sceneSource=true, filterExisting=true)` (`:216`).
   - `ImportApprovedEpisodes.Import(decisions, true, downloadClientItem, importMode)` (`:217`).
   - If any file imported and folder is safe (no rar > 10MB, no non-sample residuals), delete the source folder (`ShouldDeleteFolder`, `:112`).
4. File branch (`ProcessFile`, `:264`): rejects `._` macOS files, dangerous (`.lnk/.scr/.vbs/.zipx/.ps1/.arj/.lzh`), executable (`.bat/.cmd/.exe/.sh`), user-rejected, unsupported extensions (`:266-329`); checks lock; runs `ImportDecisionMaker` for the single file.

### Decision (per file)
`ImportDecisionMaker.GetDecision(localEpisode, downloadClientItem, otherFiles)` (`ImportDecisionMaker.cs:109`):
1. `Parser.Parser.ParsePath` → `LocalEpisode.FileEpisodeInfo`.
2. `_diskProvider.GetFileSize` → `Size`.
3. Determine `ReleaseType` from download/folder/file info (Unknown fallback).
4. `AggregationService.Augment` (`Aggregation/AggregationService.cs:41`): scene-name, FFprobe MediaInfo (when `!ExistingFile || EnableMediaInfo`), all augmenters (Episodes, Language, Quality, ReleaseGroup, ReleaseHash, ReleaseInfo, SubtitleInfo).
5. If episodes empty: classify as `PartialSeason`/`SeasonExtra`/`InvalidSeasonOrEpisode` rejection.
6. Else: pull `IndexerFlags` from `TrackedDownload`, run custom-format calc, evaluate every spec via `EvaluateSpec` → collect `ImportRejection`s.
7. Returns `ImportDecision`. Approved iff `Rejections` empty.

### Approved import (`ImportApprovedEpisodes.Import`)
`EpisodeImport/ImportApprovedEpisodes.cs:57` — for each approved decision (sorted by quality profile, size, min episode#):
1. Skip duplicates already imported in this batch (`:78-85`).
2. Build `EpisodeFile` from `LocalEpisode.ToEpisodeFile()`; refresh size; pull `IndexerFlags`/`ReleaseType` from grab `History`.
3. Decide `copyOnly`: `Auto` honors `downloadClientItem.CanMoveFiles`; `Move`/`Copy` explicit.
4. **New download path** (`newDownload=true`, `:126`):
   a. Compute `OriginalFilePath` if download output is parent of file path.
   b. `UpgradeMediaFileService.UpgradeEpisodeFile(file, localEpisode, copyOnly)` (`UpgradeMediaFileService.cs:37`):
      - For each existing `EpisodeFile` linked to those episodes, recycle-bin via `RecycleBinProvider.DeleteFile(path, subfolder)` and `MediaFileService.Delete(file, Upgrade)`.
      - Then `EpisodeFileMover.MoveEpisodeFile`/`CopyEpisodeFile` (uses `BuildFileNames.BuildFilePath`, ensures folders, may invoke `ScriptImportDecider.TryImport`; transfers via `IDiskTransferService`).
5. **Existing file path** (rescan): delete previous DB rows with same relative path (`ManualOverride`).
6. `MediaFileService.Add(episodeFile)` → DB insert + `EpisodeFileAddedEvent`.
7. `ExtraService.ImportEpisode` (or `MoveFilesAfterRename` + `ImportExtraFiles` when script imported).
8. Raise `EpisodeImportedEvent`.
9. Exception handling:
   - `RootFolderNotFoundException` → `EpisodeImportFailedEvent`.
   - `DestinationAlreadyExistsException` → push `RescanSeriesCommand`.
   - `RecycleBinException` → `EpisodeImportFailedEvent`.

### Rename
`RenameEpisodeFileService.RenameFiles` (`:130`): for each file, `EpisodeFileMover.MoveEpisodeFile(file, series)` → `MediaFileService.Update` → raise `EpisodeFileRenamedEvent`. Catches `FileAlreadyExistsException` (warn), `SameFilenameException` (debug). On any rename, `RemoveEmptySubfolders(series.Path)` and raise `SeriesRenamedEvent`. Final `RenameCompletedEvent`.

## FFprobe / FFmpeg integration
- Single library boundary: **FFMpegCore** (`using FFMpegCore;`) inside `MediaInfo/VideoFileInfoReader.cs`.
- `GlobalFFOptions.Configure(opts => opts.BinaryFolder = AppDomain.CurrentDomain.BaseDirectory)` (`:38`) — Sonarr ships its own ffprobe in the install dir.
- Calls:
  - `FFProbe.Analyse(filename, customArguments: "-probesize 50000000")` (`:59`).
  - On null/empty audio channel layout, retry with `"-probesize 150000000 -analyzeduration 150000000"` (`:64`).
  - `FFProbe.GetFrames(filename, customArguments: "-read_intervals \"%+#1\" -select_streams v:N")` (`:154`) for one-frame side-data probe when transfer fn = PQ (HDR detection).
  - `FFProbe.TryGetPixelFormat(format, out pixelFormat)` for bit-depth lookup (`:230`).
- `DetectSample.IsSample` (`EpisodeImport/DetectSample.cs:36`) calls `IVideoFileInfoReader.GetRunTime` (also FFprobe).
- HDR side-data names live in `MediaInfo/FFMpegCoreSideDataTypes.cs`: `DoviConfigurationRecordSideData`, `HdrDynamicMetadataSpmte2094`, `MasteringDisplayMetadata`, `ContentLightLevelMetadata`.
- `MediaInfoFormatter` (`MediaInfo/MediaInfoFormatter.cs`) formats codec/channel/HDR strings for Organizer tokens and the import script env (`ScriptImportDecider.cs:170-187`).

## Disk-IO layer / atomicity assumptions
- All file system access goes through `IDiskProvider` (NzbDrone.Common.Disk) and `IDiskTransferService`. Nothing in MediaFiles uses `System.IO.File.*` directly except for `Path.*` string utilities and `FileInfo`/`DirectoryInfo` (read-only metadata).
- Transfer modes (`TransferMode`): `Move`, `Copy`, `HardLink`, `HardLinkOrCopy`. `EpisodeFileMovingService.CopyEpisodeFile` selects `HardLinkOrCopy` when `CopyUsingHardlinks=true`, else `Copy` (`EpisodeFileMovingService.cs:100-108`). Move uses `Move`. Hardlink failure is silently downgraded to Copy by the transfer service.
- Atomic-rename guarantees rest on `IDiskTransferService.TransferFile`. Documented assumption: same-volume rename is atomic; cross-volume falls back to copy-then-delete.
- Recycle-bin path (`RecycleBinProvider.DeleteFile`, `:70`): collision-safe via `_diskProvider.FileExists` + `_2`, `_3` suffix rename loop.
- `EnsureEpisodeFolder` (`EpisodeFileMovingService.cs:182`) creates series → season → episode folder hierarchy before transfer; uses recursive `CreateFolder` on parent. Throws `RootFolderNotFoundException` if the configured root is missing — guards against creating files under a stale mount.
- `EpisodeFileMovingService.TransferFile` validates path with `IsValidPath(PathValidationType.CurrentOs)` and throws `SameFilenameException` when source==destination (`:114-126`).
- Permission propagation in `MediaFileAttributeService`: Windows uses `InheritFolderPermissions`; Linux/Mac uses chmod/chown driven by config.
- `IsFileLocked` (rescan-only, no download client) protects against importing partially written files (`DownloadedEpisodesImportService.cs:196`, `:333`).

## Notable quirks / TODOs / HACKs found in source
- `MediaFileService.Delete` (`:62`) — comment "Little hack so we have the episodes and series attached for the event consumers"; mutates `episodeFile.Path` to absolute before publish.
- `MediaFileTableCleanupService.cs:59-66` — large commented-out block re-parsing episodes during cleanup. Intentionally disabled.
- `VideoFileInfoReader.cs:53` — `// TODO: Cache media info by path, mtime and length so we don't need to read files multiple times`.
- `VideoFileInfoReader.cs:62-64` — fallback re-probe with 150MB probesize/analyzeduration when the first pass yielded no audio channel layout. Slow on large files.
- `VideoFileInfoReader.cs:218` — `mjpeg/png` filter for "motion image codec streams [...] often in front of the main video stream".
- `UpdateEpisodeFileService.cs:62-65` — `UtcAirDate` deliberately writes the UTC value as if local: "Intentionally pass UTC as local per user preference".
- `UpdateEpisodeFileService.cs:86-87` — preserves prior subsecond mtime (Sonarr issue #7228 workaround).
- `DiskScanService.cs:175` — log message reuses `decisionsStopwatch` to report `fileInfoStopwatch` elapsed (likely typo).
- `MediaFileDeletionService.cs:111` — `_logger.ProgressDebug("{0}: Deleting episode files}", series.Title)` has stray closing brace in format string.
- `ImportApprovedEpisodes.cs:198` — commented-out `e.Data.Add("report", remoteEpisode.Report.ToJson())` for spec exception logging.
- `ManualImportService.cs:547` — `// TODO: Cleanup non-tracked downloads`.
- `MediaFileExtensions` arbitrarily encodes default Quality on extension; `.mkv` defaults to `HDTV720p`, `.m2ts` to `Bluray720p`. Used as fallback when Parser fails.
- `DiskScanService.cs:72-75` — excluded-extras regex hard-codes `extras|extrafanart|behind the scenes|deleted scenes|featurettes|interviews|other|scenes|samples|shorts|trailers|theme[-_. ]music|backdrops`.
- `DownloadedEpisodesImportService.cs:266-274` — explicit `._` AppleDouble file rejection.
- `EpisodeFileMovingService.cs:135-148` — script import path: if script returns `RenameRequested`, recursively call `MoveEpisodeFile` (with episodes loaded from DB) and swallow `SameFilenameException`. Implies double-transfer in some paths.
- `MediaFileAttributeService.cs:36-58` — Windows `InheritFolderPermissions` wrapped in try/catch listing `UnauthorizedAccessException|InvalidOperationException|FileNotFoundException` as benign — "to prevent this from causing issues with remote NAS boxes".
- `RecycleBinProvider.Cleanup` — only purges files past `RecycleBinCleanupDays`; folders only removed if empty. Never recurses time-check on subfolders.
- `VideoFileInfoReader.cs:24-25` — `MINIMUM_MEDIA_INFO_SCHEMA_REVISION == CURRENT == 14`. Means any future schema bump must update both.

## Test files (the port must keep equivalents passing)
- `src/NzbDrone.Core.Test/MediaFiles/DiskScanServiceTests/ScanFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/DownloadedEpisodesCommandServiceFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/DownloadedEpisodesImportServiceFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/EpisodeFileMovingServiceTests/MoveEpisodeFileFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/MediaFileDeletionService/DeleteEpisodeFileFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/MediaFileRepositoryFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/MediaFileServiceTests/FilterFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/MediaFileTableCleanupServiceFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/RenameEpisodeFileServiceFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/UpdateEpisodeFileServiceTests/ChangeFileDateForFileFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/UpgradeMediaFileServiceFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/EpisodeImport/DetectSampleFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/EpisodeImport/GetSceneNameFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/EpisodeImport/ImportApprovedEpisodesFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/EpisodeImport/ImportDecisionMakerFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/EpisodeImport/LocalEpisodeCustomFormatCalculationServiceFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/EpisodeImport/Aggregation/Aggregators/**` (full augmenter coverage)
- `src/NzbDrone.Core.Test/MediaFiles/EpisodeImport/Specifications/{AbsoluteEpisodeNumber,AlreadyImported,EpisodeTitle,FreeSpace,FullSeason,HasAudioTrack,MatchesFolder,MatchesGrab,NotSample,NotUnpacking,Upgrade}SpecificationFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/MediaInfo/UpdateMediaInfoServiceFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/MediaInfo/VideoFileInfoReaderFixture.cs`
- `src/NzbDrone.Core.Test/MediaFiles/MediaInfo/MediaInfoFormatterTests/**`

## Open questions
- **MEDIAFILES-Q1** What FFprobe (libav) version is bundled? FFMpegCore wraps the system binary; the Go port likely calls `ffprobe` directly. We need a pinned version + JSON schema baseline (`-show_streams -show_format -print_format json`).
- **MEDIAFILES-Q2** Does the Go port reuse FFMpegCore via interop, or shell out? Recommend native Go `os/exec` and parse `-print_format json`. Spike → `spikes/ffmpeg-exec.md`.
- **MEDIAFILES-Q3** `MediaInfoModel.RawStreamData` is `string.Concat(analysis.OutputData)` — the raw FFprobe stderr/output. Schema-relevant? Stored on every row. Decide whether the Go port preserves byte-identical content or normalizes.
- **MEDIAFILES-Q4** `IDiskTransferService` semantics on Windows for hardlink fallback are not documented in MediaFiles itself; need a card on `NzbDrone.Common.Disk.DiskTransferService` referenced here. Cross-link to common.md.
- **MEDIAFILES-Q5** Atomicity guarantee across SMB / NFS / overlayfs: Sonarr currently relies on whatever `File.Move` / `rename(2)` does. Document what we promise on the Go side (hardlink first, fall back to copy+fsync+rename).
- **MEDIAFILES-Q6** `ScriptImportDecider` uses `StartAndCapture` with `StringDictionary` env vars; ordering and quoting on Windows shells (`cmd.exe`/`.ps1`) — port must replicate exact env-var names and `"src" "dst"` arg quoting. List ~50 `Sonarr_*` vars in a separate sub-card.
- **MEDIAFILES-Q7** `MediaInfo` is an `IEmbeddedDocument` (JSON-in-column). Confirm Postgres path uses `jsonb` vs `json`; cross-reference `embedded-docs/catalogue.md`.
- **MEDIAFILES-Q8** `MediaFileTableCleanupService` uses `PathEqualityComparer.Instance` — case-insensitive on Windows, case-sensitive elsewhere. Need a deterministic Go equivalent or a per-OS toggle.
- **MEDIAFILES-Q9** `EpisodeFileMovingService.TransferFile` calls `_scriptImportDecider.TryImport` *during* every transfer when `localEpisode != null`. Is this intended for non-`UseScriptImport` setups? (Decider returns `DeferMove` early so it falls through, but the flow control is non-obvious.)
- **MEDIAFILES-Q10** `VideoFileInfoReader` schema revision is 14; what's the upgrade story when we change Go-side struct? Need a migration/replay command.
- **MEDIAFILES-Q11** `FileExtensions.RemoveFileExtension` regex `\.[a-z0-9]{2,4}$` only strips known media + usenet extensions — but uses regex match length 2–4 which would miss `.tar.gz` etc. Behaviour matches C#; document explicitly.
- **MEDIAFILES-Q12** `DownloadedEpisodesImportService.ProcessFile` rejects `.exe/.bat/.cmd/.sh` even when nested under a known-good folder — this can drop legitimate `.sh` companion files some users keep. Confirm policy or add config toggle.
- **MEDIAFILES-Q13** `MediaFileAttributeService.SetFolderLastWriteTime` is a no-op on non-Windows. Should the Go port preserve this asymmetry or unify?
- **MEDIAFILES-Q14** `RecycleBinProvider.Cleanup` does not honour subfolder mtime — only file mtime per recursion. Behaviour parity vs. simplification?
