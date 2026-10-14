using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using FizzWare.NBuilder;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Cache;
using NzbDrone.Core.Languages;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Parser.TitleMatching;
using NzbDrone.Core.Tags;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.Test.ParserTests.TitleMatching
{
    [TestFixture]
    public class EpisodeTitleMatcherFixture : CoreTest<EpisodeTitleMatcher>
    {
        private const int TitleMatchTagId = 7;

        // TheTVDB's 2026 Formula 1 season as published.
        private static readonly (int Number, string AirDate, string Title)[] Season2026 =
        {
            (1, "2026-02-11", "Bahrain Pre-Season Testing 1 - Day 1"),
            (2, "2026-02-12", "Bahrain Pre-Season Testing 1 - Day 2"),
            (3, "2026-02-13", "Bahrain Pre-Season Testing 1 - Day 3"),
            (4, "2026-02-18", "2026 Testing 2 - Day 1 (Session 1)"),
            (5, "2026-02-18", "2026 Testing 2  - Day 1 (Session 2)"),
            (6, "2026-02-19", "2026 Testing 2 - Day 2 (Session 1)"),
            (7, "2026-02-19", "2026 Testing 2 - Day 2 (Session 2)"),
            (8, "2026-02-20", "2026 Testing 2 - Day 3 (Session 1)"),
            (9, "2026-02-20", "2026 Testing 2 - Day 3 (Session 2)"),
            (10, "2026-03-06", "Australia (Practice 1)"),
            (11, "2026-03-06", "Australia (Practice 2)"),
            (12, "2026-03-07", "Australia (Practice 3)"),
            (13, "2026-03-07", "Australia (Qualifying)"),
            (14, "2026-03-08", "Australia (Race)"),
            (15, "2026-03-13", "China (Practice)"),
            (16, "2026-03-13", "China (Sprint Qualifying)"),
            (17, "2026-03-14", "China (Sprint Race)"),
            (18, "2026-03-14", "China (Qualifying)"),
            (19, "2026-03-15", "China (Race)"),
            (20, "2026-03-27", "Japan (Practice 1)"),
            (21, "2026-03-27", "Japan (Practice 2)"),
            (22, "2026-03-28", "Japan (Practice 3)"),
            (23, "2026-03-28", "Japan (Qualifying)"),
            (24, "2026-03-29", "Japan (Race)"),
            (25, "2026-05-01", "Miami (Practice)"),
            (26, "2026-05-01", "Miami (Sprint Qualifying)"),
            (27, "2026-05-02", "Miami (Sprint Race)"),
            (28, "2026-05-02", "Miami (Qualifying)"),
            (29, "2026-05-03", "Miami (Race)"),
            (30, "2026-05-22", "Canada (Practice)"),
            (31, "2026-05-22", "Canada (Sprint Qualifying)"),
            (32, "2026-05-23", "Canada (Sprint Race)"),
            (33, "2026-05-23", "Canada (Qualifying)"),
            (34, "2026-05-24", "Canada (Race)"),
            (35, "2026-06-05", "Monaco (Practice 1)"),
            (36, "2026-06-05", "Monaco (Practice 2)"),
            (37, "2026-06-06", "Monaco (Practice 3)"),
            (38, "2026-06-06", "Monaco (Qualifying)"),
            (39, "2026-06-07", "Monaco (Race)"),
            (40, "2026-06-12", "Barcelona-Catalunya (Practice 1)"),
            (41, "2026-06-12", "Barcelona-Catalunya (Practice 2)"),
            (42, "2026-06-13", "Barcelona-Catalunya (Practice 3)"),
            (43, "2026-06-13", "Barcelona-Catalunya (Qualifying)"),
            (44, "2026-06-14", "Barcelona-Catalunya (Race)"),
            (45, "2026-06-26", "Austria (Practice 1)"),
            (46, "2026-06-26", "Austria (Practice 2)"),
            (47, "2026-06-27", "Austria (Practice 3)"),
            (48, "2026-06-27", "Austria (Qualifying)"),
            (49, "2026-06-28", "Austria (Race)"),
            (50, "2026-07-03", "Great Britain (Practice)"),
            (51, "2026-07-03", "Great Britain (Sprint Qualifying)"),
            (52, "2026-07-04", "Great Britain (Sprint Race)"),
            (53, "2026-07-04", "Great Britain (Qualifying)"),
            (54, "2026-07-05", "Great Britain (Race)"),
            (55, "2026-07-17", "Belgium (Practice 1)"),
            (56, "2026-07-17", "Belgium (Practice 2)"),
            (57, "2026-07-18", "Belgium (Practice 3)"),
            (58, "2026-07-18", "Belgium (Qualifying)"),
            (59, "2026-07-19", "Belgium (Race)"),
            (60, "2026-07-24", "Hungary (Practice 1)"),
            (61, "2026-07-24", "Hungary (Practice 2)"),
            (62, "2026-07-25", "Hungary (Practice 3)"),
            (63, "2026-07-25", "Hungary (Qualifying)"),
            (64, "2026-07-26", "Hungary (Race)"),
            (65, "2026-08-21", "Netherlands (Practice)"),
            (66, "2026-08-21", "Netherlands (Sprint Qualifying)"),
            (67, "2026-08-22", "Netherlands (Sprint Race)"),
            (68, "2026-08-22", "Netherlands (Qualifying)"),
            (69, "2026-08-23", "Netherlands (Race)"),
            (70, "2026-09-04", "Italy (Practice 1)"),
            (71, "2026-09-04", "Italy (Practice 2)"),
            (72, "2026-09-05", "Italy (Practice 3)"),
            (73, "2026-09-05", "Italy (Qualifying)"),
            (74, "2026-09-06", "Italy (Race)"),
            (75, "2026-09-11", "Spain (Practice 1)"),
            (76, "2026-09-11", "Spain (Practice 2)"),
            (77, "2026-09-12", "Spain (Practice 3)"),
            (78, "2026-09-12", "Spain (Qualifying)"),
            (79, "2026-09-13", "Spain (Race)"),
            (80, "2026-09-24", "Azerbaijan (Practice 1)"),
            (81, "2026-09-24", "Azerbaijan (Practice 2)"),
            (82, "2026-09-25", "Azerbaijan (Practice 3)"),
            (83, "2026-09-25", "Azerbaijan (Qualifying)"),
            (84, "2026-09-26", "Azerbaijan (Race)"),
            (85, "2026-10-02", "Bahrain (Practice 1)"),
            (86, "2026-10-02", "Bahrain (Practice 2)"),
            (87, "2026-10-03", "Bahrain (Practice 3)"),
            (88, "2026-10-03", "Bahrain (Qualifying)"),
            (89, "2026-10-04", "Bahrain (Race)"),
            (90, "2026-10-09", "Singapore (Practice)"),
            (91, "2026-10-09", "Singapore (Sprint Qualifying)"),
            (92, "2026-10-10", "Singapore (Sprint Race)"),
            (93, "2026-10-10", "Singapore (Qualifying)"),
            (94, "2026-10-11", "Singapore (Race)"),
            (95, "2026-10-23", "United States (Practice 1)"),
            (96, "2026-10-23", "United States (Practice 2)"),
            (97, "2026-10-24", "United States (Practice 3)"),
            (98, "2026-10-24", "United States (Qualifying)"),
            (99, "2026-10-25", "United States (Race)"),
            (100, "2026-10-30", "Mexico (Practice 1)"),
            (101, "2026-10-30", "Mexico (Practice 2)"),
            (102, "2026-10-31", "Mexico (Practice 3)"),
            (103, "2026-10-31", "Mexico (Qualifying)"),
            (104, "2026-11-01", "Mexico (Race)"),
            (105, "2026-11-06", "Brazil (Practice 1)"),
            (106, "2026-11-06", "Brazil (Practice 2)"),
            (107, "2026-11-07", "Brazil (Practice 3)"),
            (108, "2026-11-07", "Brazil (Qualifying)"),
            (109, "2026-11-08", "Brazil (Race)"),
            (110, "2026-11-19", "Las Vegas (Practice 1)"),
            (111, "2026-11-19", "Las Vegas (Practice 2)"),
            (112, "2026-11-20", "Las Vegas (Practice 3)"),
            (113, "2026-11-20", "Las Vegas (Qualifying)"),
            (114, "2026-11-21", "Las Vegas (Race)"),
            (115, "2026-11-27", "Qatar (Practice 1)"),
            (116, "2026-11-27", "Qatar (Practice 2)"),
            (117, "2026-11-28", "Qatar (Practice 3)"),
            (118, "2026-11-28", "Qatar (Qualifying)"),
            (119, "2026-11-29", "Qatar (Race)"),
            (120, "2026-12-04", "Abu Dhabi (Practice 1)"),
            (121, "2026-12-04", "Abu Dhabi (Practice 2)"),
            (122, "2026-12-05", "Abu Dhabi (Practice 3)"),
            (123, "2026-12-05", "Abu Dhabi (Qualifying)"),
            (124, "2026-12-06", "Abu Dhabi (Race)")
        };

        private Series _series;
        private List<Episode> _episodes;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ICacheManager>(Mocker.Resolve<CacheManager>());

            _series = Builder<Series>.CreateNew()
                                     .With(s => s.Id = 1)
                                     .With(s => s.Title = "Formula 1")
                                     .With(s => s.Tags = new HashSet<int> { TitleMatchTagId })
                                     .Build();

            // Shifted into the past so every event has aired whenever the tests run.
            var seasonStart = DateTime.Parse(Season2026[0].AirDate, CultureInfo.InvariantCulture);
            var shiftedStart = DateTime.UtcNow.Date.AddDays(-400);

            _episodes = Season2026.Select(e => new Episode
            {
                Id = 1000 + e.Number,
                SeriesId = _series.Id,
                SeasonNumber = 2026,
                EpisodeNumber = e.Number,
                Title = e.Title,
                AirDateUtc = shiftedStart + (DateTime.Parse(e.AirDate, CultureInfo.InvariantCulture) - seasonStart)
            }).ToList();

            Mocker.GetMock<ITagRepository>()
                  .Setup(s => s.FindByLabel(EpisodeTitleMatcher.TagLabel))
                  .Returns(new Tag { Id = TitleMatchTagId, Label = EpisodeTitleMatcher.TagLabel });

            Mocker.GetMock<IEpisodeService>()
                  .Setup(s => s.GetEpisodesBySeason(_series.Id, It.IsAny<int>()))
                  .Returns((int seriesId, int seasonNumber) => seasonNumber == 2026 ? _episodes : new List<Episode>());
        }

        [TestCase("Formula1.2026.Singapore.Grand.Prix.1080p.WEB.H.264-BILLIE", 94)]
        [TestCase("Formula1.2026.China.Grand.Prix.Practice.1080p.WEB.h264-BILLIE", 15)]
        [TestCase("Formula1_2026_China_Grand_Prix_Sprint_Race_1080p_WEB_h264-BILLIE", 17)]
        [TestCase("Formula1.2026.China.Grand.Prix.Qualifying.1080p.WEB.h264-BILLIE", 18)]
        [TestCase("Formula1.2026.Round17.Singapore.Race.F1LIVE.F1TV.WEB-DL.1080p.H264.English-MWR", 94)]
        [TestCase("Formula1.2026.Round05.Canada.Sprint.F1LIVE.F1TV.WEB-DL.1080p.H264.English-MWR", 32)]
        [TestCase("Formula1.2026.Round05.Canada.Sprint.Qualifying.F1TV.WEB-DL.2160p.HLG.h265.Multi-MWR", 31)]
        [TestCase("Formula1.2026.Canadian.Grand.Prix.1080p.WEB.H.264-BILLIE", 34)]
        [TestCase("Formula1.2026.Round06.Monaco.Qualifying.F1TV.WEB-DL.1080p.H264.English-MWR", 38)]
        [TestCase("Formula1.2026.Round07.Spain.Barcelona.FP2.F1TV.WEB-DL.1080p.H264.English-MWR", 41)]
        [TestCase("Formula1.2026.Barcelona-Catalunya.Grand.Prix.Practice.Three.1080p.WEB.H.264-BILLIE", 42)]
        [TestCase("Formula1.2026.Austrian.Grand.Prix.Qualifying.1080p.WEB.H.264-BILLIE", 48)]
        [TestCase("Formula1.2026.British.Grand.Prix.Sprint.Race.1080p.AHDTV.x264-DARKSPORT", 52)]
        [TestCase("Formula1.2026.Hungarian.Grand.Prix.FP3.1080p.WEB.h264-VERUM", 62)]
        [TestCase("Formula1.2026.Dutch.Grand.Prix.Qualifying.1080p.AHDTV.x264-DARKSPORT", 68)]
        [TestCase("Formula1.2026.Italian.Grand.Prix.1080p.WEB.H.264-BILLIE", 74)]
        [TestCase("Formula1.2026.Round14.Spain.Madrid.Race.F1TV.WEB-DL.1080p.H264.English-MWR", 79)]
        [TestCase("Formula1.2026.Round16.Bahrain.Race.F1LIVE.F1TV.WEB-DL.1080p.H264.English-MWR", 89)]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.Practice.One.PROPER.1080p.WEB.H.264-BILLIE", 90)]
        [TestCase("Formula.1.2026x17.Singapore.Qualifying.SkyF1HD.1080p", 93)]
        [TestCase("Sky Sports _ Formula1_2026_Singapore_Grand_Prix_UNCUT_1080p_AHDTV_x264-DARKSPORT", 94)]
        public void should_match_release_to_episode_by_title(string title, int episodeNumber)
        {
            var eventEpisodeInfo = Subject.ParseEventTitle(Parse(title), _series);

            eventEpisodeInfo.SeasonNumber.Should().Be(2026);
            eventEpisodeInfo.EpisodeNumbers.Should().Equal(episodeNumber);
            eventEpisodeInfo.ReleaseTitle.Should().Be(title);
        }

        [TestCase("Formula1.Academy.2026.Canadian.Grand.Prix.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.Drivers.Press.Conference.1080p.AHDTV.x264-DARKSPORT")]
        [TestCase("Formula1.2026.Round17.Singapore.Pre-Race.Show.F1TV.WEB-DL.1080p.H264.English-MWR")]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.Weekend.Warm-Up.1080p.WEB.h264-VERUM")]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.Teds.Notebook.720p.HDTV.H.264-JFF")]
        [TestCase("Formula1.2026.Round17.Singapore.Full.Race.Events.2160p.WEB-DL.DDP5.1.HFR.H.265-WADU")]
        [TestCase("Formula.1-LENOVO.Grand.Prix.Du.Canada.GP-Startline-24.Mayis.2026-1080p.TOD.WEB-DL.AAC.H.264-TURG")]
        [TestCase("Formula1.2026.Bahrain.Pre.Season.Testing.Two.Day.One.Session.One.HDR.2160p.WEB.h265-VERUM")]
        [TestCase("Formula1.2025.Singapore.Grand.Prix.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Round07.Singapore.Race.F1TV.WEB-DL.1080p.H264.English-MWR")]
        [TestCase("Formula1.2026.Singapore.Qualifying.And.Race.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Barcelona.Grand.Prix.Practice.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Grand.Prix.Race.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.S2026E89.1080p.ANTP.WEB-DL.AAC2.0.H.264-PlayWEB")]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.FP3.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.Practice.4.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Hungarian.Grand.Prix.Race.4.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.Weekend.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2025.2026.Singapore.Grand.Prix.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Singapore.Grand.Prix.2025.Rerun.1080p.WEB.H.264-BILLIE")]
        public void should_not_match_release(string title)
        {
            Subject.ParseEventTitle(Parse(title), _series).Should().BeNull();
        }

        [Test]
        public void should_not_match_when_series_is_not_tagged()
        {
            _series.Tags = new HashSet<int>();

            Subject.ParseEventTitle(Parse("Formula1.2026.Singapore.Grand.Prix.1080p.WEB.H.264-BILLIE"), _series).Should().BeNull();

            Mocker.GetMock<IEpisodeService>()
                  .Verify(v => v.GetEpisodesBySeason(It.IsAny<int>(), It.IsAny<int>()), Times.Never());
        }

        [Test]
        public void should_not_match_when_series_only_has_other_tags()
        {
            _series.Tags = new HashSet<int> { TitleMatchTagId + 1 };

            Subject.ParseEventTitle(Parse("Formula1.2026.Singapore.Grand.Prix.1080p.WEB.H.264-BILLIE"), _series).Should().BeNull();
        }

        [Test]
        public void should_not_match_event_that_has_not_aired()
        {
            // Moves the whole weekend, so it stays one weekend.
            var singapore = _episodes.Where(e => e.EpisodeNumber >= 90 && e.EpisodeNumber <= 94).ToList();
            var shift = DateTime.UtcNow.AddDays(7) - singapore.Max(e => e.AirDateUtc.Value);
            singapore.ForEach(e => e.AirDateUtc += shift);

            Subject.ParseEventTitle(Parse("Formula1.2026.Singapore.Grand.Prix.1080p.WEB.H.264-BILLIE"), _series).Should().BeNull();
        }

        [TestCase("Formula1.2026.Italian.Grand.Prix.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Spanish.Grand.Prix.1080p.WEB.H.264-BILLIE")]
        [TestCase("Formula1.2026.Brazilian.Grand.Prix.1080p.WEB.H.264-BILLIE")]
        public void should_not_take_language_from_event_name(string title)
        {
            var parsedEpisodeInfo = Parse(title);
            parsedEpisodeInfo.Languages.Should().NotContain(Language.Unknown);

            Subject.ParseEventTitle(parsedEpisodeInfo, _series).Languages.Should().Equal(Language.Unknown);
        }

        [Test]
        public void should_keep_event_language_the_release_names_again()
        {
            Subject.ParseEventTitle(Parse("Formula1.2026.Italian.Grand.Prix.ITALIAN.1080p.WEB.H.264-BILLIE"), _series)
                   .Languages.Should().Equal(Language.Italian);
        }

        [Test]
        public void should_keep_language_named_by_release()
        {
            Subject.ParseEventTitle(Parse("Formula1.2026.Round17.Singapore.Race.F1LIVE.F1TV.WEB-DL.1080p.H264.English-MWR"), _series)
                   .Languages.Should().Equal(Language.English);
        }

        private static ParsedEpisodeInfo Parse(string title)
        {
            return Parser.Parser.ParseTitle(title) ?? new ParsedEpisodeInfo { ReleaseTitle = title, Languages = [Language.Unknown] };
        }
    }
}
