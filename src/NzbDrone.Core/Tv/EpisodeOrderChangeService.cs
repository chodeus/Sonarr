using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net;
using NLog;
using NzbDrone.Common;
using NzbDrone.Common.Disk;
using NzbDrone.Common.Extensions;
using NzbDrone.Common.Instrumentation.Extensions;
using NzbDrone.Core.DataAugmentation.Scene;
using NzbDrone.Core.Exceptions;
using NzbDrone.Core.MediaFiles;
using NzbDrone.Core.MediaFiles.Events;
using NzbDrone.Core.Messaging.Commands;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.MetadataSource;
using NzbDrone.Core.Tv.Commands;

namespace NzbDrone.Core.Tv
{
    public interface IEpisodeOrderChangeService
    {
        EpisodeOrderPreview Preview(int seriesId, string seasonType);
    }

    public class EpisodeOrderChangeService : IEpisodeOrderChangeService, IExecute<ChangeEpisodeOrderCommand>
    {
        private readonly ISeriesService _seriesService;
        private readonly IEpisodeService _episodeService;
        private readonly IMediaFileService _mediaFileService;
        private readonly IProvideSeriesInfo _seriesInfo;
        private readonly IRenameEpisodeFileService _renameEpisodeFileService;
        private readonly IDiskProvider _diskProvider;
        private readonly ISceneMappingService _sceneMappingService;
        private readonly IExecute<RefreshSeriesCommand> _refreshSeries;
        private readonly IEventAggregator _eventAggregator;
        private readonly Logger _logger;

        public EpisodeOrderChangeService(ISeriesService seriesService,
                                         IEpisodeService episodeService,
                                         IMediaFileService mediaFileService,
                                         IProvideSeriesInfo seriesInfo,
                                         IRenameEpisodeFileService renameEpisodeFileService,
                                         IDiskProvider diskProvider,
                                         ISceneMappingService sceneMappingService,
                                         IExecute<RefreshSeriesCommand> refreshSeries,
                                         IEventAggregator eventAggregator,
                                         Logger logger)
        {
            _seriesService = seriesService;
            _episodeService = episodeService;
            _mediaFileService = mediaFileService;
            _seriesInfo = seriesInfo;
            _renameEpisodeFileService = renameEpisodeFileService;
            _diskProvider = diskProvider;
            _sceneMappingService = sceneMappingService;
            _refreshSeries = refreshSeries;
            _eventAggregator = eventAggregator;
            _logger = logger;
        }

        public EpisodeOrderPreview Preview(int seriesId, string seasonType)
        {
            // Never saved: the preview applies the new order to this copy of the series and its episodes
            var series = _seriesService.GetSeries(seriesId);

            if (seasonType.IsNullOrWhiteSpace() ||
                (seasonType != SeasonType.Official && series.SeasonTypes?.Any(t => t.Type == seasonType) != true))
            {
                throw new NzbDroneClientException(HttpStatusCode.BadRequest, "{0} has no episode order '{1}'", series.Title, seasonType);
            }

            var seriesInfo = _seriesInfo.GetSeriesInfo(series.TvdbId, series.Language, seasonType);
            var episodes = _episodeService.GetEpisodeBySeries(series.Id);
            var files = _mediaFileService.GetFilesBySeries(series.Id);

            var remoteEpisodes = RemoteEpisodeMatcher.Distinct(series, seriesInfo.Item2);
            var matches = RemoteEpisodeMatcher.Match(remoteEpisodes, episodes);
            var tvdbIdMatches = matches.Where(m => RemoteEpisodeMatcher.IsMatchedByTvdbId(m.Key, m.Value)).ToList();
            var tvdbIdMatched = tvdbIdMatches.Select(m => m.Value).ToHashSet(ReferenceEqualityComparer.Instance);

            // Other orders turn off XEM and scene-title seasons, which releases for this series are matched through
            var leavingAiredOrder = series.SeasonType == SeasonType.Official && seasonType != SeasonType.Official;
            var sceneNumberedEpisodes = leavingAiredOrder ? episodes.Count(IsSceneRenumbered) : 0;
            var sceneSeasonTitles = leavingAiredOrder ? _sceneMappingService.FindByTvdbId(series.TvdbId).Count(m => m.SeasonNumber.HasValue || m.SceneSeasonNumber.HasValue) : 0;

            var episodesWithFiles = episodes.Where(e => e.EpisodeFileId > 0).ToList();
            var existingEpisodes = episodesWithFiles.ToLookup(e => e.EpisodeFileId, ToPreviewEpisode);
            var unmatchedFileIds = episodesWithFiles.Where(e => !tvdbIdMatched.Contains(e)).Select(e => e.EpisodeFileId).ToHashSet();

            foreach (var match in tvdbIdMatches)
            {
                RemoteEpisodeMatcher.Apply(match.Value, match.Key);
            }

            series.SeasonType = seasonType;
            series.Seasons = seriesInfo.Item1.Seasons;

            var linkedFiles = files.Where(f => existingEpisodes.Contains(f.Id)).ToList();
            var renames = _renameEpisodeFileService.GetRenamePreviews(series, episodes, linkedFiles.Where(f => !unmatchedFileIds.Contains(f.Id)).ToList())
                                                   .ToDictionary(p => p.EpisodeFileId);

            var previewFiles = linkedFiles.Select(f => ToPreviewFile(f, episodes.Where(e => e.EpisodeFileId == f.Id).ToList(), existingEpisodes[f.Id], unmatchedFileIds.Contains(f.Id), renames.GetValueOrDefault(f.Id)))
                .OrderBy(f => f.NewEpisodes.FirstOrDefault()?.SeasonNumber ?? int.MaxValue)
                .ThenBy(f => f.NewEpisodes.FirstOrDefault()?.EpisodeNumber ?? int.MaxValue)
                .ToList();

            MarkConflicts(series, previewFiles);

            var refreshRequired = episodesWithFiles.Any(e => e.TvdbId == 0);

            return new EpisodeOrderPreview
            {
                SeriesId = series.Id,
                SeasonType = seasonType,
                CanChange = !refreshRequired &&
                            sceneNumberedEpisodes == 0 &&
                            sceneSeasonTitles == 0 &&
                            previewFiles.None(f => f.Status is EpisodeOrderPreviewFileStatus.Unmatched or EpisodeOrderPreviewFileStatus.Unnameable),
                RefreshRequired = refreshRequired,
                SceneNumberedEpisodes = sceneNumberedEpisodes,
                SceneSeasonTitles = sceneSeasonTitles,
                HasAbsoluteEpisodeNumbers = remoteEpisodes.Any(e => e.AbsoluteEpisodeNumber.HasValue),
                EpisodesAdded = remoteEpisodes.Count - matches.Count,
                EpisodesRemoved = episodes.Count - matches.Count,
                Files = previewFiles
            };
        }

        public void Execute(ChangeEpisodeOrderCommand message)
        {
            var preview = Preview(message.SeriesId, message.SeasonType);
            var series = _seriesService.GetSeries(message.SeriesId);

            if (!preview.CanChange)
            {
                throw new CommandFailedException(GetRefusal(preview, series.Title));
            }

            var previousSeasonType = series.SeasonType;

            if (previousSeasonType != message.SeasonType)
            {
                _logger.Info("Changing episode order for {0} from {1} to {2}", series, previousSeasonType, message.SeasonType);
                SaveSeasonType(series, message.SeasonType);
            }

            try
            {
                Refresh(series.Id);

                var notAppliedCount = CountNotApplied(series.Id, preview.Files, f => f.NewEpisodes);

                if (notAppliedCount > 0)
                {
                    throw new InvalidOperationException($"The new episode order didn't apply to {notAppliedCount} episode files");
                }
            }
            catch (Exception ex)
            {
                Restore(series.Id, previousSeasonType, preview);

                throw new CommandFailedException("Couldn't apply the new episode order to {0}, so it was put back on its previous order and no files were renamed", ex, series.Title);
            }

            if (!message.RenameFiles)
            {
                return;
            }

            series = _seriesService.GetSeries(series.Id);
            var renamedCount = RenameFiles(series);

            _logger.ProgressInfo("{0} episode files renamed for {1}", renamedCount, series.Title);
            _eventAggregator.PublishEvent(new RenameCompletedEvent());
        }

        private void Restore(int seriesId, string seasonType, EpisodeOrderPreview preview)
        {
            try
            {
                var series = _seriesService.GetSeries(seriesId);

                if (series.SeasonType != seasonType)
                {
                    SaveSeasonType(series, seasonType);
                }

                // Matching by TVDB ID moves any renumbered episodes back with their files
                Refresh(seriesId);

                var notRestoredCount = CountNotApplied(seriesId, preview.Files, f => f.ExistingEpisodes);

                if (notRestoredCount > 0)
                {
                    _logger.Error("{0} episode files of {1} still have the new episode numbers; refresh the series to finish restoring", notRestoredCount, series.Title);
                }
            }
            catch (Exception ex)
            {
                _logger.Error(ex, "Couldn't restore the previous episode order for series {0}; refresh it to finish restoring", seriesId);
            }
        }

        private void SaveSeasonType(Series series, string seasonType)
        {
            series.SeasonType = seasonType;
            _seriesService.UpdateSeries(series, updateEpisodesToMatchSeason: false, publishUpdatedEvent: false);
        }

        private void Refresh(int seriesId)
        {
            _refreshSeries.Execute(new RefreshSeriesCommand(new List<int> { seriesId }) { Trigger = CommandTrigger.Manual });
        }

        private int CountNotApplied(int seriesId, List<EpisodeOrderPreviewFile> files, Func<EpisodeOrderPreviewFile, List<EpisodeOrderPreviewEpisode>> expected)
        {
            var episodesByFile = _episodeService.GetEpisodeBySeries(seriesId).Where(e => e.EpisodeFileId > 0).ToLookup(e => e.EpisodeFileId);

            return files.Count(f => !IsApplied(expected(f), episodesByFile[f.EpisodeFileId]));
        }

        private static string GetRefusal(EpisodeOrderPreview preview, string title)
        {
            if (preview.RefreshRequired)
            {
                return $"Refresh {title} first so its episodes have TVDB IDs";
            }

            if (preview.SceneNumberedEpisodes > 0 || preview.SceneSeasonTitles > 0)
            {
                return $"Releases for {title} are matched through XEM scene numbering and scene titles, which other episode orders turn off";
            }

            var unmatchedCount = preview.Files.Count(f => f.Status == EpisodeOrderPreviewFileStatus.Unmatched);

            if (unmatchedCount > 0)
            {
                return $"{unmatchedCount} episode files belong to episodes that aren't in the new episode order";
            }

            return $"{preview.Files.Count(f => f.Status == EpisodeOrderPreviewFileStatus.Unnameable)} combined episode files can't be named for their episodes in the new episode order";
        }

        private int RenameFiles(Series series)
        {
            var renamedCount = 0;
            var pendingIds = _renameEpisodeFileService.GetRenamePreviews(series.Id).Select(p => p.EpisodeFileId).ToList();

            // Repeat while the pending list shrinks, so a file whose destination another file is leaving gets its turn
            while (pendingIds.Any())
            {
                renamedCount += _renameEpisodeFileService.RenameFiles(_mediaFileService.Get(pendingIds), series).Count;

                var stillPendingIds = _renameEpisodeFileService.GetRenamePreviews(series.Id).Select(p => p.EpisodeFileId).ToList();

                if (stillPendingIds.Count >= pendingIds.Count)
                {
                    _logger.Warn("{0} episode files for {1} weren't renamed, their destinations are in use", stillPendingIds.Count, series.Title);
                    break;
                }

                pendingIds = stillPendingIds;
            }

            return renamedCount;
        }

        private void MarkConflicts(Series series, List<EpisodeOrderPreviewFile> files)
        {
            var renamed = files.Where(f => f.Status == EpisodeOrderPreviewFileStatus.Renamed).ToList();
            var leavingByPath = renamed.ToDictionary(f => Path.Combine(series.Path, f.ExistingPath), PathEqualityComparer.Instance);
            var destinationCounts = files.GroupBy(f => Path.Combine(series.Path, f.NewPath ?? f.ExistingPath), PathEqualityComparer.Instance)
                                         .ToDictionary(g => g.Key, g => g.Count(), PathEqualityComparer.Instance);

            EpisodeOrderPreviewFile OccupantOf(EpisodeOrderPreviewFile file) => leavingByPath.GetValueOrDefault(Path.Combine(series.Path, file.NewPath));

            foreach (var file in renamed)
            {
                var destination = Path.Combine(series.Path, file.NewPath);
                var occupant = OccupantOf(file);

                // A case-only rename finds the file itself at its destination
                if (occupant == file)
                {
                    continue;
                }

                if (destinationCounts[destination] > 1 ||
                    (occupant == null && _diskProvider.FileExists(destination)) ||
                    (occupant != null && IsInCycle(file, OccupantOf)))
                {
                    file.Status = EpisodeOrderPreviewFileStatus.Conflict;
                }
            }

            // A file waiting on a destination held by a conflicted file can't move either
            bool blocked;

            do
            {
                blocked = false;

                foreach (var file in renamed.Where(f => f.Status == EpisodeOrderPreviewFileStatus.Renamed))
                {
                    var occupant = OccupantOf(file);

                    if (occupant != null && occupant != file && occupant.Status == EpisodeOrderPreviewFileStatus.Conflict)
                    {
                        file.Status = EpisodeOrderPreviewFileStatus.Conflict;
                        blocked = true;
                    }
                }
            }
            while (blocked);
        }

        private static bool IsInCycle(EpisodeOrderPreviewFile file, Func<EpisodeOrderPreviewFile, EpisodeOrderPreviewFile> occupantOf)
        {
            var visited = new HashSet<EpisodeOrderPreviewFile>();
            var next = occupantOf(file);

            while (next != null && visited.Add(next))
            {
                if (next == file)
                {
                    return true;
                }

                next = occupantOf(next);
            }

            return false;
        }

        private static bool IsApplied(List<EpisodeOrderPreviewEpisode> expected, IEnumerable<Episode> episodes)
        {
            var applied = episodes.Select(ToPreviewEpisode).OrderBy(e => e.SeasonNumber).ThenBy(e => e.EpisodeNumber).ToList();

            return applied.Count == expected.Count &&
                   applied.Zip(expected).All(p => p.First.SeasonNumber == p.Second.SeasonNumber && p.First.EpisodeNumber == p.Second.EpisodeNumber);
        }

        private static EpisodeOrderPreviewFile ToPreviewFile(EpisodeFile file, List<Episode> newEpisodes, IEnumerable<EpisodeOrderPreviewEpisode> existingEpisodes, bool unmatched, RenameEpisodeFilePreview rename)
        {
            var newPath = unmatched ? null : rename?.NewPath ?? file.RelativePath;

            return new EpisodeOrderPreviewFile
            {
                EpisodeFileId = file.Id,
                ExistingPath = file.RelativePath,
                NewPath = newPath,
                ExistingEpisodes = existingEpisodes.OrderBy(e => e.SeasonNumber).ThenBy(e => e.EpisodeNumber).ToList(),
                NewEpisodes = unmatched
                    ? new List<EpisodeOrderPreviewEpisode>()
                    : newEpisodes.Select(ToPreviewEpisode).OrderBy(e => e.SeasonNumber).ThenBy(e => e.EpisodeNumber).ToList(),
                Status = unmatched ? EpisodeOrderPreviewFileStatus.Unmatched
                    : !NameDescribesEpisodes(newPath, newEpisodes) ? EpisodeOrderPreviewFileStatus.Unnameable
                    : rename != null ? EpisodeOrderPreviewFileStatus.Renamed
                    : EpisodeOrderPreviewFileStatus.Unchanged
            };
        }

        // Multi-episode styles write ranges and the first episode's season, so read the name back with the real parser
        private static bool NameDescribesEpisodes(string relativePath, List<Episode> episodes)
        {
            if (episodes.Count < 2)
            {
                return true;
            }

            var parsed = Parser.Parser.ParsePath(relativePath);

            if (parsed == null)
            {
                return false;
            }

            if (parsed.EpisodeNumbers.Any())
            {
                return episodes.All(e => e.SeasonNumber == parsed.SeasonNumber) &&
                       parsed.EpisodeNumbers.Order().SequenceEqual(episodes.Select(e => e.EpisodeNumber).Order());
            }

            return parsed.AbsoluteEpisodeNumbers.Any() &&
                   episodes.All(e => e.AbsoluteEpisodeNumber.HasValue) &&
                   parsed.AbsoluteEpisodeNumbers.Order().SequenceEqual(episodes.Select(e => e.AbsoluteEpisodeNumber.Value).Order());
        }

        private static bool IsSceneRenumbered(Episode episode)
        {
            return (episode.SceneSeasonNumber.HasValue && (episode.SceneSeasonNumber != episode.SeasonNumber || episode.SceneEpisodeNumber != episode.EpisodeNumber)) ||
                   (episode.SceneAbsoluteEpisodeNumber.HasValue && episode.SceneAbsoluteEpisodeNumber != episode.AbsoluteEpisodeNumber);
        }

        private static EpisodeOrderPreviewEpisode ToPreviewEpisode(Episode episode)
        {
            return new EpisodeOrderPreviewEpisode
            {
                SeasonNumber = episode.SeasonNumber,
                EpisodeNumber = episode.EpisodeNumber
            };
        }
    }
}
