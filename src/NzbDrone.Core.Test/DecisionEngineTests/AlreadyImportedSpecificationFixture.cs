using System;
using System.Collections.Generic;
using System.Linq;
using FizzWare.NBuilder;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Core.Configuration;
using NzbDrone.Core.DecisionEngine.Specifications;
using NzbDrone.Core.History;
using NzbDrone.Core.Indexers;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Qualities;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.Test.DecisionEngineTests
{
    [TestFixture]
    public class AlreadyImportedSpecificationFixture : CoreTest<AlreadyImportedSpecification>
    {
        private const int FIRST_EPISODE_ID = 1;
        private const string TITLE = "Series.Title.S01E01.720p.HDTV.x264-Sonarr";

        private Series _series;
        private QualityModel _hdtv720p;
        private QualityModel _hdtv1080p;
        private RemoteEpisode _remoteEpisode;
        private List<EpisodeHistory> _history;

        [SetUp]
        public void Setup()
        {
            var singleEpisodeList = new List<Episode>
                                    {
                                        new Episode
                                        {
                                            Id = FIRST_EPISODE_ID,
                                            SeasonNumber = 12,
                                            EpisodeNumber = 3,
                                            EpisodeFileId = 1
                                        }
                                    };

            _series = Builder<Series>.CreateNew()
                                     .Build();

            _hdtv720p = new QualityModel(Quality.HDTV720p, new Revision(version: 1));
            _hdtv1080p = new QualityModel(Quality.HDTV1080p, new Revision(version: 1));

            _remoteEpisode = new RemoteEpisode
            {
                Series = _series,
                ParsedEpisodeInfo = new ParsedEpisodeInfo { Quality = _hdtv720p },
                Episodes = singleEpisodeList,
                Release = Builder<ReleaseInfo>.CreateNew()
                                              .Build()
            };

            _history = new List<EpisodeHistory>();

            Mocker.GetMock<IConfigService>()
                  .SetupGet(s => s.EnableCompletedDownloadHandling)
                  .Returns(true);

            Mocker.GetMock<IHistoryService>()
                  .Setup(s => s.FindByEpisodeId(It.IsAny<int>()))
                  .Returns(_history);
        }

        private void GivenCdhDisabled()
        {
            Mocker.GetMock<IConfigService>()
                  .SetupGet(s => s.EnableCompletedDownloadHandling)
                  .Returns(false);
        }

        private void GivenHistoryItem(string downloadId, string sourceTitle, QualityModel quality, EpisodeHistoryEventType eventType, int fileId = 1)
        {
            var history = new EpisodeHistory
            {
                DownloadId = downloadId,
                SourceTitle = sourceTitle,
                Quality = quality,
                Date = DateTime.UtcNow,
                EventType = eventType
            };

            if (eventType == EpisodeHistoryEventType.DownloadFolderImported)
            {
                history.Data.Add(EpisodeHistory.FILE_ID, fileId.ToString());
            }

            _history.Add(history);
        }

        private void GivenReleaseTitle(string title)
        {
            _remoteEpisode.Release = Builder<ReleaseInfo>.CreateNew()
                                                         .With(r => r.Title = title)
                                                         .Build();
        }

        [Test]
        public void should_be_accepted_if_CDH_is_disabled()
        {
            GivenCdhDisabled();

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_be_accepted_if_episode_does_not_have_a_file()
        {
            _remoteEpisode.Episodes.First().EpisodeFileId = 0;

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_be_accepted_if_episode_does_not_have_grabbed_event()
        {
            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_be_accepted_if_episode_does_not_have_imported_event()
        {
            GivenHistoryItem(Guid.NewGuid().ToString().ToUpper(), TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_be_accepted_if_the_release_is_not_the_imported_release()
        {
            var downloadId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported);
            GivenReleaseTitle("Series.Title.S01E01.720p.HDTV.x264-Other");

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_be_rejected_if_the_same_release_was_imported_with_the_same_quality()
        {
            var downloadId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported);
            GivenReleaseTitle("series.title.s01e01.720p.hdtv.x264-sonarr.mkv");

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeFalse();
        }

        [Test]
        public void should_be_rejected_if_a_later_grab_of_the_episode_failed()
        {
            var importedId = Guid.NewGuid().ToString().ToUpper();
            var failedId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(failedId, TITLE, _hdtv720p, EpisodeHistoryEventType.DownloadFailed);
            GivenHistoryItem(failedId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(importedId, TITLE, _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported);
            GivenHistoryItem(importedId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenReleaseTitle(TITLE);

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeFalse();
        }

        [Test]
        public void should_be_rejected_if_release_title_matches_the_imported_name_instead_of_the_grabbed_one()
        {
            var downloadId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(downloadId, "Series.Title.S01E01.720p.HDTV.AAC2.0.x264-Sonarr", _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported);
            GivenReleaseTitle(TITLE);

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeFalse();
        }

        [Test]
        public void should_be_accepted_if_the_file_on_disk_came_from_another_release()
        {
            var currentId = Guid.NewGuid().ToString().ToUpper();
            var replacedId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(currentId, "Series.Title.S01E01.720p.HDTV.x264-Other", _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported, fileId: 1);
            GivenHistoryItem(currentId, "Series.Title.S01E01.720p.HDTV.x264-Other", _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(replacedId, TITLE, _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported, fileId: 2);
            GivenHistoryItem(replacedId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenReleaseTitle(TITLE);

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_be_rejected_by_the_last_imported_grab_if_no_import_names_the_file()
        {
            var downloadId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported, fileId: 2);
            GivenReleaseTitle(TITLE);

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeFalse();
        }

        [Test]
        public void should_be_accepted_if_no_import_names_the_file_and_the_last_grab_was_not_imported()
        {
            var importedId = Guid.NewGuid().ToString().ToUpper();
            var lastId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(lastId, "Series.Title.S01E01.720p.HDTV.x264-Other", _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(importedId, TITLE, _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported, fileId: 2);
            GivenHistoryItem(importedId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenReleaseTitle(TITLE);

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_tie_grabs_to_the_import_whatever_the_download_id_case()
        {
            GivenHistoryItem("abcdef0123456789", "Series.Title.S01E01.720p.HDTV.AAC2.0.x264-Sonarr", _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem("ABCDEF0123456789", TITLE, _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported);
            GivenReleaseTitle("Series.Title.S01E01.720p.HDTV.AAC2.0.x264-Sonarr");

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeFalse();
        }

        [Test]
        public void should_be_accepted_if_grabbed_download_id_and_release_torrent_hash_is_unknown()
        {
            var downloadId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(downloadId, TITLE, _hdtv1080p, EpisodeHistoryEventType.DownloadFolderImported);

            _remoteEpisode.Release = Builder<TorrentInfo>.CreateNew()
                                                         .With(t => t.DownloadProtocol = DownloadProtocol.Torrent)
                                                         .With(t => t.InfoHash = null)
                                                         .Build();

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_be_accepted_if_the_import_has_no_download_id_to_tie_it_to_a_grab()
        {
            GivenHistoryItem(null, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(null, "Series.Title.S01E01.720p.HDTV.x264-Other", _hdtv720p, EpisodeHistoryEventType.DownloadFolderImported);
            GivenReleaseTitle(TITLE);

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_be_rejected_if_grabbed_download_id_matches_release_torrent_hash()
        {
            var downloadId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(downloadId, TITLE, _hdtv1080p, EpisodeHistoryEventType.DownloadFolderImported);

            _remoteEpisode.Release = Builder<TorrentInfo>.CreateNew()
                                                         .With(t => t.DownloadProtocol = DownloadProtocol.Torrent)
                                                         .With(t => t.InfoHash = downloadId)
                                                         .Build();

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeFalse();
        }

        [Test]
        public void should_be_rejected_if_release_title_matches_grabbed_event_source_title()
        {
            var downloadId = Guid.NewGuid().ToString().ToUpper();

            GivenHistoryItem(downloadId, TITLE, _hdtv720p, EpisodeHistoryEventType.Grabbed);
            GivenHistoryItem(downloadId, "Series.Title.S01E01.720p.HDTV.x264-Sonarr-Renamed", _hdtv1080p, EpisodeHistoryEventType.DownloadFolderImported);

            _remoteEpisode.Release = Builder<TorrentInfo>.CreateNew()
                                                         .With(t => t.DownloadProtocol = DownloadProtocol.Torrent)
                                                         .With(t => t.InfoHash = null)
                                                         .With(t => t.Title = TITLE)
                                                         .Build();

            Subject.IsSatisfiedBy(_remoteEpisode, new()).Accepted.Should().BeFalse();
        }
    }
}
