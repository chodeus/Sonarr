using System;
using System.Collections.Generic;
using System.Linq;
using FizzWare.NBuilder;
using FluentAssertions;
using NUnit.Framework;
using NzbDrone.Core.Datastore;
using NzbDrone.Core.Download;
using NzbDrone.Core.Download.History;
using NzbDrone.Core.Download.TrackedDownloads;
using NzbDrone.Core.History;
using NzbDrone.Core.Indexers;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.Parser;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;
using NzbDrone.Core.Tv.Events;

namespace NzbDrone.Core.Test.Download.TrackedDownloads
{
    [TestFixture]
    public class TrackedDownloadServiceRemapFixture : DbTest<TrackedDownloadService, Episode>
    {
        private const string DownloadId = "season-pack";

        // The torrent name drops the "(US)", so only the grab history maps it to the series
        private const string Title = "Series.Title.S04.1080p.WEB-DL-GROUP";

        private const string BlockedReason = "One or more episodes expected in this release were not imported or missing from the release";

        private Series _series;
        private List<Episode> _episodes;
        private EpisodeRepository _episodeRepository;
        private SeriesRepository _seriesRepository;
        private List<EpisodeHistory> _history;

        [SetUp]
        public void Setup()
        {
            _seriesRepository = new SeriesRepository(Mocker.Resolve<IMainDatabase>(), Mocker.Resolve<IEventAggregator>());
            _episodeRepository = new EpisodeRepository(Mocker.Resolve<IMainDatabase>(), Mocker.Resolve<IEventAggregator>(), TestLogger);
            Mocker.SetConstant<ISeriesRepository>(_seriesRepository);
            Mocker.SetConstant<IEpisodeRepository>(_episodeRepository);
            Mocker.SetConstant<IEpisodeService>(Mocker.Resolve<EpisodeService>());
            Mocker.SetConstant<ISeriesService>(Mocker.Resolve<SeriesService>());
            Mocker.SetConstant<IParsingService>(Mocker.Resolve<ParsingService>());

            _series = _seriesRepository.Insert(Builder<Series>.CreateNew()
                                                              .With(s => s.Title = "Series Title (US)")
                                                              .With(s => s.CleanTitle = "seriestitleus")
                                                              .With(s => s.TvdbId = 1)
                                                              .With(s => s.Status = SeriesStatusType.Continuing)
                                                              .BuildNew());

            var episodes = Builder<Episode>.CreateListOfSize(8)
                                           .All()
                                           .With(e => e.Id = 0)
                                           .With(e => e.SeriesId = _series.Id)
                                           .With(e => e.SeasonNumber = 4)
                                           .With(e => e.Title = "TBA")
                                           .Build()
                                           .ToList();

            for (var i = 0; i < episodes.Count; i++)
            {
                episodes[i].EpisodeNumber = i + 1;
            }

            _episodeRepository.InsertMany(episodes);
            _episodes = _episodeRepository.All().ToList();

            _history = _episodes.Select(e => new EpisodeHistory
            {
                DownloadId = DownloadId,
                SourceTitle = Title,
                SeriesId = _series.Id,
                EpisodeId = e.Id,
                EventType = EpisodeHistoryEventType.Grabbed,
                Data = new Dictionary<string, string>()
            }).ToList();

            Mocker.GetMock<IHistoryService>()
                  .Setup(s => s.FindByDownloadId(DownloadId))
                  .Returns(() => _history.ToList());
        }

        private TrackedDownload Track()
        {
            var client = new DownloadClientDefinition { Id = 1, Protocol = DownloadProtocol.Torrent };
            var item = new DownloadClientItem
            {
                Title = Title,
                DownloadId = DownloadId,
                Status = DownloadItemStatus.Completed,
                DownloadClientInfo = new DownloadClientItemClientInfo { Protocol = client.Protocol, Id = client.Id, Name = "client" }
            };

            return Subject.TrackDownload(client, item);
        }

        private TrackedDownload TrackBlocked()
        {
            var trackedDownload = Track();
            trackedDownload.State = TrackedDownloadState.ImportBlocked;
            trackedDownload.Warn(BlockedReason);

            return trackedDownload;
        }

        [Test]
        public void should_keep_the_grab_history_mapping_when_the_series_is_edited()
        {
            Track().RemoteEpisode.Series.Id.Should().Be(_series.Id);

            Subject.Handle(new SeriesEditedEvent(_series, _series));

            var remoteEpisode = Subject.Find(DownloadId).RemoteEpisode;
            remoteEpisode.Series.Id.Should().Be(_series.Id);
            remoteEpisode.Episodes.Should().HaveCount(8);
        }

        [Test]
        public void should_keep_the_series_when_a_refresh_removes_a_grabbed_episode()
        {
            TrackBlocked();

            var removed = _episodes.Last();
            _episodeRepository.Delete(removed);

            Subject.Handle(new EpisodeInfoRefreshedEvent(_series, new List<Episode>(), new List<Episode>(), new List<Episode> { removed }));

            var trackedDownload = Subject.Find(DownloadId);
            trackedDownload.RemoteEpisode.Series.Id.Should().Be(_series.Id);
            trackedDownload.RemoteEpisode.Episodes.Select(e => e.Id).Should().BeEquivalentTo(_episodes.SkipLast(1).Select(e => e.Id));
            trackedDownload.StatusMessages.SelectMany(m => m.Messages).Should().Equal(BlockedReason);
        }

        [Test]
        public void should_keep_the_mapping_and_blocked_reason_when_a_remap_fails()
        {
            TrackBlocked();

            // Fails after the title mapping is built, so applying a failed remap would show
            Mocker.GetMock<IHistoryService>()
                  .Setup(s => s.FindByDownloadId(DownloadId))
                  .Throws(new InvalidOperationException("database is locked"));

            Subject.Handle(new SeriesEditedEvent(_series, _series));

            var trackedDownload = Subject.Find(DownloadId);
            trackedDownload.RemoteEpisode.Series.Id.Should().Be(_series.Id);
            trackedDownload.StatusMessages.SelectMany(m => m.Messages).Should().Equal(BlockedReason);
        }

        [Test]
        public void should_warn_when_a_new_download_cannot_be_mapped()
        {
            Mocker.GetMock<IHistoryService>()
                  .Setup(s => s.FindByDownloadId(DownloadId))
                  .Throws(new InvalidOperationException("database is locked"));

            var trackedDownload = Track();

            trackedDownload.Status.Should().Be(TrackedDownloadStatus.Warning);
            trackedDownload.StatusMessages.SelectMany(m => m.Messages).Should().Equal("Unable to parse episodes from title");
        }

        [Test]
        public void should_unmap_an_imported_download_whose_series_was_deleted()
        {
            Mocker.GetMock<IDownloadHistoryService>()
                  .Setup(s => s.GetLatestDownloadHistoryItem(DownloadId))
                  .Returns(new DownloadHistory { DownloadId = DownloadId, SeriesId = _series.Id, EventType = DownloadHistoryEventType.DownloadImported });

            Track().RemoteEpisode.Series.Id.Should().Be(_series.Id);

            _seriesRepository.Delete(_series.Id);
            Subject.Handle(new SeriesDeletedEvent(new List<Series> { _series }, false, false));

            Subject.Find(DownloadId).RemoteEpisode.Series.Should().BeNull();
        }

        [Test]
        public void should_show_the_previous_mapping_until_a_remap_finishes()
        {
            Track();

            var readsDuringRemap = new List<Series>();

            Mocker.GetMock<IHistoryService>()
                  .Setup(s => s.FindByDownloadId(DownloadId))
                  .Callback(() => readsDuringRemap.Add(Subject.Find(DownloadId).RemoteEpisode?.Series))
                  .Returns(() => _history.ToList());

            Subject.Handle(new SeriesEditedEvent(_series, _series));

            readsDuringRemap.Should().NotBeEmpty();
            readsDuringRemap.Should().OnlyContain(s => s != null && s.Id == _series.Id);
        }
    }
}
