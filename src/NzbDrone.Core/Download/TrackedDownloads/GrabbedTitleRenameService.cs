using System;
using System.Collections.Generic;
using System.Linq;
using NLog;
using NzbDrone.Common.Cache;
using NzbDrone.Core.CustomFormats;
using NzbDrone.Core.History;
using NzbDrone.Core.Indexers;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.Parser;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Qualities;

namespace NzbDrone.Core.Download.TrackedDownloads
{
    public class GrabbedTitleRenameService : IHandleAsync<TrackedDownloadRefreshedEvent>
    {
        private static readonly TimeSpan RenameRetryDelay = TimeSpan.FromMinutes(15);

        private readonly IProvideDownloadClient _downloadClientProvider;
        private readonly ICustomFormatCalculationService _formatCalculator;
        private readonly IHistoryService _historyService;
        private readonly ICached<bool> _renameAttempts;
        private readonly Logger _logger;

        public GrabbedTitleRenameService(IProvideDownloadClient downloadClientProvider,
                                         ICustomFormatCalculationService formatCalculator,
                                         IHistoryService historyService,
                                         ICacheManager cacheManager,
                                         Logger logger)
        {
            _downloadClientProvider = downloadClientProvider;
            _formatCalculator = formatCalculator;
            _historyService = historyService;
            _renameAttempts = cacheManager.GetCache<bool>(GetType());
            _logger = logger;
        }

        public void HandleAsync(TrackedDownloadRefreshedEvent message)
        {
            foreach (var trackedDownload in message.TrackedDownloads)
            {
                var downloadItem = trackedDownload.DownloadItem;

                try
                {
                    if (!ShouldRename(trackedDownload))
                    {
                        continue;
                    }

                    // Republished events still carry the old name until the next refresh, so one attempt covers them
                    _renameAttempts.Set(AttemptKey(trackedDownload), true, RenameRetryDelay);
                    _downloadClientProvider.Get(trackedDownload.DownloadClient).RenameItem(downloadItem, trackedDownload.GrabbedEvent.SourceTitle);
                }
                catch (Exception e)
                {
                    _logger.Warn(e, "Couldn't rename {0} to its grabbed release title", downloadItem.Title);
                }
            }
        }

        private bool ShouldRename(TrackedDownload trackedDownload)
        {
            if (!trackedDownload.IsTrackable ||
                trackedDownload.Protocol != DownloadProtocol.Torrent ||
                trackedDownload.State is not (TrackedDownloadState.Downloading or TrackedDownloadState.ImportBlocked))
            {
                return false;
            }

            var grabbedEvent = trackedDownload.GrabbedEvent;
            var remoteEpisode = trackedDownload.RemoteEpisode;

            if (grabbedEvent == null || remoteEpisode?.Series == null || remoteEpisode.ParsedEpisodeInfo == null || remoteEpisode.CustomFormats == null)
            {
                return false;
            }

            // Import finds the series by the client name; an ID-matched grab title wouldn't parse to it and would block the import
            if (grabbedEvent.Data.GetValueOrDefault(EpisodeHistory.SERIES_MATCH_TYPE) == nameof(SeriesMatchType.Id))
            {
                return false;
            }

            // Scores can still differ once renamed (client vs indexer size), so matching names must end it
            if (trackedDownload.DownloadItem.Title.IsSameReleaseTitle(grabbedEvent.SourceTitle) ||
                _renameAttempts.Find(AttemptKey(trackedDownload)))
            {
                return false;
            }

            if (!IsGrabbedTitleBetter(grabbedEvent, remoteEpisode))
            {
                return false;
            }

            if (!IsGrabbedForSameEpisodes(trackedDownload))
            {
                _logger.Debug("Not renaming {0}, its grabbed release title is for other episodes", trackedDownload.DownloadItem.Title);
                return false;
            }

            return true;
        }

        private bool IsGrabbedTitleBetter(EpisodeHistory grabbedEvent, RemoteEpisode remoteEpisode)
        {
            var qualityProfile = remoteEpisode.Series.QualityProfile.Value;
            var qualityComparison = new QualityModelComparer(qualityProfile).Compare(grabbedEvent.Quality, remoteEpisode.ParsedEpisodeInfo.Quality);

            if (qualityComparison != 0)
            {
                return qualityComparison > 0;
            }

            var grabbedScore = qualityProfile.CalculateCustomFormatScore(_formatCalculator.ParseCustomFormat(grabbedEvent, remoteEpisode.Series));

            return grabbedScore > qualityProfile.CalculateCustomFormatScore(remoteEpisode.CustomFormats);
        }

        // The next refresh maps the download by its new name, so a rename must not move it to other episodes
        private bool IsGrabbedForSameEpisodes(TrackedDownload trackedDownload)
        {
            var remoteEpisode = trackedDownload.RemoteEpisode;
            var grabs = _historyService.Find(trackedDownload.DownloadItem.DownloadId, EpisodeHistoryEventType.Grabbed);

            return grabs.Any() &&
                   grabs.All(h => h.SeriesId == remoteEpisode.Series.Id) &&
                   grabs.Select(h => h.EpisodeId).ToHashSet().SetEquals(remoteEpisode.Episodes.Select(e => e.Id));
        }

        private static string AttemptKey(TrackedDownload trackedDownload)
        {
            return $"{trackedDownload.DownloadClient}_{trackedDownload.DownloadItem.DownloadId}";
        }
    }
}
