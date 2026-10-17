using System.Collections.Generic;
using System.Linq;
using NLog;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.Tv;
using NzbDrone.Core.Tv.Events;

namespace NzbDrone.Core.MediaFiles
{
    public class EpisodeFileSeasonService : IHandle<EpisodeInfoRefreshedEvent>
    {
        private readonly IMediaFileService _mediaFileService;
        private readonly Logger _logger;

        public EpisodeFileSeasonService(IMediaFileService mediaFileService, Logger logger)
        {
            _mediaFileService = mediaFileService;
            _logger = logger;
        }

        public void Handle(EpisodeInfoRefreshedEvent message)
        {
            var seasonNumbers = message.Updated.Where(e => e.EpisodeFileId > 0)
                                               .GroupBy(e => e.EpisodeFileId)
                                               .ToDictionary(g => g.Key, GetSeasonNumber);

            if (seasonNumbers.Empty())
            {
                return;
            }

            var movedFiles = _mediaFileService.GetFilesBySeries(message.Series.Id)
                                              .Where(f => seasonNumbers.TryGetValue(f.Id, out var seasonNumber) && f.SeasonNumber != seasonNumber)
                                              .ToList();

            foreach (var episodeFile in movedFiles)
            {
                _logger.Debug("Episode file {0} moved from season {1} to season {2}", episodeFile.RelativePath, episodeFile.SeasonNumber, seasonNumbers[episodeFile.Id]);
                episodeFile.SeasonNumber = seasonNumbers[episodeFile.Id];
            }

            if (movedFiles.Any())
            {
                _mediaFileService.Update(movedFiles);
            }
        }

        private static int GetSeasonNumber(IEnumerable<Episode> episodesInFile)
        {
            return episodesInFile.Min(e => e.SeasonNumber);
        }
    }
}
