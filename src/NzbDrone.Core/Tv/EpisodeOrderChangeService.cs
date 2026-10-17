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
        private readonly IExecute<RefreshSeriesCommand> _refreshSeries;
        private readonly IEventAggregator _eventAggregator;
        private readonly Logger _logger;

        public EpisodeOrderChangeService(ISeriesService seriesService,
                                         IEpisodeService episodeService,
                                         IMediaFileService mediaFileService,
                                         IProvideSeriesInfo seriesInfo,
                                         IRenameEpisodeFileService renameEpisodeFileService,
                                         IDiskProvider diskProvider,
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

            var previewFiles = linkedFiles.Select(f => new EpisodeOrderPreviewFile
                {
                    EpisodeFileId = f.Id,
                    ExistingPath = f.RelativePath,
                    NewPath = unmatchedFileIds.Contains(f.Id) ? null : renames.GetValueOrDefault(f.Id)?.NewPath ?? f.RelativePath,
                    ExistingEpisodes = existingEpisodes[f.Id].OrderBy(e => e.SeasonNumber).ThenBy(e => e.EpisodeNumber).ToList(),
                    NewEpisodes = unmatchedFileIds.Contains(f.Id)
                        ? new List<EpisodeOrderPreviewEpisode>()
                        : episodes.Where(e => e.EpisodeFileId == f.Id).Select(ToPreviewEpisode).OrderBy(e => e.SeasonNumber).ThenBy(e => e.EpisodeNumber).ToList(),
                    Status = unmatchedFileIds.Contains(f.Id)
                        ? EpisodeOrderPreviewFileStatus.Unmatched
                        : renames.ContainsKey(f.Id) ? EpisodeOrderPreviewFileStatus.Renamed : EpisodeOrderPreviewFileStatus.Unchanged
                })
                .OrderBy(f => f.NewEpisodes.FirstOrDefault()?.SeasonNumber ?? int.MaxValue)
                .ThenBy(f => f.NewEpisodes.FirstOrDefault()?.EpisodeNumber ?? int.MaxValue)
                .ToList();

            MarkConflicts(series, previewFiles);

            return new EpisodeOrderPreview
            {
                SeriesId = series.Id,
                SeasonType = seasonType,
                RefreshRequired = episodesWithFiles.Any(e => e.TvdbId == 0),
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

            if (preview.RefreshRequired)
            {
                throw new CommandFailedException("Refresh {0} first so its episodes have TVDB IDs", series.Title);
            }

            var unmatchedCount = preview.Files.Count(f => f.Status == EpisodeOrderPreviewFileStatus.Unmatched);

            if (unmatchedCount > 0)
            {
                throw new CommandFailedException("{0} episode files belong to episodes that aren't in the new episode order", unmatchedCount);
            }

            if (series.SeasonType != message.SeasonType)
            {
                _logger.Info("Changing episode order for {0} from {1} to {2}", series, series.SeasonType, message.SeasonType);
                series.SeasonType = message.SeasonType;
                _seriesService.UpdateSeries(series, updateEpisodesToMatchSeason: false, publishUpdatedEvent: false);
            }

            _refreshSeries.Execute(new RefreshSeriesCommand(new List<int> { series.Id }) { Trigger = CommandTrigger.Manual });

            var episodesByFile = _episodeService.GetEpisodeBySeries(series.Id).Where(e => e.EpisodeFileId > 0).ToLookup(e => e.EpisodeFileId);
            var notAppliedCount = preview.Files.Count(f => !IsApplied(f, episodesByFile[f.EpisodeFileId]));

            if (notAppliedCount > 0)
            {
                throw new CommandFailedException("The new episode order didn't apply to {0} episode files, so no files were renamed", notAppliedCount);
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

        private static bool IsApplied(EpisodeOrderPreviewFile file, IEnumerable<Episode> episodes)
        {
            var applied = episodes.Select(ToPreviewEpisode).OrderBy(e => e.SeasonNumber).ThenBy(e => e.EpisodeNumber).ToList();

            return applied.Count == file.NewEpisodes.Count &&
                   applied.Zip(file.NewEpisodes).All(p => p.First.SeasonNumber == p.Second.SeasonNumber && p.First.EpisodeNumber == p.Second.EpisodeNumber);
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
