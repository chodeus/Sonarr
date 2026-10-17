using System;
using System.Collections.Generic;
using System.Linq;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Disk;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.Exceptions;
using NzbDrone.Core.Languages;
using NzbDrone.Core.MediaFiles;
using NzbDrone.Core.MediaFiles.Events;
using NzbDrone.Core.Messaging.Commands;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.MetadataSource;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;
using NzbDrone.Core.Tv.Commands;
using NzbDrone.Test.Common;

namespace NzbDrone.Core.Test.TvTests
{
    [TestFixture]
    public class EpisodeOrderChangeServiceFixture : CoreTest<EpisodeOrderChangeService>
    {
        private Series _series;
        private List<Episode> _episodes;
        private List<EpisodeFile> _files;
        private List<Episode> _remoteEpisodes;
        private bool _titlesInPaths;

        [SetUp]
        public void Setup()
        {
            _titlesInPaths = true;

            _series = new Series
            {
                Id = 1,
                TvdbId = 1000,
                Title = "Show",
                Path = "/tv/Show".AsOsAgnostic(),
                SeasonType = SeasonType.Official,
                SeasonTypes = new List<SeasonType> { new SeasonType { Type = SeasonType.Official }, new SeasonType { Type = "dvd" } },
                Seasons = new List<Season> { new Season { SeasonNumber = 1 } }
            };

            _episodes = new List<Episode>
            {
                new Episode { Id = 1, SeriesId = 1, TvdbId = 101, SeasonNumber = 1, EpisodeNumber = 1, Title = "A", EpisodeFileId = 11 },
                new Episode { Id = 2, SeriesId = 1, TvdbId = 102, SeasonNumber = 1, EpisodeNumber = 2, Title = "B", EpisodeFileId = 12 },
                new Episode { Id = 3, SeriesId = 1, TvdbId = 103, SeasonNumber = 1, EpisodeNumber = 3, Title = "C" }
            };

            _files = new List<EpisodeFile>
            {
                new EpisodeFile { Id = 11, SeriesId = 1, SeasonNumber = 1, RelativePath = "Season 01/S01E01 - A.mkv" },
                new EpisodeFile { Id = 12, SeriesId = 1, SeasonNumber = 1, RelativePath = "Season 01/S01E02 - B.mkv" }
            };

            _remoteEpisodes = new List<Episode>
            {
                new Episode { TvdbId = 102, SeasonNumber = 1, EpisodeNumber = 1, Title = "B" },
                new Episode { TvdbId = 101, SeasonNumber = 1, EpisodeNumber = 2, Title = "A" },
                new Episode { TvdbId = 103, SeasonNumber = 2, EpisodeNumber = 1, Title = "C" },
                new Episode { TvdbId = 104, SeasonNumber = 2, EpisodeNumber = 2, Title = "D" }
            };

            Mocker.GetMock<ISeriesService>()
                  .Setup(s => s.GetSeries(1))
                  .Returns(() => _series.JsonClone());

            Mocker.GetMock<IEpisodeService>()
                  .Setup(s => s.GetEpisodeBySeries(1))
                  .Returns(() => _episodes.JsonClone());

            Mocker.GetMock<IMediaFileService>()
                  .Setup(s => s.GetFilesBySeries(1))
                  .Returns(() => _files.JsonClone());

            Mocker.GetMock<IMediaFileService>()
                  .Setup(s => s.Get(It.IsAny<IEnumerable<int>>()))
                  .Returns<IEnumerable<int>>(ids => _files.Where(f => ids.Contains(f.Id)).ToList().JsonClone());

            Mocker.GetMock<IProvideSeriesInfo>()
                  .Setup(s => s.GetSeriesInfo(1000, It.IsAny<Language>(), It.IsAny<string>()))
                  .Returns(() => Tuple.Create(new Series { Seasons = new List<Season> { new Season { SeasonNumber = 1 }, new Season { SeasonNumber = 2 } } }, _remoteEpisodes.JsonClone()));

            Mocker.GetMock<IRenameEpisodeFileService>()
                  .Setup(s => s.GetRenamePreviews(It.IsAny<Series>(), It.IsAny<List<Episode>>(), It.IsAny<List<EpisodeFile>>()))
                  .Returns<Series, List<Episode>, List<EpisodeFile>>((series, episodes, files) => BuildPreviews(episodes, files));

            Mocker.GetMock<IRenameEpisodeFileService>()
                  .Setup(s => s.GetRenamePreviews(1))
                  .Returns(() => BuildPreviews(_episodes, _files));

            Mocker.GetMock<IRenameEpisodeFileService>()
                  .Setup(s => s.RenameFiles(It.IsAny<List<EpisodeFile>>(), It.IsAny<Series>()))
                  .Returns<List<EpisodeFile>, Series>((files, series) => RenameUnblocked(files));

            Mocker.GetMock<IExecute<RefreshSeriesCommand>>()
                  .Setup(s => s.Execute(It.IsAny<RefreshSeriesCommand>()))
                  .Callback(ApplyRemoteNumbering);
        }

        private List<RenameEpisodeFilePreview> BuildPreviews(List<Episode> episodes, List<EpisodeFile> files)
        {
            return files.Select(f => new { File = f, Episodes = episodes.Where(e => e.EpisodeFileId == f.Id).OrderBy(e => e.SeasonNumber).ThenBy(e => e.EpisodeNumber).ToList() })
                        .Where(x => x.Episodes.Any())
                        .Select(x => new RenameEpisodeFilePreview
                        {
                            SeriesId = 1,
                            SeasonNumber = x.Episodes[0].SeasonNumber,
                            EpisodeNumbers = x.Episodes.Select(e => e.EpisodeNumber).ToList(),
                            EpisodeFileId = x.File.Id,
                            ExistingPath = x.File.RelativePath,
                            NewPath = PathFor(x.Episodes[0])
                        })
                        .Where(p => p.NewPath != p.ExistingPath)
                        .ToList();
        }

        private string PathFor(Episode episode)
        {
            var name = $"S{episode.SeasonNumber:00}E{episode.EpisodeNumber:00}" + (_titlesInPaths ? $" - {episode.Title}" : string.Empty);

            return $"Season {episode.SeasonNumber:00}/{name}.mkv";
        }

        private List<RenamedEpisodeFile> RenameUnblocked(List<EpisodeFile> files)
        {
            var renamed = new List<RenamedEpisodeFile>();

            foreach (var preview in BuildPreviews(_episodes, _files).Where(p => files.Any(f => f.Id == p.EpisodeFileId)))
            {
                if (_files.Any(f => f.RelativePath == preview.NewPath))
                {
                    continue;
                }

                _files.Single(f => f.Id == preview.EpisodeFileId).RelativePath = preview.NewPath;
                renamed.Add(new RenamedEpisodeFile { PreviousRelativePath = preview.ExistingPath });
            }

            return renamed;
        }

        private void ApplyRemoteNumbering()
        {
            foreach (var episode in _episodes)
            {
                var remote = _remoteEpisodes.SingleOrDefault(r => r.TvdbId == episode.TvdbId);

                if (remote != null)
                {
                    episode.SeasonNumber = remote.SeasonNumber;
                    episode.EpisodeNumber = remote.EpisodeNumber;
                }
            }
        }

        private void GivenPathsWithoutTitles()
        {
            _titlesInPaths = false;
            _files[0].RelativePath = "Season 01/S01E01.mkv";
            _files[1].RelativePath = "Season 01/S01E02.mkv";
        }

        private EpisodeOrderPreviewFile PreviewFile(EpisodeOrderPreview preview, int episodeFileId)
        {
            return preview.Files.Single(f => f.EpisodeFileId == episodeFileId);
        }

        private void Execute(bool renameFiles = true)
        {
            Subject.Execute(new ChangeEpisodeOrderCommand { SeriesId = 1, SeasonType = "dvd", RenameFiles = renameFiles });
        }

        [Test]
        public void should_preview_new_numbers_and_paths_for_files_whose_episodes_move()
        {
            var preview = Subject.Preview(1, "dvd");

            var file = PreviewFile(preview, 11);
            file.Status.Should().Be(EpisodeOrderPreviewFileStatus.Renamed);
            file.ExistingEpisodes.Should().ContainSingle(e => e.SeasonNumber == 1 && e.EpisodeNumber == 1);
            file.NewEpisodes.Should().ContainSingle(e => e.SeasonNumber == 1 && e.EpisodeNumber == 2);
            file.NewPath.Should().Be("Season 01/S01E02 - A.mkv");

            PreviewFile(preview, 12).NewPath.Should().Be("Season 01/S01E01 - B.mkv");
            preview.RefreshRequired.Should().BeFalse();
            preview.HasAbsoluteEpisodeNumbers.Should().BeFalse();
            preview.EpisodesAdded.Should().Be(1);
            preview.EpisodesRemoved.Should().Be(0);
        }

        [Test]
        public void should_preview_unchanged_file_when_its_path_stays()
        {
            _remoteEpisodes = new List<Episode>
            {
                new Episode { TvdbId = 101, SeasonNumber = 1, EpisodeNumber = 1, Title = "A" },
                new Episode { TvdbId = 102, SeasonNumber = 1, EpisodeNumber = 2, Title = "B" }
            };

            var preview = Subject.Preview(1, "dvd");

            preview.Files.Should().OnlyContain(f => f.Status == EpisodeOrderPreviewFileStatus.Unchanged && f.NewPath == f.ExistingPath);
            preview.EpisodesRemoved.Should().Be(1);
        }

        [Test]
        public void should_report_absolute_numbers_when_the_new_order_has_them()
        {
            _remoteEpisodes[0].AbsoluteEpisodeNumber = 1;

            Subject.Preview(1, "dvd").HasAbsoluteEpisodeNumbers.Should().BeTrue();
        }

        [Test]
        public void should_not_save_anything_while_previewing()
        {
            Subject.Preview(1, "dvd");

            Mocker.GetMock<ISeriesService>().Verify(s => s.UpdateSeries(It.IsAny<Series>(), It.IsAny<bool>(), It.IsAny<bool>()), Times.Never());
            Mocker.GetMock<IEpisodeService>().Verify(s => s.UpdateEpisodes(It.IsAny<List<Episode>>()), Times.Never());
            Mocker.GetMock<IEpisodeService>().Verify(s => s.UpdateMany(It.IsAny<List<Episode>>()), Times.Never());
            Mocker.GetMock<IMediaFileService>().Verify(s => s.Update(It.IsAny<List<EpisodeFile>>()), Times.Never());
            Mocker.GetMock<IExecute<RefreshSeriesCommand>>().Verify(s => s.Execute(It.IsAny<RefreshSeriesCommand>()), Times.Never());
            _episodes[0].EpisodeNumber.Should().Be(1);
        }

        [TestCase("regional")]
        [TestCase("")]
        [TestCase(null)]
        public void should_reject_an_episode_order_the_series_does_not_have(string seasonType)
        {
            Assert.Throws<NzbDroneClientException>(() => Subject.Preview(1, seasonType));

            Mocker.GetMock<IProvideSeriesInfo>().Verify(s => s.GetSeriesInfo(It.IsAny<int>(), It.IsAny<Language>(), It.IsAny<string>()), Times.Never());
        }

        [Test]
        public void should_mark_file_unmatched_when_its_episode_is_not_in_the_new_order()
        {
            _remoteEpisodes = new List<Episode>
            {
                new Episode { TvdbId = 105, SeasonNumber = 1, EpisodeNumber = 1, Title = "E" },
                new Episode { TvdbId = 102, SeasonNumber = 1, EpisodeNumber = 2, Title = "B" }
            };

            var preview = Subject.Preview(1, "dvd");

            var file = PreviewFile(preview, 11);
            file.Status.Should().Be(EpisodeOrderPreviewFileStatus.Unmatched);
            file.NewPath.Should().BeNull();
            file.NewEpisodes.Should().BeEmpty();
            PreviewFile(preview, 12).Status.Should().Be(EpisodeOrderPreviewFileStatus.Unchanged);
        }

        [Test]
        public void should_require_refresh_when_an_episode_with_a_file_has_no_tvdb_id()
        {
            _episodes[0].TvdbId = 0;

            var preview = Subject.Preview(1, "dvd");

            preview.RefreshRequired.Should().BeTrue();
            PreviewFile(preview, 11).Status.Should().Be(EpisodeOrderPreviewFileStatus.Unmatched);
        }

        [Test]
        public void should_mark_conflict_when_two_files_get_the_same_destination()
        {
            Mocker.GetMock<IRenameEpisodeFileService>()
                  .Setup(s => s.GetRenamePreviews(It.IsAny<Series>(), It.IsAny<List<Episode>>(), It.IsAny<List<EpisodeFile>>()))
                  .Returns<Series, List<Episode>, List<EpisodeFile>>((series, episodes, files) => files.Select(f => new RenameEpisodeFilePreview { EpisodeFileId = f.Id, ExistingPath = f.RelativePath, NewPath = "Season 01/Same.mkv" }).ToList());

            var preview = Subject.Preview(1, "dvd");

            preview.Files.Should().OnlyContain(f => f.Status == EpisodeOrderPreviewFileStatus.Conflict);
        }

        [Test]
        public void should_mark_conflict_when_an_untracked_file_holds_the_destination()
        {
            Mocker.GetMock<IDiskProvider>()
                  .Setup(s => s.FileExists(It.Is<string>(p => p.Contains("S01E02 - A"))))
                  .Returns(true);

            var preview = Subject.Preview(1, "dvd");

            PreviewFile(preview, 11).Status.Should().Be(EpisodeOrderPreviewFileStatus.Conflict);
            PreviewFile(preview, 12).Status.Should().Be(EpisodeOrderPreviewFileStatus.Renamed);
        }

        [Test]
        public void should_mark_conflict_when_files_swap_destinations()
        {
            GivenPathsWithoutTitles();

            var preview = Subject.Preview(1, "dvd");

            preview.Files.Should().OnlyContain(f => f.Status == EpisodeOrderPreviewFileStatus.Conflict);
        }

        [Test]
        public void should_not_mark_conflict_when_a_file_moves_into_a_destination_another_file_leaves()
        {
            GivenPathsWithoutTitles();
            _remoteEpisodes[0].SeasonNumber = 2;
            _remoteEpisodes[0].EpisodeNumber = 5;

            var preview = Subject.Preview(1, "dvd");

            PreviewFile(preview, 11).NewPath.Should().Be("Season 01/S01E02.mkv");
            preview.Files.Should().OnlyContain(f => f.Status == EpisodeOrderPreviewFileStatus.Renamed);

            Mocker.GetMock<IDiskProvider>().Verify(s => s.FileExists(It.Is<string>(p => p.Contains("S01E02"))), Times.Never());
        }

        [Test]
        public void should_mark_conflict_when_a_file_waits_on_a_destination_held_by_a_conflict()
        {
            GivenPathsWithoutTitles();
            _remoteEpisodes[0].SeasonNumber = 2;
            _remoteEpisodes[0].EpisodeNumber = 5;

            Mocker.GetMock<IDiskProvider>()
                  .Setup(s => s.FileExists(It.Is<string>(p => p.Contains("S02E05"))))
                  .Returns(true);

            var preview = Subject.Preview(1, "dvd");

            PreviewFile(preview, 12).Status.Should().Be(EpisodeOrderPreviewFileStatus.Conflict);
            PreviewFile(preview, 11).Status.Should().Be(EpisodeOrderPreviewFileStatus.Conflict);
        }

        [Test]
        public void should_refuse_and_keep_the_order_when_a_file_is_unmatched()
        {
            _remoteEpisodes.RemoveAll(e => e.TvdbId == 101);

            Assert.Throws<CommandFailedException>(() => Execute());

            Mocker.GetMock<ISeriesService>().Verify(s => s.UpdateSeries(It.IsAny<Series>(), It.IsAny<bool>(), It.IsAny<bool>()), Times.Never());
            Mocker.GetMock<IExecute<RefreshSeriesCommand>>().Verify(s => s.Execute(It.IsAny<RefreshSeriesCommand>()), Times.Never());
        }

        [Test]
        public void should_refuse_and_keep_the_order_when_a_refresh_is_required()
        {
            _episodes[1].TvdbId = 0;

            Assert.Throws<CommandFailedException>(() => Execute()).Message.Should().Contain("Refresh");

            Mocker.GetMock<ISeriesService>().Verify(s => s.UpdateSeries(It.IsAny<Series>(), It.IsAny<bool>(), It.IsAny<bool>()), Times.Never());
        }

        [Test]
        public void should_save_the_order_refresh_and_rename_the_files()
        {
            Execute();

            Mocker.GetMock<ISeriesService>().Verify(s => s.UpdateSeries(It.Is<Series>(x => x.SeasonType == "dvd"), false, false), Times.Once());
            Mocker.GetMock<IExecute<RefreshSeriesCommand>>().Verify(s => s.Execute(It.Is<RefreshSeriesCommand>(c => c.SeriesIds.SequenceEqual(new[] { 1 }) && c.Trigger == CommandTrigger.Manual)), Times.Once());
            Mocker.GetMock<IEventAggregator>().Verify(s => s.PublishEvent(It.IsAny<RenameCompletedEvent>()), Times.Once());

            _files.Single(f => f.Id == 11).RelativePath.Should().Be("Season 01/S01E02 - A.mkv");
            _files.Single(f => f.Id == 12).RelativePath.Should().Be("Season 01/S01E01 - B.mkv");
            ExceptionVerification.ExpectedWarns(0);
        }

        [Test]
        public void should_not_rename_when_the_new_order_did_not_apply()
        {
            Mocker.GetMock<IExecute<RefreshSeriesCommand>>()
                  .Setup(s => s.Execute(It.IsAny<RefreshSeriesCommand>()));

            Assert.Throws<CommandFailedException>(() => Execute());

            Mocker.GetMock<IRenameEpisodeFileService>().Verify(s => s.RenameFiles(It.IsAny<List<EpisodeFile>>(), It.IsAny<Series>()), Times.Never());
        }

        [Test]
        public void should_not_rename_when_rename_files_is_off()
        {
            Execute(renameFiles: false);

            Mocker.GetMock<IExecute<RefreshSeriesCommand>>().Verify(s => s.Execute(It.IsAny<RefreshSeriesCommand>()), Times.Once());
            Mocker.GetMock<IRenameEpisodeFileService>().Verify(s => s.RenameFiles(It.IsAny<List<EpisodeFile>>(), It.IsAny<Series>()), Times.Never());
            _files[0].RelativePath.Should().Be("Season 01/S01E01 - A.mkv");
        }

        [Test]
        public void should_rename_a_file_once_another_file_leaves_its_destination()
        {
            GivenPathsWithoutTitles();
            _remoteEpisodes[0].SeasonNumber = 2;
            _remoteEpisodes[0].EpisodeNumber = 5;

            Execute();

            Mocker.GetMock<IRenameEpisodeFileService>().Verify(s => s.RenameFiles(It.IsAny<List<EpisodeFile>>(), It.IsAny<Series>()), Times.Exactly(2));
            _files.Single(f => f.Id == 11).RelativePath.Should().Be("Season 01/S01E02.mkv");
            _files.Single(f => f.Id == 12).RelativePath.Should().Be("Season 02/S02E05.mkv");
            ExceptionVerification.ExpectedWarns(0);
        }

        [Test]
        public void should_stop_renaming_when_files_stay_pending()
        {
            GivenPathsWithoutTitles();

            Execute();

            Mocker.GetMock<IRenameEpisodeFileService>().Verify(s => s.RenameFiles(It.IsAny<List<EpisodeFile>>(), It.IsAny<Series>()), Times.Once());
            ExceptionVerification.ExpectedWarns(1);
        }
    }
}
