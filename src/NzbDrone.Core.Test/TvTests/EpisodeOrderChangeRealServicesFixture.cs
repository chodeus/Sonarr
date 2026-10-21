using System;
using System.Collections.Generic;
using System.Linq;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Disk;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.CustomFormats;
using NzbDrone.Core.DataAugmentation.Scene;
using NzbDrone.Core.Languages;
using NzbDrone.Core.MediaFiles;
using NzbDrone.Core.MediaFiles.Events;
using NzbDrone.Core.Messaging.Commands;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.MetadataSource;
using NzbDrone.Core.Organizer;
using NzbDrone.Core.Qualities;
using NzbDrone.Core.RootFolders;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;
using NzbDrone.Core.Tv.Commands;
using NzbDrone.Test.Common;

namespace NzbDrone.Core.Test.TvTests
{
    // The simulated disk keys paths with "/"
    [TestFixture]
    [Platform(Exclude = "Win")]
    public class EpisodeOrderChangeRealServicesFixture : DbTest<EpisodeOrderChangeService, Episode>
    {
        private const string SeriesPath = "/tv/Show";

        private Series _series;
        private List<EpisodeFile> _files;
        private List<Episode> _aired;
        private List<Episode> _dvd;
        private HashSet<string> _disk;
        private List<SceneMapping> _storedSceneMappings;

        [SetUp]
        public void Setup()
        {
            _series = new Series
            {
                Id = 1,
                TvdbId = 1000,
                Title = "Show",
                Path = SeriesPath,
                SeasonFolder = true,
                Monitored = true,
                SeasonType = SeasonType.Official,
                SeasonTypes = new List<SeasonType> { new SeasonType { Type = SeasonType.Official }, new SeasonType { Type = "dvd" } },
                Seasons = new List<Season> { new Season { SeasonNumber = 1 }, new Season { SeasonNumber = 2 } }
            };

            _disk = new HashSet<string>();
            _storedSceneMappings = new List<SceneMapping>();

            GivenNaming();
            GivenDatabase();
            GivenFileSystem();

            Mocker.GetMock<IProvideSeriesInfo>()
                  .Setup(s => s.GetSeriesInfo(1000, It.IsAny<Language>(), "dvd"))
                  .Returns(() => Tuple.Create(SeriesInfo(), _dvd.JsonClone()));

            Mocker.GetMock<IProvideSeriesInfo>()
                  .Setup(s => s.GetSeriesInfo(1000, It.IsAny<Language>(), SeasonType.Official))
                  .Returns(() => Tuple.Create(SeriesInfo(), _aired.JsonClone()));

            Mocker.GetMock<IRootFolderService>().Setup(s => s.GetBestRootFolderPath(It.IsAny<string>())).Returns("/tv");
            Mocker.GetMock<ISceneMappingRepository>().Setup(s => s.All()).Returns(() => _storedSceneMappings);

            // Real chain below the command: refresh, episode refresh, disk scan, table cleanup, rename, move, transfer
            Mocker.SetConstant<ISceneMappingService>(Mocker.Resolve<SceneMappingService>());
            Mocker.SetConstant<IBuildFileNames>(Mocker.Resolve<FileNameBuilder>());
            Mocker.SetConstant<IDiskTransferService>(Mocker.Resolve<DiskTransferService>());
            Mocker.SetConstant<IMoveEpisodeFiles>(Mocker.Resolve<EpisodeFileMovingService>());
            Mocker.SetConstant<IRenameEpisodeFileService>(Mocker.Resolve<RenameEpisodeFileService>());
            Mocker.SetConstant<IMediaFileTableCleanupService>(Mocker.Resolve<MediaFileTableCleanupService>());
            Mocker.SetConstant<IDiskScanService>(Mocker.Resolve<DiskScanService>());
            Mocker.SetConstant<IRefreshEpisodeService>(Mocker.Resolve<RefreshEpisodeService>());
            Mocker.SetConstant<IExecute<RefreshSeriesCommand>>(Mocker.Resolve<RefreshSeriesService>());
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

        private void GivenNaming()
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
        }

        // Database boundary: episodes are rows in the test database, series and files persist in these lists
        private void GivenDatabase()
        {
            Mocker.SetConstant<ICacheManager>(new CacheManager());
            Mocker.SetConstant<IEpisodeRepository>(Mocker.Resolve<EpisodeRepository>());
            var episodes = Mocker.Resolve<EpisodeService>();
            Mocker.SetConstant<IEpisodeService>(episodes);

            Mocker.GetMock<ISeriesService>().Setup(s => s.GetSeries(1)).Returns(() => _series.JsonClone());
            Mocker.GetMock<ISeriesService>()
                  .Setup(s => s.UpdateSeries(It.IsAny<Series>(), It.IsAny<bool>(), It.IsAny<bool>()))
                  .Returns<Series, bool, bool>((series, a, b) =>
                  {
                      _series = series.JsonClone();
                      return series;
                  });

            var files = Mocker.GetMock<IMediaFileService>();
            files.Setup(s => s.GetFilesBySeries(1)).Returns(() => _files.JsonClone());
            files.Setup(s => s.Get(It.IsAny<IEnumerable<int>>())).Returns<IEnumerable<int>>(ids => _files.Where(f => ids.Contains(f.Id)).ToList().JsonClone());
            files.Setup(s => s.Update(It.IsAny<EpisodeFile>())).Callback<EpisodeFile>(PersistFile);
            files.Setup(s => s.Update(It.IsAny<List<EpisodeFile>>())).Callback<List<EpisodeFile>>(list => list.ForEach(PersistFile));

            // Mirrors MediaFileService.Delete, whose EpisodeFileDeletedEvent reaches EpisodeService synchronously
            files.Setup(s => s.Delete(It.IsAny<EpisodeFile>(), It.IsAny<DeleteMediaFileReason>())).Callback<EpisodeFile, DeleteMediaFileReason>((file, reason) =>
            {
                _files.RemoveAll(f => f.Id == file.Id);
                episodes.Handle(new EpisodeFileDeletedEvent(file, reason));
            });
        }

        private void GivenEpisodes(params Episode[] episodes)
        {
            Db.InsertMany(episodes.ToList());
        }

        private void PersistFile(EpisodeFile file)
        {
            var index = _files.FindIndex(f => f.Id == file.Id);
            _files[index] = file.JsonClone();
        }

        // File system boundary
        private void GivenFileSystem()
        {
            var disk = Mocker.GetMock<IDiskProvider>();
            disk.Setup(s => s.FileExists(It.IsAny<string>())).Returns<string>(p => _disk.Contains(p));
            var folders = new HashSet<string> { "/", "/tv", SeriesPath };
            disk.Setup(s => s.FolderExists(It.IsAny<string>())).Returns<string>(p => folders.Contains(p) || _disk.Any(f => f.StartsWith(p + "/")));
            disk.Setup(s => s.CreateFolder(It.IsAny<string>())).Callback<string>(p => folders.Add(p));
            disk.Setup(s => s.GetFiles(It.IsAny<string>(), It.IsAny<bool>())).Returns<string, bool>((p, r) => _disk.Where(f => f.StartsWith(p + "/")).ToArray());
            disk.Setup(s => s.GetFileSize(It.IsAny<string>())).Returns(0L);
            disk.Setup(s => s.MoveFile(It.IsAny<string>(), It.IsAny<string>(), It.IsAny<bool>())).Callback<string, string, bool>((source, destination, overwrite) =>
            {
                if (_disk.Contains(destination) && !overwrite)
                {
                    throw new FileAlreadyExistsException("File already exists", destination);
                }

                _disk.Remove(source);
                _disk.Add(destination);
            });
        }

        private EpisodeFile File(int id, string relativePath, bool onDisk = true)
        {
            if (onDisk)
            {
                _disk.Add(SeriesPath + "/" + relativePath);
            }

            return new EpisodeFile { Id = id, SeriesId = 1, SeasonNumber = int.Parse(relativePath.Split('/')[0].Replace("Season ", string.Empty)), RelativePath = relativePath, Quality = new QualityModel(Quality.HDTV720p) };
        }

        private static Episode Ep(int tvdbId, int season, int episode, int fileId = 0)
        {
            return new Episode { SeriesId = 1, TvdbId = tvdbId, SeasonNumber = season, EpisodeNumber = episode, Title = "T" + tvdbId, EpisodeFileId = fileId, Monitored = true };
        }

        private static Episode Remote(int tvdbId, int season, int episode)
        {
            return new Episode { TvdbId = tvdbId, SeasonNumber = season, EpisodeNumber = episode, Title = "T" + tvdbId };
        }

        private void Execute()
        {
            Subject.Execute(new ChangeEpisodeOrderCommand { SeriesId = 1, SeasonType = "dvd", RenameFiles = true });
        }

        [Test]
        public void should_apply_the_order_when_a_file_missing_from_disk_is_cleaned_by_the_refresh_scan()
        {
            GivenEpisodes(Ep(101, 1, 1, 11), Ep(102, 1, 2, 12));
            _files = new List<EpisodeFile> { File(11, "Season 1/Show - S01E01.mkv"), File(12, "Season 1/Show - S01E02.mkv", onDisk: false) };
            _aired = new List<Episode> { Remote(101, 1, 1), Remote(102, 1, 2) };
            _dvd = new List<Episode> { Remote(101, 1, 1), Remote(102, 2, 1) };

            var preview = Subject.Preview(1, "dvd");
            preview.CanChange.Should().BeTrue();

            ExceptionVerification.IgnoreWarns();
            ExceptionVerification.IgnoreErrors();

            Assert.DoesNotThrow(Execute);
            _series.SeasonType.Should().Be("dvd");
        }

        [Test]
        public void should_rename_a_chained_file_across_season_folders_without_logging_errors()
        {
            GivenEpisodes(Ep(101, 1, 1, 11), Ep(102, 2, 1, 12));
            _files = new List<EpisodeFile> { File(11, "Season 1/Show - S01E01.mkv"), File(12, "Season 2/Show - S02E01.mkv") };
            _aired = new List<Episode> { Remote(101, 1, 1), Remote(102, 2, 1) };
            _dvd = new List<Episode> { Remote(101, 2, 1), Remote(102, 2, 2) };

            // ExceptionVerification loses its target when this is the run's first test, so capture logs directly
            var memory = new NLog.Targets.MemoryTarget { Layout = "${level}|${message}" };
            NLog.LogManager.Configuration.AddRuleForAllLevels(memory);
            NLog.LogManager.ReconfigExistingLoggers();

            try
            {
                var preview = Subject.Preview(1, "dvd");
                preview.Files.Should().OnlyContain(f => f.Status == EpisodeOrderPreviewFileStatus.Renamed);
                preview.CanChange.Should().BeTrue();

                Execute();

                _disk.Should().BeEquivalentTo(new[] { SeriesPath + "/Season 2/Show - S02E01.mkv", SeriesPath + "/Season 2/Show - S02E02.mkv" });
                memory.Logs.Where(l => l.StartsWith("Error|")).Should().BeEmpty();
            }
            finally
            {
                var rule = NLog.LogManager.Configuration.LoggingRules.Single(r => r.Targets.Contains(memory));
                NLog.LogManager.Configuration.LoggingRules.Remove(rule);
                NLog.LogManager.ReconfigExistingLoggers();
            }

            ExceptionVerification.IgnoreWarns();
            ExceptionVerification.IgnoreErrors();
        }

        [Test]
        public void should_announce_each_pass_of_a_chained_rename_in_order()
        {
            GivenEpisodes(Ep(101, 1, 1, 11), Ep(102, 2, 1, 12));
            _files = new List<EpisodeFile> { File(11, "Season 1/Show - S01E01.mkv"), File(12, "Season 2/Show - S02E01.mkv") };
            _aired = new List<Episode> { Remote(101, 1, 1), Remote(102, 2, 1) };
            _dvd = new List<Episode> { Remote(101, 2, 1), Remote(102, 2, 2) };

            var passes = new List<List<string>>();
            Mocker.GetMock<IEventAggregator>()
                  .Setup(s => s.PublishEvent(It.IsAny<SeriesRenamedEvent>()))
                  .Callback<SeriesRenamedEvent>(e => passes.Add(e.RenamedFiles.Select(r => r.PreviousRelativePath).ToList()));

            Execute();

            // Extras follow the files on each event and never overwrite, so a file's must move before the next file takes its name
            passes.Should().BeEquivalentTo(
                new[] { new List<string> { "Season 2/Show - S02E01.mkv" }, new List<string> { "Season 1/Show - S01E01.mkv" } },
                o => o.WithStrictOrdering());
            ExceptionVerification.IgnoreWarns();
            ExceptionVerification.IgnoreErrors();
        }

        [Test]
        public void should_not_count_all_season_scene_titles_as_carrying_a_season()
        {
            GivenEpisodes(Ep(101, 1, 1, 11));
            _files = new List<EpisodeFile> { File(11, "Season 1/Show - S01E01.mkv") };
            _aired = new List<Episode> { Remote(101, 1, 1) };
            _dvd = new List<Episode> { Remote(101, 1, 1) };
            _storedSceneMappings = new List<SceneMapping>
            {
                new SceneMapping { TvdbId = 1000, Title = "Show Alt", ParseTerm = "showalt", SearchTerm = "Show Alt", SeasonNumber = -1, SceneSeasonNumber = -1, Type = "ServicesProvider" },
                new SceneMapping { TvdbId = 1000, Title = "Show Alt 2", ParseTerm = "showalt2", SearchTerm = "Show Alt 2", SeasonNumber = -1, Type = "ServicesProvider" }
            };

            var preview = Subject.Preview(1, "dvd");

            preview.SceneSeasonTitles.Should().Be(0);
            preview.CanChange.Should().BeTrue();
        }
    }
}
