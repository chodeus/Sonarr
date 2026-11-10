using System;
using System.Collections.Generic;
using System.Linq;
using NLog;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.Configuration;
using NzbDrone.Core.History;
using NzbDrone.Core.Parser;
using NzbDrone.Core.Parser.Model;

namespace NzbDrone.Core.DecisionEngine.Specifications
{
    public class AlreadyImportedSpecification : IDownloadDecisionEngineSpecification
    {
        private readonly IHistoryService _historyService;
        private readonly IConfigService _configService;
        private readonly Logger _logger;

        public AlreadyImportedSpecification(IHistoryService historyService,
                                            IConfigService configService,
                                            Logger logger)
        {
            _historyService = historyService;
            _configService = configService;
            _logger = logger;
        }

        public SpecificationPriority Priority => SpecificationPriority.Database;
        public RejectionType Type => RejectionType.Permanent;

        public DownloadSpecDecision IsSatisfiedBy(RemoteEpisode subject, ReleaseDecisionInformation information)
        {
            var cdhEnabled = _configService.EnableCompletedDownloadHandling;

            if (!cdhEnabled)
            {
                _logger.Debug("Skipping already imported check because CDH is disabled");
                return DownloadSpecDecision.Accept();
            }

            _logger.Debug("Performing already imported check on report");
            foreach (var episode in subject.Episodes)
            {
                if (!episode.HasFile)
                {
                    _logger.Debug("Skipping already imported check for episode without file");
                    continue;
                }

                var historyForEpisode = _historyService.FindByEpisodeId(episode.Id);
                var imported = FindImportOfFile(historyForEpisode, episode.EpisodeFileId);

                if (imported == null)
                {
                    continue;
                }

                var release = subject.Release;

                if (release is TorrentInfo torrentInfo &&
                    torrentInfo.InfoHash.IsNotNullOrWhiteSpace() &&
                    torrentInfo.InfoHash.Equals(imported.DownloadId, StringComparison.OrdinalIgnoreCase))
                {
                    _logger.Debug("Has same torrent hash as a grabbed and imported release");
                    return DownloadSpecDecision.Reject(DownloadRejectionReason.AlreadyImportedSameHash, "Has same torrent hash as a grabbed and imported release");
                }

                // Only based on title because a release with the same title on another indexer/released at
                // a different time very likely has the exact same content and we don't need to also try it.
                if (GetReleaseTitles(historyForEpisode, imported).Any(title => release.Title.IsSameReleaseTitle(title)))
                {
                    _logger.Debug("Has same release name as a grabbed and imported release");
                    return DownloadSpecDecision.Reject(DownloadRejectionReason.AlreadyImportedSameName, "Has same release name as a grabbed and imported release");
                }
            }

            return DownloadSpecDecision.Accept();
        }

        // The file on disk, not the last grab: a later grab may have failed, and an equal quality can still score lower once imported
        private static EpisodeHistory FindImportOfFile(List<EpisodeHistory> history, int episodeFileId)
        {
            var fileId = episodeFileId.ToString();

            return history.FirstOrDefault(h => h.EventType == EpisodeHistoryEventType.DownloadFolderImported &&
                                               h.Data?.GetValueOrDefault(EpisodeHistory.FILE_ID) == fileId);
        }

        private static IEnumerable<string> GetReleaseTitles(List<EpisodeHistory> history, EpisodeHistory imported)
        {
            // Without a download ID, grabs can't be tied to this import
            if (imported.DownloadId.IsNullOrWhiteSpace())
            {
                return new[] { imported.SourceTitle };
            }

            return history.Where(h => h.EventType == EpisodeHistoryEventType.Grabbed && h.DownloadId == imported.DownloadId)
                          .Select(h => h.SourceTitle)
                          .Append(imported.SourceTitle);
        }
    }
}
