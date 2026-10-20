using System;
using System.Collections.Generic;
using FizzWare.NBuilder;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Cache;
using NzbDrone.Core.Download.Pending;
using NzbDrone.Core.Indexers;
using NzbDrone.Core.Jobs;
using NzbDrone.Core.Languages;
using NzbDrone.Core.Lifecycle;
using NzbDrone.Core.Parser;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Profiles.Delay;
using NzbDrone.Core.Profiles.Qualities;
using NzbDrone.Core.Qualities;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.Test.Download.Pending.PendingReleaseServiceTests
{
    // Real delay profile service over the real test database
    [TestFixture]
    public class PendingQueueDelayFixture : DbTest<PendingReleaseService, PendingRelease>
    {
        private DelayProfileRepository _delayProfiles;
        private DateTime _published;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ICacheManager>(new CacheManager());
            _delayProfiles = Mocker.Resolve<DelayProfileRepository>();
            Mocker.SetConstant<IDelayProfileRepository>(_delayProfiles);
            Mocker.SetConstant<IDelayProfileService>(Mocker.Resolve<DelayProfileService>());

            var series = Builder<Series>.CreateNew()
                                        .With(s => s.Tags = new HashSet<int> { 5 })
                                        .With(s => s.QualityProfile = new QualityProfile { Items = Qualities.QualityFixture.GetDefaultQualities() })
                                        .Build();

            var parsedEpisodeInfo = Builder<ParsedEpisodeInfo>.CreateNew()
                                                              .With(p => p.Quality = new QualityModel(Quality.HDTV720p))
                                                              .With(p => p.Languages = new List<Language> { Language.English })
                                                              .With(p => p.AirDate = null)
                                                              .Build();

            _published = DateTime.UtcNow.AddMinutes(-10);

            Mocker.GetMock<IPendingReleaseRepository>()
                  .Setup(s => s.All())
                  .Returns(new List<PendingRelease>
                  {
                      new PendingRelease
                      {
                          SeriesId = series.Id,
                          Title = "Series.Title.S01E01.720p.HDTV-GROUP",
                          ParsedEpisodeInfo = parsedEpisodeInfo,
                          Release = new ReleaseInfo { PublishDate = _published, DownloadProtocol = DownloadProtocol.Usenet },
                          Reason = PendingReleaseReason.Delay
                      }
                  });

            Mocker.GetMock<ISeriesService>()
                  .Setup(s => s.GetSeries(It.IsAny<IEnumerable<int>>()))
                  .Returns(new List<Series> { series });

            Mocker.GetMock<IParsingService>()
                  .Setup(s => s.Map(It.IsAny<ParsedEpisodeInfo>(), It.IsAny<Series>()))
                  .Returns(new RemoteEpisode { Episodes = new List<Episode> { Builder<Episode>.CreateNew().Build() } });

            Mocker.GetMock<ITaskManager>()
                  .Setup(s => s.GetNextExecution(It.IsAny<Type>()))
                  .Returns(DateTime.UtcNow);
        }

        [Test]
        public void queue_eta_should_follow_the_tagged_profile_when_the_default_is_stored_out_of_place()
        {
            var seeded = _delayProfiles.Get(1);
            seeded.Order = 0;
            _delayProfiles.SetFields(seeded, p => p.Order);
            Db.Insert(new DelayProfile { EnableUsenet = true, EnableTorrent = true, UsenetDelay = 120, Order = 1, Tags = new HashSet<int> { 5 } });

            Subject.Handle(new ApplicationStartedEvent());

            Subject.GetPendingQueue().Should().ContainSingle().Which.EstimatedCompletionTime.Should().Be(_published.AddMinutes(120));
        }
    }
}
