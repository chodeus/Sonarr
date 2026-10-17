using System.Collections.Generic;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Core.MediaFiles;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;
using NzbDrone.Core.Tv.Events;

namespace NzbDrone.Core.Test.MediaFiles
{
    [TestFixture]
    public class EpisodeFileSeasonServiceFixture : CoreTest<EpisodeFileSeasonService>
    {
        private Series _series;
        private List<EpisodeFile> _episodeFiles;
        private List<EpisodeFile> _updatedFiles;

        [SetUp]
        public void Setup()
        {
            _series = new Series { Id = 1 };
            _episodeFiles = new List<EpisodeFile>
            {
                new EpisodeFile { Id = 10, SeriesId = 1, SeasonNumber = 1, RelativePath = "Season 01/S01E01.mkv" },
                new EpisodeFile { Id = 11, SeriesId = 1, SeasonNumber = 1, RelativePath = "Season 01/S01E02E03.mkv" }
            };
            _updatedFiles = null;

            Mocker.GetMock<IMediaFileService>()
                  .Setup(s => s.GetFilesBySeries(1))
                  .Returns(_episodeFiles);

            Mocker.GetMock<IMediaFileService>()
                  .Setup(s => s.Update(It.IsAny<List<EpisodeFile>>()))
                  .Callback<List<EpisodeFile>>(f => _updatedFiles = f);
        }

        private void GivenUpdated(params Episode[] episodes)
        {
            Subject.Handle(new EpisodeInfoRefreshedEvent(_series, new List<Episode>(), episodes, new List<Episode>()));
        }

        [Test]
        public void should_move_file_to_the_season_of_its_episode()
        {
            GivenUpdated(new Episode { EpisodeFileId = 10, SeasonNumber = 2, EpisodeNumber = 4 });

            _updatedFiles.Should().ContainSingle().Which.Id.Should().Be(10);
            _episodeFiles[0].SeasonNumber.Should().Be(2);
        }

        [Test]
        public void should_use_the_lowest_season_of_a_multi_episode_file()
        {
            GivenUpdated(new Episode { EpisodeFileId = 11, SeasonNumber = 3, EpisodeNumber = 1 },
                         new Episode { EpisodeFileId = 11, SeasonNumber = 2, EpisodeNumber = 9 });

            _updatedFiles.Should().ContainSingle().Which.Id.Should().Be(11);
            _episodeFiles[1].SeasonNumber.Should().Be(2);
        }

        [Test]
        public void should_not_update_files_whose_season_did_not_change()
        {
            GivenUpdated(new Episode { EpisodeFileId = 10, SeasonNumber = 1, EpisodeNumber = 7 });

            Mocker.GetMock<IMediaFileService>()
                  .Verify(s => s.Update(It.IsAny<List<EpisodeFile>>()), Times.Never());
        }

        [Test]
        public void should_not_load_files_when_no_updated_episode_has_a_file()
        {
            GivenUpdated(new Episode { EpisodeFileId = 0, SeasonNumber = 4, EpisodeNumber = 1 });

            Mocker.GetMock<IMediaFileService>()
                  .Verify(s => s.GetFilesBySeries(It.IsAny<int>()), Times.Never());
        }
    }
}
