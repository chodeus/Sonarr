using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using NLog;
using NzbDrone.Common.Disk;
using NzbDrone.Core.Configuration;
using NzbDrone.Core.Download;
using NzbDrone.Core.MediaFiles.EpisodeImport.Aggregation.Aggregators;
using NzbDrone.Core.MediaFiles.MediaInfo;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Parser.TitleMatching;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.MediaFiles.EpisodeImport.Aggregation
{
    public interface IAggregationService
    {
        LocalEpisode Augment(LocalEpisode localEpisode, DownloadClientItem downloadClientItem);
    }

    public class AggregationService : IAggregationService
    {
        private readonly IEnumerable<IAggregateLocalEpisode> _augmenters;
        private readonly IDiskProvider _diskProvider;
        private readonly IVideoFileInfoReader _videoFileInfoReader;
        private readonly IConfigService _configService;
        private readonly IEpisodeTitleMatcher _episodeTitleMatcher;
        private readonly Logger _logger;

        public AggregationService(IEnumerable<IAggregateLocalEpisode> augmenters,
                                 IDiskProvider diskProvider,
                                 IVideoFileInfoReader videoFileInfoReader,
                                 IConfigService configService,
                                 IEpisodeTitleMatcher episodeTitleMatcher,
                                 Logger logger)
        {
            _augmenters = augmenters.OrderBy(a => a.Order).ToList();
            _diskProvider = diskProvider;
            _videoFileInfoReader = videoFileInfoReader;
            _configService = configService;
            _episodeTitleMatcher = episodeTitleMatcher;
            _logger = logger;
        }

        public LocalEpisode Augment(LocalEpisode localEpisode, DownloadClientItem downloadClientItem)
        {
            var isMediaFile = MediaFileExtensions.Extensions.Contains(Path.GetExtension(localEpisode.Path));

            if (localEpisode.DownloadClientEpisodeInfo == null &&
                localEpisode.FolderEpisodeInfo == null &&
                localEpisode.FileEpisodeInfo == null)
            {
                if (isMediaFile)
                {
                    throw new AugmentingFailedException("Unable to parse episode info from path: {0}", localEpisode.Path);
                }
            }

            localEpisode.Size = _diskProvider.GetFileSize(localEpisode.Path);
            localEpisode.SceneName = localEpisode.SceneSource ? SceneNameCalculator.GetSceneName(localEpisode) : null;

            // After the scene name, which reads FullSeason; the aggregators and import checks then all see the event's numbers and languages
            localEpisode.FileEpisodeInfo = ParseEventTitle(localEpisode.FileEpisodeInfo, localEpisode.Series);
            localEpisode.FolderEpisodeInfo = ParseEventTitle(localEpisode.FolderEpisodeInfo, localEpisode.Series);
            localEpisode.DownloadClientEpisodeInfo = ParseEventTitle(localEpisode.DownloadClientEpisodeInfo, localEpisode.Series);

            if (isMediaFile && (!localEpisode.ExistingFile || _configService.EnableMediaInfo))
            {
                localEpisode.MediaInfo = _videoFileInfoReader.GetMediaInfo(localEpisode.Path);
            }

            foreach (var augmenter in _augmenters)
            {
                try
                {
                    augmenter.Aggregate(localEpisode, downloadClientItem);
                }
                catch (Exception ex)
                {
                    var message = $"Unable to augment information for file: '{localEpisode.Path}'. Series: {localEpisode.Series} Error: {ex.Message}";

                    _logger.Warn(ex, message);
                }
            }

            return localEpisode;
        }

        private ParsedEpisodeInfo ParseEventTitle(ParsedEpisodeInfo parsedEpisodeInfo, Series series)
        {
            return _episodeTitleMatcher.ParseEventTitle(parsedEpisodeInfo, series) ?? parsedEpisodeInfo;
        }
    }
}
