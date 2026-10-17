using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using FluentAssertions;
using Moq;
using NLog;
using NUnit.Framework;
using NzbDrone.Common;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Disk;
using NzbDrone.Core.Download.Aggregation.Aggregators;
using NzbDrone.Core.Extras.Others;
using NzbDrone.Core.Extras.Subtitles;
using NzbDrone.Core.Indexers;
using NzbDrone.Core.Languages;
using NzbDrone.Core.MediaFiles;
using NzbDrone.Core.MediaFiles.EpisodeImport;
using NzbDrone.Core.MediaFiles.EpisodeImport.Aggregation;
using NzbDrone.Core.MediaFiles.EpisodeImport.Aggregation.Aggregators;
using NzbDrone.Core.MediaFiles.EpisodeImport.Aggregation.Aggregators.Augmenters.Language;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.Parser;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Parser.TitleMatching;
using NzbDrone.Core.Tags;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;
using NzbDrone.Test.Common;

namespace NzbDrone.Core.Test.ParserTests.TitleMatching
{
    // Real parser, ParsingService, matcher and import aggregation; only the database-backed services are mocked
    [TestFixture]
    public class EventTitleMappingFixture : CoreTest<ParsingService>
    {
        private const int TagId = 7;

        private Series _series;
        private List<Episode> _episodes;
        private Tag _tag;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ICacheManager>(Mocker.Resolve<CacheManager>());
            Mocker.SetConstant<IEpisodeTitleMatcher>(Mocker.Resolve<EpisodeTitleMatcher>());

            _series = new Series
            {
                Id = 1,
                Title = "Formula 1",
                CleanTitle = "formula1",
                Tags = new HashSet<int> { TagId },
                SeriesType = SeriesTypes.Standard,
                SeasonType = SeasonType.Official,
                OriginalLanguage = Language.English
            };

            var season = EpisodeTitleMatcherFixture.Season2026;
            var seasonStart = DateTime.Parse(season[0].AirDate, CultureInfo.InvariantCulture);
            var shiftedStart = DateTime.UtcNow.Date.AddDays(-400);

            _episodes = season.Select(e => new Episode
            {
                Id = 1000 + e.Number,
                SeriesId = _series.Id,
                SeasonNumber = 2026,
                EpisodeNumber = e.Number,
                Title = e.Title,
                AirDateUtc = shiftedStart + (DateTime.Parse(e.AirDate, CultureInfo.InvariantCulture) - seasonStart)
            }).ToList();

            _tag = new Tag { Id = TagId, Label = EpisodeTitleMatcher.TagLabel };

            Mocker.GetMock<ITagRepository>()
                  .Setup(s => s.FindByLabel(EpisodeTitleMatcher.TagLabel))
                  .Returns(() => _tag);

            Mocker.GetMock<ISeriesService>()
                  .Setup(s => s.FindByTitle(It.IsAny<string>()))
                  .Returns((string title) => title.CleanSeriesTitle() == _series.CleanTitle ? _series : null);

            Mocker.GetMock<IEpisodeService>()
                  .Setup(s => s.GetEpisodesBySeason(_series.Id, It.IsAny<int>()))
                  .Returns((int seriesId, int seasonNumber) => seasonNumber == 2026 ? _episodes : new List<Episode>());

            Mocker.GetMock<IEpisodeService>()
                  .Setup(s => s.FindEpisode(_series.Id, It.IsAny<int>(), It.IsAny<int>()))
                  .Returns((int seriesId, int seasonNumber, int episodeNumber) =>
                      _episodes.SingleOrDefault(e => e.SeasonNumber == seasonNumber && e.EpisodeNumber == episodeNumber));
        }

        private static IEnumerable<TestCaseData> MustMatchTitles()
        {
            yield return new TestCaseData("Formula1.2026.Singapore.Grand.Prix.1080p.WEB.H.264-BILLIE", 94);
            yield return new TestCaseData("Formula1.2026.China.Grand.Prix.Practice.1080p.WEB.h264-BILLIE", 15);
            yield return new TestCaseData("Formula1_2026_China_Grand_Prix_Sprint_Race_1080p_WEB_h264-BILLIE", 17);
            yield return new TestCaseData("Formula1.2026.China.Grand.Prix.Qualifying.1080p.WEB.h264-BILLIE", 18);
            yield return new TestCaseData("Formula1.2026.Round17.Singapore.Race.F1LIVE.F1TV.WEB-DL.1080p.H264.English-MWR", 94);
            yield return new TestCaseData("Formula1.2026.Round05.Canada.Sprint.F1LIVE.F1TV.WEB-DL.1080p.H264.English-MWR", 32);
            yield return new TestCaseData("Formula1.2026.Round05.Canada.Sprint.Qualifying.F1TV.WEB-DL.2160p.HLG.h265.Multi-MWR", 31);
            yield return new TestCaseData("Formula1.2026.Canadian.Grand.Prix.Sprint.Qualification.1080p.WEB.H.264-BILLIE", 31);
            yield return new TestCaseData("Formula1.2026.Canadian.Grand.Prix.1080p.WEB.H.264-BILLIE", 34);
            yield return new TestCaseData("Formula1.2026.Round06.Monaco.Qualifying.F1TV.WEB-DL.1080p.H264.English-MWR", 38);
            yield return new TestCaseData("Formula1.2026.Round07.Spain.Barcelona.FP2.F1TV.WEB-DL.1080p.H264.English-MWR", 41);
            yield return new TestCaseData("Formula1.2026.Barcelona-Catalunya.Grand.Prix.Practice.Three.1080p.WEB.H.264-BILLIE", 42);
            yield return new TestCaseData("Formula1.2026.Austrian.Grand.Prix.Qualifying.1080p.WEB.H.264-BILLIE", 48);
            yield return new TestCaseData("Formula1.2026.British.Grand.Prix.Sprint.Race.1080p.AHDTV.x264-DARKSPORT", 52);
            yield return new TestCaseData("Formula1.2026.Hungarian.Grand.Prix.FP3.1080p.WEB.h264-VERUM", 62);
            yield return new TestCaseData("Formula1.2026.Dutch.Grand.Prix.Qualifying.1080p.AHDTV.x264-DARKSPORT", 68);
            yield return new TestCaseData("Formula1.2026.Italian.Grand.Prix.1080p.WEB.H.264-BILLIE", 74);
            yield return new TestCaseData("Formula1.2026.Round14.Spain.Madrid.Race.F1TV.WEB-DL.1080p.H264.English-MWR", 79);
            yield return new TestCaseData("Formula1.2026.Round16.Bahrain.Race.F1LIVE.F1TV.WEB-DL.1080p.H264.English-MWR", 89);
            yield return new TestCaseData("Formula1.2026.Singapore.Grand.Prix.Practice.One.PROPER.1080p.WEB.H.264-BILLIE", 90);
            yield return new TestCaseData("Formula1.2026.Singapore.Grand.Prix.Qualification.1080p.WEB.H.264-BILLIE", 93);
            yield return new TestCaseData("Formula.1.2026x17.Singapore.Qualifying.SkyF1HD.1080p", 93);
        }

        [TestCaseSource(nameof(MustMatchTitles))]
        public void should_map_release_to_episode_through_real_parser(string title, int episodeNumber)
        {
            var remoteEpisode = MapRelease(title);

            remoteEpisode.Series.Should().Be(_series);
            remoteEpisode.Episodes.Select(e => e.EpisodeNumber).Should().Equal(episodeNumber);
        }

        [TestCaseSource(nameof(MustMatchTitles))]
        public void should_import_download_to_the_episode_its_release_maps_to(string title, int episodeNumber)
        {
            var localEpisode = Import(Download(title));

            localEpisode.Episodes.Select(e => e.EpisodeNumber).Should().Equal(episodeNumber);
        }

        [Test]
        public void should_import_daily_dated_file_like_its_release()
        {
            const string name = "Formula1.2026.10.11.Singapore.Grand.Prix.1080p.WEB.H.264-BILLIE";
            var path = $"/downloads/{name}/{name}.mkv".AsOsAgnostic();

            MapRelease(name).Episodes.Select(e => e.EpisodeNumber).Should().Equal(94);

            var localEpisode = Import(new LocalEpisode
            {
                Path = path,
                Series = _series,
                SceneSource = true,
                FileEpisodeInfo = Parser.Parser.ParsePath(path)
            });

            localEpisode.Episodes.Select(e => e.EpisodeNumber).Should().Equal(94);
        }

        [Test]
        public void should_keep_episode_of_file_named_by_sonarr()
        {
            var path = "/tv/Formula 1/Season 2026/Formula 1 - S2026E14 - Australia (Race) WEBDL-1080p.mkv".AsOsAgnostic();

            var localEpisode = Import(new LocalEpisode
            {
                Path = path,
                Series = _series,
                ExistingFile = true,
                FileEpisodeInfo = Parser.Parser.ParsePath(path)
            });

            localEpisode.Episodes.Select(e => e.EpisodeNumber).Should().Equal(14);
        }

        [TestCase("Formula1.2026.Round17.Singapore.Race.F1TV.WEB-DL.1080p.H264.German-MWR", "German")]
        [TestCase("Formula1.2026.Round17.Singapore.Race.F1TV.WEB-DL.1080p.H264.Spanish-MWR", "Spanish")]
        [TestCase("Formula1.2026.Round17.Singapore.Race.F1TV.WEB-DL.1080p.H264.Dutch-MWR", "Dutch")]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.FRENCH.1080p.WEB.H264-GRP", "French")]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.ITALIAN.1080p.WEB.H264-GRP", "Italian")]
        public void should_keep_audio_language_that_does_not_name_the_event(string title, string language)
        {
            var remoteEpisode = MapRelease(title);
            remoteEpisode.Episodes.Select(e => e.EpisodeNumber).Should().Equal(94);

            ReleaseLanguages(remoteEpisode).Select(l => l.Name).Should().Equal(language);
        }

        [TestCase("Formula.1.2026.Gran.Premio.del.Belgio.Gara.ITALIAN.1080p.WEB.H264-GRP")]
        [TestCase("Formula.1.2026.Gran.Premio.de.Japon.Carrera.SPANISH.1080p.WEB.H264-GRP")]
        public void should_not_pick_event_from_a_language_word_alone(string title)
        {
            MapRelease(title).Episodes.Should().BeEmpty();
        }

        [TestCase("Formula1.2026.Round17.Singapore.Tech.Talk.F1TV.WEB-DL.1080p.H264.English-MWR")]
        [TestCase("Formula1.2026.Round17.Singapore.Grid.Walk.F1TV.WEB-DL.1080p.H264.English-MWR")]
        [TestCase("Formula1.2026.Round17.Singapore.F1TV.WEB-DL.1080p.H264.English-MWR")]
        public void should_not_map_release_without_session_or_grand_prix_to_the_race(string title)
        {
            MapRelease(title).Episodes.Should().BeEmpty();
            Import(Download(title)).Episodes.Should().BeEmpty();
        }

        [TestCase("Formula.1.2026x17.Singapore.Press.Conference.SkyF1HD.1080p")]
        [TestCase("Formula.1.2026x17.Singapore.Teds.Notebook.SkyF1HD.1080p")]
        [TestCase("Formula.1.2026x17.Singapore.Practice.4.SkyF1HD.1080p")]
        [TestCase("Formula.1.2026x17.Singapore.Qualifying.And.Race.SkyF1HD.1080p")]
        public void should_not_fall_back_to_parsed_numbers_when_no_event_matches(string title)
        {
            MapRelease(title).Episodes.Should().BeEmpty();
            Import(Download(title)).Episodes.Should().BeEmpty();
        }

        [Test]
        public void should_import_event_named_language_file_with_the_release_language()
        {
            const string name = "Formula1.2026.Italian.Grand.Prix.1080p.WEB.H.264-BILLIE";

            var releaseLanguages = ReleaseLanguages(MapRelease(name));
            var localEpisode = Import(Download(name));

            localEpisode.Episodes.Select(e => e.EpisodeNumber).Should().Equal(74);
            releaseLanguages.Should().Equal(Language.English);
            localEpisode.Languages.Should().Equal(releaseLanguages);
        }

        [Test]
        public void should_keep_special_found_by_its_title()
        {
            const string title = "Formula1.2026.The.Last.Lap.1080p.WEB.H264-GRP";
            var special = new Episode { Id = 2005, SeriesId = _series.Id, SeasonNumber = 0, EpisodeNumber = 5, Title = "The Last Lap" };
            _episodes.Add(special);

            Mocker.GetMock<IEpisodeService>()
                  .Setup(s => s.FindEpisodeByTitle(_series.Id, 0, title))
                  .Returns(special);

            var specialEpisodeInfo = Subject.ParseSpecialEpisodeTitle(Parser.Parser.ParseTitle(title), title, _series);
            specialEpisodeInfo.Should().NotBeNull();

            Subject.Map(specialEpisodeInfo, 0, 0, null).Episodes.Select(e => e.Id).Should().Equal(special.Id);
            Subject.GetEpisodes(specialEpisodeInfo, _series, false).Select(e => e.Id).Should().Equal(special.Id);
            Subject.GetEpisodes(specialEpisodeInfo, _series, true).Select(e => e.Id).Should().Equal(special.Id);
        }

        [Test]
        public void should_pair_extra_files_named_unlike_their_video_by_event()
        {
            _series.Path = "/tv/Formula 1".AsOsAgnostic();

            var folder = "/downloads/Formula.1.2026x17.Singapore.SkyF1HD".AsOsAgnostic();
            var qualifyingVideo = Path.Combine(folder, "Formula.1.2026x17.Singapore.Qualifying.SkyF1HD.1080p.mkv");
            var raceVideo = Path.Combine(folder, "Formula.1.2026x17.Singapore.Race.SkyF1HD.1080p.mkv");
            var qualifyingExtras = new[] { "Formula.1.2026x17.Singapore.Qualifying.English.srt", "Formula.1.2026x17.Singapore.Qualifying.Poster.png" }
                .Select(f => Path.Combine(folder, f))
                .ToList();
            var raceExtras = new[] { "Formula.1.2026x17.Singapore.Race.English.srt", "Formula.1.2026x17.Singapore.Race.Poster.png" }
                .Select(f => Path.Combine(folder, f))
                .ToList();

            var localEpisode = Import(new LocalEpisode
            {
                Path = qualifyingVideo,
                Series = _series,
                SceneSource = true,
                FileEpisodeInfo = Parser.Parser.ParsePath(qualifyingVideo)
            });
            localEpisode.Episodes.Select(e => e.EpisodeNumber).Should().Equal(93);

            var episodeFile = new EpisodeFile { RelativePath = "Season 2026/Formula 1 - S2026E93 - Singapore (Qualifying).mkv".AsOsAgnostic() };

            Mocker.GetMock<IDiskProvider>()
                  .Setup(s => s.GetParentFolder(It.IsAny<string>()))
                  .Returns((string path) => Directory.GetParent(path).FullName);
            Mocker.GetMock<IDiskProvider>()
                  .Setup(s => s.GetFiles(folder, true))
                  .Returns(new[] { qualifyingVideo, raceVideo });
            Mocker.GetMock<IDetectSample>()
                  .Setup(s => s.IsSample(It.IsAny<Series>(), It.IsAny<string>(), It.IsAny<bool>()))
                  .Returns(DetectSampleResult.NotSample);

            var extras = qualifyingExtras.Concat(raceExtras).ToList();
            Mocker.Resolve<SubtitleService>().ImportFiles(localEpisode, episodeFile, extras.Where(f => f.EndsWith(".srt")).ToList(), true).ToList();
            Mocker.Resolve<OtherExtraService>().ImportFiles(localEpisode, episodeFile, extras.Where(f => f.EndsWith(".png")).ToList(), true).ToList();

            Mocker.GetMock<IDiskTransferService>().Invocations
                  .Where(i => i.Method.Name == nameof(IDiskTransferService.TransferFile))
                  .Select(i => (string)i.Arguments[0])
                  .Should().BeEquivalentTo(qualifyingExtras);
        }

        [Test]
        public void should_match_once_the_title_match_tag_is_created()
        {
            var matcher = Mocker.Resolve<IEpisodeTitleMatcher>();
            var parsedEpisodeInfo = Parser.Parser.ParseTitle("Formula1.2026.Singapore.Grand.Prix.1080p.WEB.H.264-BILLIE");

            _tag = null;
            matcher.ParseEventTitle(parsedEpisodeInfo, _series).Should().BeNull();

            _tag = new Tag { Id = TagId, Label = EpisodeTitleMatcher.TagLabel };
            Mocker.GetMock<IServiceFactory>()
                  .Setup(s => s.BuildAll<IHandle<TagsUpdatedEvent>>())
                  .Returns(new List<IHandle<TagsUpdatedEvent>> { (IHandle<TagsUpdatedEvent>)matcher });
            Mocker.Resolve<EventAggregator>().PublishEvent(new TagsUpdatedEvent());

            matcher.ParseEventTitle(parsedEpisodeInfo, _series).EpisodeNumbers.Should().Equal(94);
        }

        private RemoteEpisode MapRelease(string title)
        {
            var parsedEpisodeInfo = Parser.Parser.ParseTitle(title);
            parsedEpisodeInfo.Should().NotBeNull("the parser must accept '{0}' or the release is never mapped", title);

            return Subject.Map(parsedEpisodeInfo, 0, 0, null);
        }

        private List<Language> ReleaseLanguages(RemoteEpisode remoteEpisode)
        {
            remoteEpisode.Release = new ReleaseInfo { Title = remoteEpisode.ParsedEpisodeInfo.ReleaseTitle };

            return new AggregateLanguages(Mocker.Resolve<IIndexerFactory>(), LogManager.GetCurrentClassLogger())
                .Aggregate(remoteEpisode)
                .Languages;
        }

        private LocalEpisode Download(string name)
        {
            var path = $"/downloads/{name}/{name}.mkv".AsOsAgnostic();

            return new LocalEpisode
            {
                Path = path,
                Series = _series,
                SceneSource = true,
                FileEpisodeInfo = Parser.Parser.ParsePath(path),
                FolderEpisodeInfo = Parser.Parser.ParseTitle(name),
                DownloadClientEpisodeInfo = Parser.Parser.ParseTitle(name)
            };
        }

        private LocalEpisode Import(LocalEpisode localEpisode)
        {
            Mocker.SetConstant<IParsingService>(Subject);

            var augmenters = new IAugmentLanguage[]
            {
                new AugmentLanguageFromFileName(),
                new AugmentLanguageFromFolder(),
                new AugmentLanguageFromDownloadClientItem(),
                new AugmentLanguageFromMediaInfo()
            };
            var aggregators = new IAggregateLocalEpisode[]
            {
                Mocker.Resolve<AggregateEpisodes>(),
                new AggregateLanguage(augmenters, LogManager.GetCurrentClassLogger())
            };

            Mocker.SetConstant<IEnumerable<IAggregateLocalEpisode>>(aggregators);

            return Mocker.Resolve<AggregationService>().Augment(localEpisode, null);
        }
    }
}
