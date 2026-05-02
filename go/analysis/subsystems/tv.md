# Tv

**Source:** `src/NzbDrone.Core/Tv/**`
**Target Go package:** `go/internal/tv/`
**Status:** drafted

## Purpose
The Tv subsystem is the **domain core** of Sonarr. It owns the canonical aggregates Series → Season → Episode (and the loose link to EpisodeFile), the only persistence path that creates and mutates them, and the lifecycle that synchronises them with the upstream metadata source (Skyhook/TheTVDB). Every other subsystem (downloads, history, parser, indexers, notifications, API) consumes Series/Episode from here. It also raises the cross-cutting domain events (`SeriesAdded`, `SeriesEdited`, `SeriesDeleted`, `EpisodeInfoRefreshed`, …) that other subsystems use as their integration triggers, and hosts the long-running `RefreshSeriesCommand` and series-move commands.

## Public types

| Type | File | Kind | Notes |
|------|------|------|-------|
| `Series` | `Tv/Series.cs` | model (`ModelBase`) | Root aggregate; props: `TvdbId`, `TvRageId`, `TvMazeId`, `ImdbId`, `TmdbId`, `MalIds (HashSet<int>)`, `AniListIds (HashSet<int>)`, `Title`, `CleanTitle`, `SortTitle`, `Status (SeriesStatusType)`, `Overview`, `AirTime`, `Monitored`, `MonitorNewItems (NewItemMonitorTypes)`, `QualityProfileId`, `SeasonFolder`, `LastInfoSync (DateTime?)`, `Runtime`, `Images (List<MediaCover>)`, `SeriesType`, `Network`, `UseSceneNumbering`, `TitleSlug`, `Path`, `Year`, `Ratings`, `Genres (List<string>)`, `Actors (List<Actor>)`, `Certification`, `RootFolderPath`, `Added`, `FirstAired (DateTime?)`, `LastAired (DateTime?)`, `QualityProfile (LazyLoaded<QualityProfile>)`, `OriginalLanguage (Language)`, `OriginalCountry`, `Seasons (List<Season>)`, `Tags (HashSet<int>)`, `AddOptions (AddSeriesOptions)`. `ApplyChanges(other)` copies a controlled subset of fields. |
| `Episode` | `Tv/Episode.cs` | model (`ModelBase`, `IComparable`) | Props: `SeriesId`, `TvdbId`, `EpisodeFileId`, `SeasonNumber`, `EpisodeNumber`, `Title`, `AirDate (string)`, `AirDateUtc (DateTime?)`, `Overview`, `Monitored`, `AbsoluteEpisodeNumber?`, `SceneAbsoluteEpisodeNumber?`, `SceneSeasonNumber?`, `SceneEpisodeNumber?`, `AiredAfterSeasonNumber?`, `AiredBeforeSeasonNumber?`, `AiredBeforeEpisodeNumber?`, `UnverifiedSceneNumbering`, `Ratings`, `Images`, `LastSearchTime (DateTime?)`, `Runtime`, `FinaleType (string)`, `SeriesTitle (private setter, denormalised join)`, `EpisodeFile (LazyLoaded<EpisodeFile>)`, `Series` (transient nav prop), `HasFile` (computed `EpisodeFileId > 0`), `AbsoluteEpisodeNumberAdded` (transient flag set during refresh). `AIR_DATE_FORMAT = "yyyy-MM-dd"`. CompareTo orders by SeasonNumber then EpisodeNumber. |
| `Season` | `Tv/Season.cs` | embedded (`IEmbeddedDocument`) | `SeasonNumber`, `Monitored`, `Images (List<MediaCover>)`. Stored as JSON inside `Series.Seasons`. |
| `Actor` | `Tv/Actor.cs` | embedded (`IEmbeddedDocument`) | `Name`, `Character`, `Images`. |
| `Ratings` | `Tv/Ratings.cs` | embedded (`IEmbeddedDocument`) | `Votes (int)`, `Value (decimal)`. |
| `AddSeriesOptions` | `Tv/AddSeriesOptions.cs` | embedded (extends `MonitoringOptions`) | adds `SearchForMissingEpisodes`, `SearchForCutoffUnmetEpisodes`. |
| `MonitoringOptions` | `Tv/MonitoringOptions.cs` | embedded | `IgnoreEpisodesWithFiles`, `IgnoreEpisodesWithoutFiles`, `Monitor (MonitorTypes)`. |
| `MonitorTypes` | `Tv/MonitoringOptions.cs:13` | enum | `Unknown, All, Future, Missing, Existing, FirstSeason, LastSeason, [Obsolete] LatestSeason, Pilot, Recent, MonitorSpecials, UnmonitorSpecials, None, Skip`. |
| `NewItemMonitorTypes` | `Tv/MonitoringOptions.cs:34` | enum | `All, None`. |
| `SeriesStatusType` | `Tv/SeriesStatusType.cs` | enum | `Deleted=-1, Continuing=0, Ended=1, Upcoming=2`. |
| `SeriesTypes` | `Tv/SeriesTypes.cs` | enum | `Standard=0, Daily=1, Anime=2`. |
| `MultipleSeriesFoundException` | `Tv/MultipleSeriesFoundException.cs` | exception | thrown by `SeriesRepository.ReturnSingleSeriesOrThrow`. |
| `BulkMoveSeries` | `Tv/Commands/BulkMoveSeriesCommand.cs:16` | DTO | `{SeriesId, SourcePath}`, equality by SeriesId. |

## Public services

| Type | Methods | Side effects | Notes |
|------|---------|--------------|-------|
| `ISeriesService` / `SeriesService` | `GetSeries(int)` / `GetSeries(IEnumerable<int>)`, `AddSeries(Series)` / `AddSeries(List<Series>)`, `FindByTvdbId/FindByTvRageId/FindByImdbId/FindByTitle(title[, year])/FindByTitleInexact/FindByPath`, `DeleteSeries(ids, deleteFiles, addImportListExclusion)`, `GetAllSeries`, `AllSeriesTvdbIds`, `GetAllSeriesPaths`, `GetAllSeriesTags`, `AllForTag(tagId)`, `GetAllSeriesQualityProfiles`, `UpdateSeries(series, updateEpisodesToMatchSeason=true, publishUpdatedEvent=true)`, `UpdateSeries(List<Series>, useExistingRelativeFolder)`, `SeriesPathExists`, `RemoveAddOptions`, `UpdateTags` | DB writes via `ISeriesRepository`; on `AddSeries` raises `SeriesAddedEvent` (single) or `SeriesImportedEvent` (bulk); on `DeleteMany` raises `SeriesDeletedEvent`; on `UpdateSeries` raises `SeriesEditedEvent` (with old/new + episodesChanged flag); on bulk update raises `SeriesBulkEditedEvent`; mutates `Tags` via `IAutoTaggingService.GetTagChanges`; bulk Update rebuilds `Path` via `IBuildSeriesPaths`; `UpdateSeries` propagates season-monitored changes to episodes via `IEpisodeService.SetEpisodeMonitoredBySeason` when `updateEpisodesToMatchSeason==true` | `Tv/SeriesService.cs` |
| `IEpisodeService` / `EpisodeService` | `GetEpisode(id)`, `GetEpisodes(ids)`, `FindEpisode(seriesId,season,ep)`, `FindEpisode(seriesId,absEp)`, `FindEpisode(seriesId,date,part)`, `FindEpisodeByTitle(seriesId,season,releaseTitle)`, `FindEpisodesBySceneNumbering(seriesId,season,ep)`, `FindEpisodesBySceneNumbering(seriesId,sceneAbs)`, `GetEpisodeBySeries`, `GetEpisodesBySeries(List<int>)`, `GetEpisodesBySeason`, `GetEpisodesBySceneSeason`, `EpisodesWithFiles`, `EpisodesWithoutFiles(pagingSpec, includeSpecials)`, `GetEpisodesByFileId`, `UpdateEpisode`, `SetEpisodeMonitored(id, bool)`, `SetMonitored(IEnumerable<int>, bool)`, `SetEpisodeMonitoredBySeason(seriesId, seasonNumber, monitored)`, `UpdateEpisodes(list)`, `UpdateLastSearchTime(list)`, `EpisodesBetweenDates(start, end, includeUnmonitored, includeSpecials)`, `InsertMany`, `UpdateMany`, `DeleteMany` | DB writes via `IEpisodeRepository`; uses `ICacheManager`-backed `HashSet<int>` per series for "missing-from-disk" episodes that should be unmonitored on next scan; reads `IConfigService.AutoUnmonitorPreviouslyDownloadedEpisodes`; **handlers**: `IHandle<EpisodeFileDeletedEvent>` clears file id and conditionally unmonitors based on `DeleteMediaFileReason`; `IHandle<EpisodeFileAddedEvent>` links file id and clears the cache; `IHandleAsync<SeriesDeletedEvent>` cascades episode deletion; `IHandleAsync<SeriesScannedEvent>` flushes the per-series unmonitor cache | `Tv/EpisodeService.cs` |
| `IEpisodeCutoffService` / `EpisodeCutoffService` | `EpisodesWhereCutoffUnmet(PagingSpec<Episode>)` | Reads all quality profiles, computes per-profile QualitiesBelowCutoff, delegates to `IEpisodeRepository.EpisodesWhereCutoffUnmet`. Honours `profile.UpgradeAllowed` (false ⇒ cutoff = first allowed quality). | `Tv/EpisodeCutoffService.cs` |
| `IEpisodeMonitoredService` / `EpisodeMonitoredService` | `SetEpisodeMonitoredStatus(series, monitoringOptions)` | Pure mutation of in-memory `episodes`/`seasons` then `_episodeService.UpdateEpisodes` + `_seriesService.UpdateSeries(series, updateEpisodesToMatchSeason=false)`. Branches per `MonitorTypes`. `Unknown` falls back to legacy `IgnoreEpisodesWithFiles` / `IgnoreEpisodesWithoutFiles` path. `Skip` is a no-op. Special handling around the latest season for `All` / `Future` (when series is `Continuing`/`Upcoming`) and around `Pilot` (does not monitor season 1 even though one episode is monitored). | `Tv/EpisodeMonitoredService.cs` |
| `IAddSeriesService` / `AddSeriesService` | `AddSeries(Series)`, `AddSeries(List<Series>, ignoreErrors=false)` | Calls `IProvideSeriesInfo.GetSeriesInfo(tvdbId)` (Skyhook), merges with caller-supplied seasons, runs `IBuildFileNames.GetSeriesFolder` for path, sets `CleanTitle`/`SortTitle`/`Added`, runs `IAddSeriesValidator` (FluentValidation), persists via `ISeriesService.AddSeries`. Bulk path dedupes by `TvdbId` and by `TitleSlug` against both already-stored and pending series. | `Tv/AddSeriesService.cs` |
| `IRefreshEpisodeService` / `RefreshEpisodeService` | `RefreshEpisodeInfo(series, IEnumerable<Episode> remoteEpisodes)` | Compares remote episodes (Skyhook) against existing, builds add/update/delete lists; for anime, dedupes by absolute number and orders by abs number; sets monitored status on new episodes from season/series monitor; "TBA" fallback for missing titles; finale-type heuristic (synthesises `"series"` finale on last season's last episode for ended series within 14 days); unmonitor-readded-episodes safety net (unmonitor episodes with airdate >1 day old when no existing episodes were present); multi-episode airtime adjustment (offset by `Runtime` minutes); direct-to-DVD airdate fallback to `series.FirstAired`. Calls `IEpisodeService.DeleteMany/UpdateMany/InsertMany` then publishes `EpisodeInfoRefreshedEvent`. | `Tv/RefreshEpisodeService.cs` |
| `RefreshSeriesService` | `IExecute<RefreshSeriesCommand>.Execute` | For each `SeriesId` (or all series with `ICheckIfSeriesShouldBeRefreshed.ShouldRefresh`): calls `IProvideSeriesInfo.GetSeriesInfo(tvdbId)`; on `SeriesNotFoundException` marks series `Status = Deleted` (without re-publishing edited event) and re-throws (per-id) or skips (all); copies refreshed scalar fields onto stored series; resolves `Path` to `DirectoryInfo.FullName` + actual casing; reconciles seasons (preserves existing monitored, defaults season 0 to unmonitored, monitors new seasons iff `MonitorNewItems == All`); calls `IRefreshEpisodeService.RefreshEpisodeInfo`; calls `_seriesService.UpdateSeries(publishUpdatedEvent: false)` then publishes `SeriesUpdatedEvent` itself; runs `UpdateTags`; conditionally rescans via `IDiskScanService.Scan` based on `IConfigService.RescanAfterRefresh` and trigger; reports `CommandResult.Indeterminate` on failures. Brackets the run with `SeriesRefreshStartingEvent` / `SeriesRefreshCompleteEvent`. | `Tv/RefreshSeriesService.cs` |
| `MoveSeriesService` | `IExecute<MoveSeriesCommand>.Execute`, `IExecute<BulkMoveSeriesCommand>.Execute` | Validates path, ensures source exists, transfers folder via `IDiskTransferService.TransferFolder(... TransferMode.Move)`, ensures parent of destination exists, publishes `SeriesMovedEvent` on success, reverts series.Path via `_seriesService.UpdateSeries(series)` on `IOException`. Bulk variant computes per-series destination via `IBuildFileNames.GetSeriesFolder`. | `Tv/MoveSeriesService.cs` |
| `IBuildSeriesPaths` / `SeriesPathBuilder` | `BuildPath(series, useExistingRelativeFolder)` | Throws if `RootFolderPath` empty; if `useExistingRelativeFolder` and current path set, derives relative path against `IRootFolderService.GetBestRootFolderPath`; otherwise `Path.Combine(rootFolder, IBuildFileNames.GetSeriesFolder(series))`. Logs a warning when relative path can't be resolved. | `Tv/SeriesPathBuilder.cs` |
| `SeriesAddedHandler` | `IHandle<SeriesAddedEvent>` / `IHandle<SeriesImportedEvent>` | Pushes `RefreshSeriesCommand(seriesIds, isNewSeries=true)` per added series. | `Tv/SeriesAddedHandler.cs` |
| `SeriesEditedService` | `IHandle<SeriesEditedEvent>` | Pushes `RefreshSeriesCommand(seriesId, isNewSeries=false)` only when `SeriesType` changed. | `Tv/SeriesEditedService.cs` |
| `SeriesScannedHandler` | `IHandle<SeriesScannedEvent>` / `IHandle<SeriesScanSkippedEvent>` | If `series.AddOptions == null`: calls `IEpisodeRefreshedService.Search(seriesId)` (post-refresh search of recently aired). Otherwise (post-add): runs `EpisodeMonitoredService.SetEpisodeMonitoredStatus`, publishes `SeriesAddCompletedEvent`, pushes `SeriesSearchCommand` / `MissingEpisodeSearchCommand` / `CutoffUnmetEpisodeSearchCommand` per `AddOptions.SearchFor*` flags, then nulls `AddOptions` and persists via `_seriesService.RemoveAddOptions`. | `Tv/SeriesScannedHandler.cs` |
| `IEpisodeRefreshedService` / `EpisodeRefreshedService` | `Search(seriesId)`; `IHandle<EpisodeInfoRefreshedEvent>` | On refresh, caches the IDs of episodes added/updated that aired within \[-14d, +1d\] and are monitored, in `ICached<List<int>>`. `Search` retrieves & purges that cache and pushes `EpisodeSearchCommand` for any cached id without a file. Skipped entirely if `series.AddOptions != null` (the new-series path) or if `!series.Monitored`. | `Tv/EpisodeRefreshedService.cs` |
| `ICheckIfSeriesShouldBeRefreshed` / `ShouldRefresh­Series` | `ShouldRefresh(series)` | True if `LastInfoSync < now-30d`; or any aired episode has Title `"TBA"` (ordinal compare); else if `LastInfoSync >= now-6h` false; else if not Ended true; else true iff last episode aired within last 30d. Any exception ⇒ true. | `Tv/ShouldRefreshSeries.cs` |
| `IAddSeriesValidator` / `AddSeriesValidator` | `Validate(series)` | FluentValidation: `Path` cascade-stops on `IsValidPath` then `RootFolderValidator`, `SeriesPathValidator`, `SeriesAncestorValidator`; `TitleSlug` runs `SeriesTitleSlugValidator`. | `Tv/AddSeriesValidator.cs` |
| `SeriesTitleSlugValidator` | `IsValid(ctx)` | Walks all series in DB to detect another series with the same slug and a different Id. **N+1 / full-table scan on every add.** | `Tv/SeriesTitleSlugValidator.cs` |
| `SeriesTitleNormalizer` (static) | `Normalize(title, tvdbId)` | Pre-computed override map: `{281588 → "a to z"}`; otherwise `Parser.Parser.NormalizeTitle(title).ToLower()`. | `Tv/SeriesTitleNormalizer.cs` |
| `ISeriesRepository` / `SeriesRepository` (Dapper, extends `BasicRepository<Series>`) | `SeriesPathExists`, `FindByTitle(cleanTitle[, year])`, `FindByTitleInexact(cleanTitle)`, `FindByTvdbId`, `FindByTvRageId`, `FindByImdbId`, `FindByPath`, `AllSeriesTvdbIds`, `AllSeriesPaths`, `AllSeriesTags`, `AllSeriesQualityProfiles` | Uses raw `instr(@cleanTitle, "Series"."CleanTitle")` for SQLite vs `strpos(...) > 0` for Postgres; `ReturnSingleSeriesOrThrow` raises `MultipleSeriesFoundException`. | `Tv/SeriesRepository.cs` |
| `IEpisodeRepository` / `EpisodeRepository` (Dapper, extends `BasicRepository<Episode>`) | `Find(seriesId, season, ep)`, `Find(seriesId, absEp)`, `Find(seriesId, date)`, `GetEpisodes(seriesId[, season])`, `GetEpisodesBySeriesIds`, `GetEpisodesBySceneSeason`, `GetEpisodeByFileId`, `EpisodesWithFiles` (joined with EpisodeFile), `EpisodesWithoutFiles(pagingSpec, includeSpecials)` (joined with Series, filters airdate by `Runtime` minutes for "now-aired"; SQLite uses `datetime(strftime('%s', AirDateUtc) + Runtime*60, 'unixepoch')`, Postgres uses `make_interval(mins => Runtime)`), `EpisodesWhereCutoffUnmet(pagingSpec, qualitiesBelowCutoff, includeSpecials)` (left-joins EpisodeFiles, builds a quality LIKE-clause that **string-matches into the JSON-encoded `Quality` column**: `"EpisodeFiles"."Quality" LIKE '%_quality_: {id},%'`), `FindEpisodesBySceneNumbering(seriesId, season, ep)` / `(seriesId, sceneAbs)`, `EpisodesBetweenDates(start, end, includeUnmonitored, includeSpecials)`, `SetMonitoredFlat(episode, monitored)`, `SetMonitoredBySeason(seriesId, seasonNumber, monitored)` (raw UPDATE that skips rows whose value already matches), `SetMonitored(ids, monitored)` (synthetic stubs + SetFields), `SetFileId(episode, fileId)`, `ClearFileId(episode, unmonitor)` (clears file id, ANDs Monitored against `!unmonitor`). `PagedQuery` joins to Series so episode rows come back populated. | `Tv/EpisodeRepository.cs` |

## DB tables touched
- `Series` — read/write (table mapping `Series`)
- `Episodes` — read/write (table mapping `Episodes`; raw UPDATE in `SetMonitoredBySeason`)
- `EpisodeFiles` — read-only join target (e.g. `EpisodesWithFiles`, cutoff-unmet)
- `Tags` — indirectly via `Series.Tags` HashSet column

(Series/Episode rows store `Seasons`/`Ratings`/`Actors`/`Images`/`AddOptions` as embedded JSON — see `IEmbeddedDocument` types.)

## Events raised (IEventAggregator)

| Event | Payload | Producers |
|------|---------|-----------|
| `SeriesAddedEvent` | `Series` | `SeriesService.AddSeries(single)` |
| `SeriesImportedEvent` | `List<int> SeriesIds` | `SeriesService.AddSeries(bulk)` |
| `SeriesEditedEvent` | `Series, OldSeries, EpisodesChanged` | `SeriesService.UpdateSeries(single)` (when `publishUpdatedEvent==true`) |
| `SeriesBulkEditedEvent` | `List<Series>` | `SeriesService.UpdateSeries(bulk)` |
| `SeriesDeletedEvent` | `List<Series>, DeleteFiles, AddImportListExclusion` | `SeriesService.DeleteSeries` |
| `SeriesUpdatedEvent` | `Series` | `RefreshSeriesService.RefreshSeriesInfo` (after a successful refresh, and also when marking Status=Deleted on `SeriesNotFoundException`) |
| `SeriesRefreshStartingEvent` | `bool ManualTrigger` | `RefreshSeriesService.Execute` (start) |
| `SeriesRefreshCompleteEvent` | (none) | `RefreshSeriesService.Execute` (end) |
| `SeriesAddCompletedEvent` | `Series` | `SeriesScannedHandler.HandleScanEvents` (post-add) |
| `SeriesMovedEvent` | `Series, SourcePath, DestinationPath` | `MoveSeriesService.MoveSingleSeries` |
| `EpisodeInfoRefreshedEvent` | `Series, ReadOnlyCollection<Episode> Added/Updated/Removed` | `RefreshEpisodeService.RefreshEpisodeInfo` |

(Note: `SeriesScannedEvent`, `SeriesScanSkippedEvent`, `EpisodeFileAddedEvent`, `EpisodeFileDeletedEvent` are raised elsewhere — `MediaFiles` — but Tv consumes them.)

## Events consumed

| Event | Handler | Behavior |
|------|---------|----------|
| `EpisodeFileDeletedEvent` (sync) | `EpisodeService.Handle` | Detach file id from each linked episode; conditionally unmonitor (skips Upgrade / ManualOverride / MissingFromDisk reasons); for `MissingFromDisk` + `AutoUnmonitorPreviouslyDownloadedEpisodes` cache the episode id for the next scan. |
| `EpisodeFileAddedEvent` (sync) | `EpisodeService.Handle` | Link file id to each episode in `message.EpisodeFile.Episodes.Value`; remove from per-series cache. |
| `SeriesDeletedEvent` (async) | `EpisodeService.HandleAsync` | Cascade-delete all episodes for the deleted series ids. |
| `SeriesScannedEvent` (async) | `EpisodeService.HandleAsync` | Flush the per-series cache: any cached ids get `Monitored=false` then the cache is cleared. |
| `SeriesScannedEvent` / `SeriesScanSkippedEvent` (sync) | `SeriesScannedHandler.Handle` | See `HandleScanEvents` above (post-add monitoring + searches, or trigger a previously-aired-episode search via `EpisodeRefreshedService.Search`). |
| `SeriesAddedEvent` / `SeriesImportedEvent` | `SeriesAddedHandler.Handle` | Push `RefreshSeriesCommand(... isNewSeries=true)`. |
| `SeriesEditedEvent` | `SeriesEditedService.Handle` | Push `RefreshSeriesCommand` only when `SeriesType` changed. |
| `EpisodeInfoRefreshedEvent` | `EpisodeRefreshedService.Handle` | Cache recently-aired added/updated monitored episode ids for follow-up search after rescan. |

## Config keys
Read via `IConfigService` inside Tv:
- `AutoUnmonitorPreviouslyDownloadedEpisodes` — `EpisodeService.Handle(EpisodeFileDeletedEvent)` at `Tv/EpisodeService.cs:267`
- `RescanAfterRefresh` (`RescanAfterRefreshType` enum: Always, AfterManual, Never) — `RefreshSeriesService.RescanSeries` at `Tv/RefreshSeriesService.cs:170`

## Jobs / commands (`IExecute<TCommand>`)

| Command | File | Trigger | Frequency | Idempotent? | Notes |
|---------|------|---------|-----------|-------------|-------|
| `RefreshSeriesCommand` | `Tv/Commands/RefreshSeriesCommand.cs` | `SeriesAddedHandler`, `SeriesEditedService` (type change), scheduled when `SeriesIds` empty (whole-library), API/manual | Long-running. `UpdateScheduledTask` only when `SeriesIds.Empty()` (all-series). `SendUpdatesToClient = true`. | Effectively yes (replays Skyhook merge); not bit-for-bit because `LastInfoSync` and event timestamps change. | Single shape covers per-series refresh (with `IsNewSeries` flag) and full-library refresh. Backwards-compat `SeriesId` setter dumps into `SeriesIds[0]`. |
| `MoveSeriesCommand` | `Tv/Commands/MoveSeriesCommand.cs` | API (rename / move single series) | One-shot. `RequiresDiskAccess = true`, `SendUpdatesToClient = true`. | No — destructive folder move. |
| `BulkMoveSeriesCommand` | `Tv/Commands/BulkMoveSeriesCommand.cs` | API (bulk root-folder change) | One-shot. `RequiresDiskAccess = true`, `SendUpdatesToClient = true`. | No. | Computes per-series destination from new `DestinationRootFolder` + `IBuildFileNames.GetSeriesFolder`. |

## External services
- **Skyhook / TheTVDB** via `IProvideSeriesInfo.GetSeriesInfo(int tvdbId) → Tuple<Series, List<Episode>>` (`MetadataSource` namespace). Called from:
  - `AddSeriesService.AddSkyhookData` (`Tv/AddSeriesService.cs:120`)
  - `RefreshSeriesService.RefreshSeriesInfo` (`Tv/RefreshSeriesService.cs:66`)
- No other external calls originate inside `Tv/`. Disk I/O is delegated to `IDiskProvider` / `IDiskTransferService` / `IDiskScanService` (in `MoveSeriesService` / `RefreshSeriesService`).

## Notable quirks / TODOs / HACKs found in source
- `Tv/SeriesService.cs:201-202` — `// updateEpisodesToMatchSeason is an override for EpisodeMonitoredService ... TODO: Remove when seasons are split from series (or we come up with a better way to address this)`. Season/episode coupling is a known design wart.
- `Tv/SeriesService.cs:223` — `// Never update AddOptions when updating a series, keep it the same as the existing stored series.`
- `Tv/EpisodeService.cs:120` — `// TODO: can replace this search mechanism with something smarter/faster/better` (FindEpisodeByTitle is O(n) over season episodes with normalized substring search).
- `Tv/EpisodeService.cs:273` — `// If episode is being unlinked because it's missing from disk store it for` (sentence cut off; logic: defer the unmonitor until the next `SeriesScannedEvent`).
- `Tv/EpisodeRepository.cs:243-256` — `BuildQualityCutoffWhereClause` builds `"EpisodeFiles"."Quality" LIKE '%_quality_: {0},%'` — text-matches inside a JSON-encoded column. Fragile if the JSON format changes.
- `Tv/EpisodeRepository.cs:213-230` — split AirDateUtc cutoff SQL between SQLite (`datetime(strftime('%s', ...))`) and Postgres (`make_interval`). Any port must reproduce both.
- `Tv/RefreshEpisodeService.cs:95-107` — finale-type heuristic invents `"series"` finales for ended series whose latest season's last episode aired in the last 14 days; comment: *"TheTVDB has a severe lack of season/series finales, this helps smooth out that limitation"*.
- `Tv/RefreshEpisodeService.cs:158-188` — `UnmonitorReaddedEpisodes` silently unmonitors aired episodes >1 day old when refreshing a brand-new series; warns rather than acts when there are existing episodes. Subtle behaviour, must be ported exactly.
- `Tv/RefreshEpisodeService.cs:191-214` — multi-episode air-time adjustment skips groups >3 to avoid stomping on real same-day batches; uses `Series.Runtime` as offset per episode.
- `Tv/RefreshEpisodeService.cs:216-226` — direct-to-DVD fallback overwrites every episode's `AirDateUtc`/`AirDate` with `series.FirstAired` when *all* episodes are missing dates and series is Ended.
- `Tv/RefreshSeriesService.cs:55-57` — `// Get the series before updating, that way any changes made to the series after the refresh started, but before this series was refreshed won't be lost.` Re-fetch race-condition workaround.
- `Tv/RefreshSeriesService.cs:83-87` — `Warn` and silently swap TvdbId when Skyhook returns a different id (dup-merge case from upstream). Logs but does not surface to user.
- `Tv/SeriesScannedHandler.cs:50-58` — comment block explaining the `SeriesSearch` vs `MissingEpisode`+`CutoffUnmet` shortcut to avoid duplicate searches.
- `Tv/EpisodeMonitoredService.cs:36-37` — `// Fallback for v2 endpoints` — `MonitorTypes.Unknown` triggers the legacy boolean-options path instead of the enum path.
- `Tv/EpisodeMonitoredService.cs:43-47` — `Skip` value documented as "use season information when series was added".
- `Tv/EpisodeMonitoredService.cs:93-95` — `#pragma warning disable CS0612` to keep handling the obsolete `LatestSeason` enum value identically to `LastSeason`.
- `Tv/SeriesTitleSlugValidator.cs:30-33` — full-table fetch (`GetAllSeries`) on every add. Not cached. Performance smell on large libraries.
- `Tv/SeriesTitleNormalizer.cs:7-10` — hardcoded fix-up: `{281588 → "a to z"}` (TVDB id of the series "A to Z"). Single-entry table that exists only because the parser would otherwise nuke the title.
- `Tv/Episode.cs:42` — `SeriesTitle` has a private setter and is a denormalised join column populated by Dapper.
- `Tv/Episode.cs:49` — `AbsoluteEpisodeNumberAdded` is a transient flag set by `RefreshEpisodeService` and consumed by `EpisodeRefreshedService`; not persisted but lives on the model — easy to mis-port.
- `Tv/Series.cs:70-86` — `ApplyChanges` is a *partial* copy (only TvdbId, Seasons, Path, QualityProfileId, SeasonFolder, Monitored, MonitorNewItems, SeriesType, RootFolderPath, Tags, AddOptions). Used by the add path to layer caller-supplied edits on top of Skyhook data.
- `Tv/EpisodeRepository.cs:258-283` — `FindOneByAirDate` is dead code in this class (the public method lives on `EpisodeService.FindOneByAirDate`); easy to delete-by-accident or accidentally call.
- `Tv/RefreshSeriesService.cs:118-119` — `series.Path = new DirectoryInfo(series.Path).FullName; series.Path = series.Path.GetActualCasing();` — silently re-canonicalises path on every refresh; failure logs a warning but proceeds.

## Test files (the port must keep passing equivalents)
- `src/NzbDrone.Core.Test/TvTests/AddSeriesFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/MoveSeriesServiceFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/RefreshEpisodeServiceFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/RefreshSeriesServiceFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/SeriesFolderPathBuilderFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/SeriesTitleNormalizerFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/SeriesTitleSlugValidatorFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/ShouldRefreshSeriesFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeMonitoredServiceTests/SetEpisodeMontitoredFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeMonitoredServiceTests/LegacySetEpisodeMontitoredFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeRepositoryTests/EpisodesRepositoryReadFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeRepositoryTests/EpisodesWithoutFilesFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeRepositoryTests/FindEpisodeFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeRepositoryTests/EpisodesWhereCutoffUnmetFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeRepositoryTests/EpisodesWithFilesFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeRepositoryTests/EpisodesBetweenDatesFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeServiceTests/FindEpisodeByTitleFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeServiceTests/HandleEpisodeFileDeletedFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/EpisodeServiceTests/ByAirDateFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/SeriesRepositoryTests/SeriesRepositoryFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/SeriesServiceTests/UpdateMultipleSeriesFixture.cs`
- `src/NzbDrone.Core.Test/TvTests/SeriesServiceTests/UpdateSeriesFixture.cs`

## Open questions
- Q-06 — see `questions.md#Q-06` (cutoff-unmet quality LIKE-match against JSON column — port strategy) — unresolved (blocks Phase 1)
- Q-07 — see `questions.md#Q-07` (Episode.AbsoluteEpisodeNumberAdded transient field semantics) — unresolved (blocks Phase 1)
- Q-08 — see `questions.md#Q-08` (RefreshSeriesCommand whole-library scheduling vs per-series — confirm scheduler integration) — unresolved (blocks Phase 1)
