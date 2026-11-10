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
        private void GivenImportedFile(int fileId)
        {
            var localEpisode = new LocalEpisode
            {
                Series = new Series { Id = 1, Path = "/tv/Series Title" },
                Episodes = new List<Episode> { _episode },
                Path = $"/downloads/{ImportedTitle}.mkv",
                Quality = new QualityModel(Quality.WEBDL1080p),
                Languages = new List<Language> { Language.English }
            };

            var episodeFile = new EpisodeFile
            {
                Id = fileId,
                SeriesId = 1,
                SceneName = ImportedTitle,
                RelativePath = "Season 1/Series Title - S01E01.mkv"
            };

            var downloadItem = new DownloadClientItem { DownloadId = "DOWNLOAD1" };

            Mocker.Resolve<HistoryService>().Handle(new EpisodeImportedEvent(localEpisode, episodeFile, new List<DeletedEpisodeFile>(), true, downloadItem));
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
        public void should_accept_the_release_once_another_file_replaced_it()
        {
            GivenImportedFile(4);

            Subject.IsSatisfiedBy(GivenRelease("series.title.s01e01.1080p.web.h264-group"), new()).Accepted.Should().BeTrue();
        }
    }
}
