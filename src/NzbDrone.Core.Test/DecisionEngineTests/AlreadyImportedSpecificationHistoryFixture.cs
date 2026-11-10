using System;
using System.Collections.Generic;
using FluentAssertions;
using NUnit.Framework;
using NzbDrone.Core.Configuration;
using NzbDrone.Core.DecisionEngine.Specifications;
using NzbDrone.Core.Download;
using NzbDrone.Core.History;
using NzbDrone.Core.Languages;
using NzbDrone.Core.MediaFiles;
using NzbDrone.Core.MediaFiles.Events;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Qualities;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.Test.DecisionEngineTests
{
    [TestFixture]
    public class AlreadyImportedSpecificationHistoryFixture : DbTest<AlreadyImportedSpecification, EpisodeHistory>
    {
        private const string ImportedTitle = "Series.Title.S01E01.1080p.WEB.h264-Group";

        private Episode _episode;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<IHistoryRepository>(Mocker.Resolve<HistoryRepository>());
            Mocker.SetConstant<IHistoryService>(Mocker.Resolve<HistoryService>());

            Mocker.GetMock<IConfigService>()
                  .SetupGet(s => s.EnableCompletedDownloadHandling)
                  .Returns(true);

            _episode = new Episode { Id = 1, EpisodeFileId = 5 };
        }

        // HistoryService writes the key as "FileId"; the database hands it back camel-cased
        private void GivenImportedFile(int fileId, string downloadId = "DOWNLOAD1", string title = ImportedTitle)
        {
            var localEpisode = new LocalEpisode
            {
                Series = new Series { Id = 1, Path = "/tv/Series Title" },
                Episodes = new List<Episode> { _episode },
                Path = $"/downloads/{title}.mkv",
                Quality = new QualityModel(Quality.WEBDL1080p),
                Languages = new List<Language> { Language.English }
            };

            var episodeFile = new EpisodeFile
            {
                Id = fileId,
                SeriesId = 1,
                SceneName = title,
                RelativePath = "Season 1/Series Title - S01E01.mkv"
            };

            var downloadItem = new DownloadClientItem { DownloadId = downloadId };

            Mocker.Resolve<HistoryService>().Handle(new EpisodeImportedEvent(localEpisode, episodeFile, new List<DeletedEpisodeFile>(), true, downloadItem));
        }

        private void GivenHistory(string downloadId, string title, EpisodeHistoryEventType eventType, DateTime date)
        {
            Mocker.Resolve<HistoryRepository>().Insert(new EpisodeHistory
            {
                EpisodeId = _episode.Id,
                SeriesId = 1,
                SourceTitle = title,
                DownloadId = downloadId,
                EventType = eventType,
                Date = date,
                Quality = new QualityModel(Quality.WEBDL1080p),
                Languages = new List<Language> { Language.English }
            });
        }

        private RemoteEpisode GivenRelease(string title)
        {
            return new RemoteEpisode
            {
                Series = new Series { Id = 1 },
                Episodes = new List<Episode> { _episode },
                Release = new ReleaseInfo { Title = title }
            };
        }

        [Test]
        public void should_reject_the_release_the_file_on_disk_was_imported_from()
        {
            GivenImportedFile(5);

            Subject.IsSatisfiedBy(GivenRelease("series.title.s01e01.1080p.web.h264-group"), new()).Accepted.Should().BeFalse();
        }

        [Test]
        public void should_accept_the_release_once_another_import_replaced_the_file()
        {
            GivenImportedFile(4);
            GivenImportedFile(5, "DOWNLOAD2", "Series.Title.S01E01.1080p.WEB-DL.DDP5.1.H.264-Other");

            Subject.IsSatisfiedBy(GivenRelease("series.title.s01e01.1080p.web.h264-group"), new()).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_reject_the_release_after_a_rescan_re_created_the_file_record_and_a_later_grab_failed()
        {
            GivenHistory("DOWNLOAD1", ImportedTitle, EpisodeHistoryEventType.Grabbed, DateTime.UtcNow.AddHours(-2));
            GivenImportedFile(4);
            GivenHistory("DOWNLOAD2", "Series.Title.S01E01.1080p.WEB-DL.DDP5.1.H.264-Other", EpisodeHistoryEventType.Grabbed, DateTime.UtcNow.AddHours(1));
            GivenHistory("DOWNLOAD2", "Series.Title.S01E01.1080p.WEB-DL.DDP5.1.H.264-Other", EpisodeHistoryEventType.DownloadFailed, DateTime.UtcNow.AddHours(2));

            Subject.IsSatisfiedBy(GivenRelease("series.title.s01e01.1080p.web.h264-group"), new()).Accepted.Should().BeFalse();
        }
    }
}
