using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Linq.Expressions;
using NLog;
using NzbDrone.Common.Extensions;
using NzbDrone.Common.Instrumentation.Extensions;
using NzbDrone.Core.Configuration;
using NzbDrone.Core.Datastore;
using NzbDrone.Core.Exceptions;
using NzbDrone.Core.MediaFiles;
using NzbDrone.Core.MediaFiles.Events;
using NzbDrone.Core.Messaging.Commands;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.MetadataSource;
using NzbDrone.Core.Tv.Commands;
using NzbDrone.Core.Tv.Events;

namespace NzbDrone.Core.Tv
{
    public class RefreshSeriesService : IExecute<RefreshSeriesCommand>
    {
        // Must match what RefreshSeriesInfo sets: anything else on the series belongs to other writers
        private static readonly Expression<Func<Series, object>>[] RefreshedFields =
        {
            s => s.TvdbId, s => s.Title, s => s.Year, s => s.TitleSlug, s => s.TvRageId, s => s.TvMazeId,
            s => s.TmdbId, s => s.ImdbId, s => s.MalIds, s => s.AniListIds, s => s.AirTime, s => s.Overview,
            s => s.OriginalLanguage, s => s.Status, s => s.CleanTitle, s => s.SortTitle, s => s.LastInfoSync,
            s => s.Runtime, s => s.Images, s => s.Network, s => s.FirstAired, s => s.LastAired, s => s.Ratings,
            s => s.Actors, s => s.Genres, s => s.Certification, s => s.OriginalCountry, s => s.OriginalTitle,
            s => s.CleanOriginalTitle, s => s.SeasonTypes, s => s.Seasons
        };

        private readonly IProvideSeriesInfo _seriesInfo;
        private readonly ISeriesService _seriesService;
        private readonly ISeriesTranslationService _seriesTranslationService;
        private readonly IRefreshEpisodeService _refreshEpisodeService;
        private readonly IEventAggregator _eventAggregator;
        private readonly IDiskScanService _diskScanService;
        private readonly ICheckIfSeriesShouldBeRefreshed _checkIfSeriesShouldBeRefreshed;
        private readonly IConfigService _configService;
        private readonly ICommandResultReporter _commandResultReporter;
        private readonly Logger _logger;

        public RefreshSeriesService(IProvideSeriesInfo seriesInfo,
                                    ISeriesService seriesService,
                                    ISeriesTranslationService seriesTranslationService,
                                    IRefreshEpisodeService refreshEpisodeService,
                                    IEventAggregator eventAggregator,
                                    IDiskScanService diskScanService,
                                    ICheckIfSeriesShouldBeRefreshed checkIfSeriesShouldBeRefreshed,
                                    IConfigService configService,
                                    ICommandResultReporter commandResultReporter,
                                    Logger logger)
        {
            _seriesInfo = seriesInfo;
            _seriesService = seriesService;
            _seriesTranslationService = seriesTranslationService;
            _refreshEpisodeService = refreshEpisodeService;
            _eventAggregator = eventAggregator;
            _diskScanService = diskScanService;
            _checkIfSeriesShouldBeRefreshed = checkIfSeriesShouldBeRefreshed;
            _configService = configService;
            _commandResultReporter = commandResultReporter;
            _logger = logger;
        }

        private void RefreshSeriesInfo(int seriesId, bool lookedUpAgain = false)
        {
            var series = _seriesService.GetSeries(seriesId);

            _logger.ProgressInfo("Updating {0}", series.Title);

            Series seriesInfo;
            List<Episode> episodes;

            try
            {
                var tuple = _seriesInfo.GetSeriesInfo(series.TvdbId, series.Language, series.SeasonType);
                seriesInfo = tuple.Item1;
                episodes = tuple.Item2;
            }
            catch (SeriesNotFoundException)
            {
                if (series.Status != SeriesStatusType.Deleted)
                {
                    series.Status = SeriesStatusType.Deleted;
                    _seriesService.UpdateSeriesFields(series, s => s.Status);
                    _logger.Debug("Series marked as deleted on tvdb for {0}", series.Title);
                    _eventAggregator.PublishEvent(new SeriesUpdatedEvent(series));
                }

                throw;
            }

            var lookedUp = series;

            // Changes saved during the lookup (a finished move, a season toggled) are on the stored copy, not this one
            series = _seriesService.GetSeries(seriesId);

            // The lookup only fits the ID, language and episode order it asked for
            if (series.TvdbId != lookedUp.TvdbId || series.Language != lookedUp.Language || series.SeasonType != lookedUp.SeasonType)
            {
                if (lookedUpAgain)
                {
                    _logger.Debug("{0} changed again during its lookup, leaving it for the next refresh", series.Title);
                    return;
                }

                RefreshSeriesInfo(seriesId, true);
                return;
            }

            if (series.TvdbId != seriesInfo.TvdbId)
            {
                _logger.Warn("Series '{0}' (tvdbid {1}) was replaced with '{2}' (tvdbid {3}), because the original was a duplicate.", series.Title, series.TvdbId, seriesInfo.Title, seriesInfo.TvdbId);
                series.TvdbId = seriesInfo.TvdbId;
            }

            series.Title = seriesInfo.Title;
            series.Year = seriesInfo.Year;
            series.TitleSlug = seriesInfo.TitleSlug;
            series.TvRageId = seriesInfo.TvRageId;
            series.TvMazeId = seriesInfo.TvMazeId;
            series.TmdbId = seriesInfo.TmdbId;
            series.ImdbId = seriesInfo.ImdbId;
            series.MalIds = seriesInfo.MalIds;
            series.AniListIds = seriesInfo.AniListIds;
            series.AirTime = seriesInfo.AirTime;
            series.Overview = seriesInfo.Overview;
            series.OriginalLanguage = seriesInfo.OriginalLanguage;
            series.Status = seriesInfo.Status;
            series.CleanTitle = seriesInfo.CleanTitle;
            series.SortTitle = seriesInfo.SortTitle;
            series.LastInfoSync = DateTime.UtcNow;
            series.Runtime = seriesInfo.Runtime;
            series.Images = seriesInfo.Images;
            series.Network = seriesInfo.Network;
            series.FirstAired = seriesInfo.FirstAired;
            series.LastAired = seriesInfo.LastAired;
            series.Ratings = seriesInfo.Ratings;
            series.Actors = seriesInfo.Actors;
            series.Genres = seriesInfo.Genres;
            series.Certification = seriesInfo.Certification;
            series.OriginalCountry = seriesInfo.OriginalCountry;
            series.OriginalTitle = seriesInfo.OriginalTitle;
            series.CleanOriginalTitle = seriesInfo.CleanOriginalTitle;
            series.SeasonTypes = seriesInfo.SeasonTypes;

            var fields = RefreshedFields.ToList();

            // A pending move owns the path until it finishes
            if (series.PendingPath.IsNullOrWhiteSpace())
            {
                var path = series.Path;

                try
                {
                    path = new DirectoryInfo(path).FullName;
                    path = path.GetActualCasing();
                }
                catch (Exception e)
                {
                    _logger.Warn(e, "Couldn't update series path for " + series.Path);
                }

                if (path != series.Path)
                {
                    series.Path = path;
                    fields.Add(s => s.Path);
                }
            }

            series.Seasons = UpdateSeasons(series, seriesInfo);

            _seriesService.UpdateSeriesFields(series, fields.ToArray());
            _seriesTranslationService.UpdateTranslations(series.Id, seriesInfo.Translations);
            _refreshEpisodeService.RefreshEpisodeInfo(series, episodes);

            _logger.Debug("Finished series refresh for {0}", series.Title);
            _eventAggregator.PublishEvent(new SeriesUpdatedEvent(series));
        }

        private List<Season> UpdateSeasons(Series series, Series seriesInfo)
        {
            var seasons = seriesInfo.Seasons.DistinctBy(s => s.SeasonNumber).ToList();

            foreach (var season in seasons)
            {
                var existingSeason = series.Seasons.FirstOrDefault(s => s.SeasonNumber == season.SeasonNumber);

                if (existingSeason == null)
                {
                    if (season.SeasonNumber == 0)
                    {
                        _logger.Debug("Ignoring season 0 for series [{0}] {1} by default", series.TvdbId, series.Title);
                        season.Monitored = false;
                        continue;
                    }

                    var monitorNewSeasons = series.MonitorNewItems == NewItemMonitorTypes.All;

                    _logger.Debug("New season ({0}) for series: [{1}] {2}, setting monitored to {3}", season.SeasonNumber, series.TvdbId, series.Title, monitorNewSeasons.ToString().ToLowerInvariant());
                    season.Monitored = monitorNewSeasons;
                }
                else
                {
                    season.Monitored = existingSeason.Monitored;
                }
            }

            return seasons;
        }

        private void RescanSeries(Series series, bool isNew, CommandTrigger trigger)
        {
            var rescanAfterRefresh = _configService.RescanAfterRefresh;

            if (isNew)
            {
                _logger.Trace("Forcing rescan of {0}. Reason: New series", series);
            }
            else if (rescanAfterRefresh == RescanAfterRefreshType.Never)
            {
                _logger.Trace("Skipping rescan of {0}. Reason: never rescan after refresh", series);
                _eventAggregator.PublishEvent(new SeriesScanSkippedEvent(series, SeriesScanSkippedReason.NeverRescanAfterRefresh));

                return;
            }
            else if (rescanAfterRefresh == RescanAfterRefreshType.AfterManual && trigger != CommandTrigger.Manual)
            {
                _logger.Trace("Skipping rescan of {0}. Reason: not after automatic scans", series);
                _eventAggregator.PublishEvent(new SeriesScanSkippedEvent(series, SeriesScanSkippedReason.RescanAfterManualRefreshOnly));

                return;
            }

            try
            {
                _diskScanService.Scan(series);
            }
            catch (Exception e)
            {
                _logger.Error(e, "Couldn't rescan series {0}", series);
            }
        }

        // Callers can hold a copy read long before (a full refresh reads every series up front), so work from fresh reads
        private void UpdateTagsAndRescan(int seriesId, bool isNew, CommandTrigger trigger)
        {
            try
            {
                UpdateTags(_seriesService.GetSeries(seriesId));

                // A move can finish while the tags are worked out, and scanning a path that's gone drops every episode file
                RescanSeries(_seriesService.GetSeries(seriesId), isNew, trigger);
            }
            catch (ModelNotFoundException)
            {
                _logger.Debug("Series {0} was deleted during the refresh, skipping its tags and rescan", seriesId);
            }
        }

        private void UpdateTags(Series series)
        {
            var tagsBefore = new HashSet<int>(series.Tags);

            if (!_seriesService.UpdateAutoTaggingTags(series))
            {
                return;
            }

            // Apply the change to a fresh read, so a tag edit saved while the rules ran stays
            var storedSeries = _seriesService.GetSeries(series.Id);
            var updatedSeries = _seriesService.GetSeries(series.Id);

            updatedSeries.Tags.ExceptWith(tagsBefore.Except(series.Tags).ToList());
            updatedSeries.Tags.UnionWith(series.Tags.Except(tagsBefore).ToList());

            _seriesService.UpdateSeriesFields(updatedSeries, s => s.Tags);
            _eventAggregator.PublishEvent(new SeriesEditedEvent(updatedSeries, storedSeries));
        }

        public void Execute(RefreshSeriesCommand message)
        {
            var trigger = message.Trigger;
            var isNew = message.IsNewSeries;
            _eventAggregator.PublishEvent(new SeriesRefreshStartingEvent(trigger == CommandTrigger.Manual));

            if (message.SeriesIds.Any())
            {
                foreach (var seriesId in message.SeriesIds)
                {
                    var series = _seriesService.GetSeries(seriesId);

                    try
                    {
                        RefreshSeriesInfo(seriesId);
                        UpdateTagsAndRescan(seriesId, isNew, trigger);
                    }
                    catch (SeriesNotFoundException)
                    {
                        _logger.Error("Series '{0}' (tvdbid {1}) was not found, it may have been removed from TheTVDB.", series.Title, series.TvdbId);

                        // Mark the result as indeterminate so it's not marked as a full success,
                        // but we can still process other series if needed.
                        _commandResultReporter.Report(CommandResult.Indeterminate);
                    }
                    catch (Exception e)
                    {
                        _logger.Error(e, "Couldn't refresh info for {0}", series);
                        UpdateTagsAndRescan(seriesId, isNew, trigger);

                        // Mark the result as indeterminate so it's not marked as a full success,
                        // but we can still process other series if needed.
                        _commandResultReporter.Report(CommandResult.Indeterminate);
                    }
                }
            }
            else
            {
                var allSeries = _seriesService.GetAllSeries().OrderBy(c => c.SortTitle).ToList();

                foreach (var series in allSeries)
                {
                    var seriesLocal = series;
                    if (trigger == CommandTrigger.Manual || _checkIfSeriesShouldBeRefreshed.ShouldRefresh(seriesLocal))
                    {
                        try
                        {
                            RefreshSeriesInfo(seriesLocal.Id);
                        }
                        catch (SeriesNotFoundException)
                        {
                            _logger.Error("Series '{0}' (tvdbid {1}) was not found, it may have been removed from TheTVDB.", seriesLocal.Title, seriesLocal.TvdbId);
                            continue;
                        }
                        catch (Exception e)
                        {
                            _logger.Error(e, "Couldn't refresh info for {0}", seriesLocal);
                        }

                        UpdateTagsAndRescan(series.Id, false, trigger);
                    }
                    else
                    {
                        _logger.Info("Skipping refresh of series: {0}", seriesLocal.Title);
                        UpdateTagsAndRescan(series.Id, false, trigger);
                    }
                }
            }

            _eventAggregator.PublishEvent(new SeriesRefreshCompleteEvent());
        }
    }
}
