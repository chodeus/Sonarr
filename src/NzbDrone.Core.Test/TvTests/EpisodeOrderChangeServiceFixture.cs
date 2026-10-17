using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Disk;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.CustomFormats;
using NzbDrone.Core.DataAugmentation.Scene;
using NzbDrone.Core.Exceptions;
using NzbDrone.Core.Languages;
using NzbDrone.Core.MediaFiles;
using NzbDrone.Core.MediaFiles.Events;
using NzbDrone.Core.Messaging.Commands;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.MetadataSource;
using NzbDrone.Core.Organizer;
using NzbDrone.Core.Qualities;
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

            Mocker.GetMock<ISceneMappingService>()
                  .Setup(s => s.FindByTvdbId(It.IsAny<int>()))
                  .Returns(new List<SceneMapping>());
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

                var file = _files.Single(f => f.Id == preview.EpisodeFileId);
                file.RelativePath = preview.NewPath;
                renamed.Add(new RenamedEpisodeFile { EpisodeFile = file, PreviousRelativePath = preview.ExistingPath });
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

        private Series SeriesInfo()
        {
            return new Series
            {
                TvdbId = 1000,
                Title = "Show",
                Seasons = new List<Season> { new Season { SeasonNumber = 1 }, new Season { SeasonNumber = 2 } },
                SeasonTypes = _series.SeasonTypes
            };
        }

        private void GivenPersistedSeries()
        {
            Mocker.GetMock<ISeriesService>()
                  .Setup(s => s.UpdateSeries(It.IsAny<Series>(), It.IsAny<bool>(), It.IsAny<bool>()))
                  .Returns<Series, bool, bool>((series, updateEpisodes, publish) =>
                  {
                      _series.SeasonType = series.SeasonType;
                      return series;
                  });
        }

        private void GivenRealNaming()
        {
            var naming = NamingConfig.Default;
            naming.RenameEpisodes = true;
            naming.StandardEpisodeFormat = "{Series Title} - S{season:00}E{episode:00}";
            naming.MultiEpisodeStyle = MultiEpisodeStyle.PrefixedRange;
            naming.SeasonFolderFormat = "Season {season}";

            Mocker.GetMock<INamingConfigService>().Setup(c => c.GetConfig()).Returns(naming);
            Mocker.GetMock<IQualityDefinitionService>()
                  .Setup(v => v.Get(It.IsAny<Quality>()))
                  .Returns<Quality>(v => Quality.DefaultQualityDefinitions.First(c => c.Quality == v));
            Mocker.GetMock<ICustomFormatService>().Setup(v => v.All()).Returns(new List<CustomFormat>());

            Mocker.SetConstant<IBuildFileNames>(Mocker.Resolve<FileNameBuilder>());
            Mocker.SetConstant<IRenameEpisodeFileService>(Mocker.Resolve<RenameEpisodeFileService>());

            _series.SeasonFolder = true;
        }

        private void GivenCombinedFile()
        {
            _episodes[1].EpisodeFileId = 11;
            _files.RemoveAll(f => f.Id == 12);
            _files[0].RelativePath = "Season 1/Show - S01E01-E02.mkv";
            _files[0].Quality = new QualityModel(Quality.HDTV720p);
        }

        [Test]
        public void should_refuse_a_combined_file_whose_episodes_become_gapped()
        {
            GivenRealNaming();
            GivenCombinedFile();
            _remoteEpisodes = new List<Episode>
            {
                new Episode { TvdbId = 101, SeasonNumber = 1, EpisodeNumber = 1, Title = "A" },
                new Episode { TvdbId = 103, SeasonNumber = 1, EpisodeNumber = 2, Title = "C" },
                new Episode { TvdbId = 102, SeasonNumber = 1, EpisodeNumber = 3, Title = "B" }
            };

            var preview = Subject.Preview(1, "dvd");

            PreviewFile(preview, 11).Status.Should().Be(EpisodeOrderPreviewFileStatus.Unnameable);
            preview.CanChange.Should().BeFalse();
        }

        [Test]
        public void should_refuse_a_combined_file_whose_episodes_split_across_seasons()
        {
            GivenRealNaming();
            GivenCombinedFile();
            _remoteEpisodes = new List<Episode>
            {
                new Episode { TvdbId = 101, SeasonNumber = 1, EpisodeNumber = 1, Title = "A" },
                new Episode { TvdbId = 102, SeasonNumber = 2, EpisodeNumber = 3, Title = "B" },
                new Episode { TvdbId = 103, SeasonNumber = 1, EpisodeNumber = 2, Title = "C" }
            };

            var preview = Subject.Preview(1, "dvd");

            PreviewFile(preview, 11).Status.Should().Be(EpisodeOrderPreviewFileStatus.Unnameable);
            preview.CanChange.Should().BeFalse();
        }

        [Test]
        public void should_refuse_a_combined_file_whose_episode_numbers_stay_consecutive_across_seasons()
        {
            GivenRealNaming();
            GivenCombinedFile();
            _remoteEpisodes = new List<Episode>
            {
                new Episode { TvdbId = 101, SeasonNumber = 1, EpisodeNumber = 5, Title = "A" },
                new Episode { TvdbId = 102, SeasonNumber = 2, EpisodeNumber = 6, Title = "B" },
                new Episode { TvdbId = 103, SeasonNumber = 1, EpisodeNumber = 1, Title = "C" }
            };

            var preview = Subject.Preview(1, "dvd");

            PreviewFile(preview, 11).Status.Should().Be(EpisodeOrderPreviewFileStatus.Unnameable);
        }

        [Test]
        public void should_rename_a_combined_file_whose_episodes_stay_consecutive()
        {
            GivenRealNaming();
            GivenCombinedFile();
            _remoteEpisodes = new List<Episode>
            {
                new Episode { TvdbId = 101, SeasonNumber = 1, EpisodeNumber = 5, Title = "A" },
                new Episode { TvdbId = 102, SeasonNumber = 1, EpisodeNumber = 6, Title = "B" },
                new Episode { TvdbId = 103, SeasonNumber = 1, EpisodeNumber = 1, Title = "C" }
            };

            var preview = Subject.Preview(1, "dvd");

            var file = PreviewFile(preview, 11);
            file.Status.Should().Be(EpisodeOrderPreviewFileStatus.Renamed);
            file.NewPath.Should().Be("Season 1/Show - S01E05-E06.mkv");
            preview.CanChange.Should().BeTrue();
        }

        [Test]
        public void should_refuse_to_change_the_order_when_a_combined_file_cannot_be_named()
        {
            GivenRealNaming();
            GivenCombinedFile();
            _remoteEpisodes[0].EpisodeNumber = 4;

            Assert.Throws<CommandFailedException>(() => Execute());

            Mocker.GetMock<ISeriesService>().Verify(s => s.UpdateSeries(It.IsAny<Series>(), It.IsAny<bool>(), It.IsAny<bool>()), Times.Never());
        }

        [Test]
        public void should_restore_the_previous_order_when_the_refresh_fails_after_the_preview()
        {
            GivenPersistedSeries();
            Mocker.SetConstant<IExecute<RefreshSeriesCommand>>(Mocker.Resolve<RefreshSeriesService>());

            var airedEpisodes = _episodes.Select(e => new Episode { TvdbId = e.TvdbId, SeasonNumber = e.SeasonNumber, EpisodeNumber = e.EpisodeNumber, Title = e.Title }).ToList();
            var dvdFetches = 0;

            Mocker.GetMock<IProvideSeriesInfo>()
                  .Setup(s => s.GetSeriesInfo(1000, It.IsAny<Language>(), "dvd"))
                  .Returns(() => ++dvdFetches == 1 ? Tuple.Create(SeriesInfo(), _remoteEpisodes.JsonClone()) : throw new IOException("metadata unavailable after preview"));

            Mocker.GetMock<IProvideSeriesInfo>()
                  .Setup(s => s.GetSeriesInfo(1000, It.IsAny<Language>(), SeasonType.Official))
                  .Returns(() => Tuple.Create(SeriesInfo(), airedEpisodes.JsonClone()));

            Assert.Throws<CommandFailedException>(() => Execute());

            dvdFetches.Should().Be(2);
            _series.SeasonType.Should().Be(SeasonType.Official);
            Mocker.GetMock<IRenameEpisodeFileService>().Verify(s => s.RenameFiles(It.IsAny<List<EpisodeFile>>(), It.IsAny<Series>()), Times.Never());
            ExceptionVerification.ExpectedErrors(1);
            ExceptionVerification.IgnoreWarns();
        }

        [Test]
        public void should_restore_the_previous_order_when_the_new_order_did_not_apply()
        {
            GivenPersistedSeries();
            Mocker.GetMock<IExecute<RefreshSeriesCommand>>()
                  .Setup(s => s.Execute(It.IsAny<RefreshSeriesCommand>()));

            Assert.Throws<CommandFailedException>(() => Execute());

            _series.SeasonType.Should().Be(SeasonType.Official);
            Mocker.GetMock<IExecute<RefreshSeriesCommand>>().Verify(s => s.Execute(It.IsAny<RefreshSeriesCommand>()), Times.Exactly(2));
        }

        [Test]
        public void should_refuse_to_leave_aired_order_while_xem_renumbers_episodes()
        {
            _episodes[0].SceneSeasonNumber = 2;
            _episodes[0].SceneEpisodeNumber = 1;

            var preview = Subject.Preview(1, "dvd");

            preview.SceneNumberedEpisodes.Should().Be(1);
            preview.CanChange.Should().BeFalse();
        }

        [Test]
        public void should_refuse_to_leave_aired_order_while_scene_titles_carry_seasons()
        {
            Mocker.GetMock<ISceneMappingService>()
                  .Setup(s => s.FindByTvdbId(1000))
                  .Returns(new List<SceneMapping> { new SceneMapping { Title = "Show Season One", SceneSeasonNumber = 1 } });

            var preview = Subject.Preview(1, "dvd");

            preview.SceneSeasonTitles.Should().Be(1);
            preview.CanChange.Should().BeFalse();
        }

        [Test]
        public void should_not_count_xem_numbering_that_matches_aired_order()
        {
            _episodes[0].SceneSeasonNumber = 1;
            _episodes[0].SceneEpisodeNumber = 1;
            Mocker.GetMock<ISceneMappingService>()
                  .Setup(s => s.FindByTvdbId(1000))
                  .Returns(new List<SceneMapping> { new SceneMapping { Title = "Show Alternate" } });

            var preview = Subject.Preview(1, "dvd");

            preview.SceneNumberedEpisodes.Should().Be(0);
            preview.SceneSeasonTitles.Should().Be(0);
            preview.CanChange.Should().BeTrue();
        }

        [Test]
        public void should_allow_returning_to_aired_order_while_xem_renumbers_episodes()
        {
            _series.SeasonType = "dvd";
            _episodes[0].SceneSeasonNumber = 2;
            _episodes[0].SceneEpisodeNumber = 1;

            var preview = Subject.Preview(1, SeasonType.Official);

            preview.SceneNumberedEpisodes.Should().Be(0);
            preview.CanChange.Should().BeTrue();
        }

        [Test]
        public void should_refuse_to_change_the_order_while_xem_renumbers_episodes()
        {
            _episodes[0].SceneSeasonNumber = 2;
            _episodes[0].SceneEpisodeNumber = 1;

            Assert.Throws<CommandFailedException>(() => Execute()).Message.Should().Contain("XEM");

            Mocker.GetMock<ISeriesService>().Verify(s => s.UpdateSeries(It.IsAny<Series>(), It.IsAny<bool>(), It.IsAny<bool>()), Times.Never());
        }

        [Test]
        public void should_not_rename_files_that_share_a_destination()
        {
            Mocker.GetMock<IRenameEpisodeFileService>()
                  .Setup(s => s.GetRenamePreviews(1))
                  .Returns(() => _files.Select(f => new RenameEpisodeFilePreview { EpisodeFileId = f.Id, ExistingPath = f.RelativePath, NewPath = "Season 01/Same.mkv" }).ToList());

            Execute();

            Mocker.GetMock<IRenameEpisodeFileService>().Verify(s => s.RenameFiles(It.IsAny<List<EpisodeFile>>(), It.IsAny<Series>()), Times.Never());
            ExceptionVerification.ExpectedWarns(1);
        }

        [Test]
        public void should_rename_each_file_once_even_when_it_stays_pending()
        {
            var renameCalls = 0;

            Mocker.GetMock<IRenameEpisodeFileService>()
                  .Setup(s => s.GetRenamePreviews(1))
                  .Returns(() => renameCalls < 3
                      ? new List<RenameEpisodeFilePreview> { new RenameEpisodeFilePreview { EpisodeFileId = 11, ExistingPath = _files[0].RelativePath, NewPath = "Season 01/Elsewhere.mkv" } }
                      : new List<RenameEpisodeFilePreview>());

            Mocker.GetMock<IRenameEpisodeFileService>()
                  .Setup(s => s.RenameFiles(It.IsAny<List<EpisodeFile>>(), It.IsAny<Series>()))
                  .Returns<List<EpisodeFile>, Series>((files, series) =>
                  {
                      renameCalls++;
                      return files.Select(f => new RenamedEpisodeFile { EpisodeFile = f }).ToList();
                  });

            Execute();

            renameCalls.Should().Be(1);
        }
    }
}
