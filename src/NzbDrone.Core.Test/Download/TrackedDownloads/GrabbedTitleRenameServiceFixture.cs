using System;
using System.Collections.Generic;
using System.Linq;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Cache;
using NzbDrone.Core.CustomFormats;
using NzbDrone.Core.Download;
using NzbDrone.Core.Download.Clients;
using NzbDrone.Core.Download.TrackedDownloads;
using NzbDrone.Core.History;
using NzbDrone.Core.Indexers;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Profiles;
using NzbDrone.Core.Profiles.Qualities;
using NzbDrone.Core.Qualities;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;
using NzbDrone.Test.Common;

namespace NzbDrone.Core.Test.Download.TrackedDownloads
{
    [TestFixture]
    public class GrabbedTitleRenameServiceFixture : CoreTest<GrabbedTitleRenameService>
    {
        private const string GrabbedTitle = "Series.Title.S01E01.1080p.WEB-DL.DDPA5.1.H.264-Group";

        private CustomFormat _atmos;
        private Series _series;
        private Mock<IDownloadClient> _downloadClient;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ICacheManager>(Mocker.Resolve<CacheManager>());

            _atmos = new CustomFormat("Atmos") { Id = 1 };

            _series = new Series
            {
                QualityProfile = new QualityProfile
                {
                    Items = Qualities.QualityFixture.GetDefaultQualities(),
                    FormatItems = new List<ProfileFormatItem> { new() { Format = _atmos, Score = 1250 } }
                }
            };

            _downloadClient = new Mock<IDownloadClient>();

            Mocker.GetMock<IProvideDownloadClient>()
                  .Setup(s => s.Get(It.IsAny<int>()))
                  .Returns(_downloadClient.Object);

            GivenGrabbedFormats();
        }

        private void GivenGrabbedFormats(params CustomFormat[] formats)
        {
            Mocker.GetMock<ICustomFormatCalculationService>()
                  .Setup(s => s.ParseCustomFormat(It.IsAny<EpisodeHistory>(), It.IsAny<Series>()))
                  .Returns(formats.ToList());
        }

        private TrackedDownload GivenTrackedDownload(int downloadClientId = 1, string downloadId = "HASH1")
        {
            return new TrackedDownload
            {
                DownloadClient = downloadClientId,
                Protocol = DownloadProtocol.Torrent,
                State = TrackedDownloadState.Downloading,
                IsTrackable = true,
                DownloadItem = new DownloadClientItem
                {
                    Title = "Series.Title.S01E01.1080p.WEB-DL.DDP5.1.H.264-Group",
                    DownloadId = downloadId
                },
                GrabbedEvent = new EpisodeHistory
                {
                    SourceTitle = GrabbedTitle,
                    Quality = new QualityModel(Quality.WEBDL1080p)
                },
                RemoteEpisode = new RemoteEpisode
                {
                    Series = _series,
                    ParsedEpisodeInfo = new ParsedEpisodeInfo { Quality = new QualityModel(Quality.WEBDL1080p) },
                    CustomFormats = new List<CustomFormat>()
                }
            };
        }

        private void Refresh(params TrackedDownload[] trackedDownloads)
        {
            Subject.HandleAsync(new TrackedDownloadRefreshedEvent(trackedDownloads.ToList()));
        }

        // Release builds never fail a test on an unexpected warning, so a swallowed exception must be ruled out here
        private void VerifyRenamed(Times times)
        {
            _downloadClient.Verify(c => c.RenameItem(It.IsAny<DownloadClientItem>(), It.IsAny<string>()), times);
            ExceptionVerification.ExpectedWarns(0);
        }

        [Test]
        public void should_rename_when_the_grabbed_title_has_a_higher_custom_format_score()
        {
            GivenGrabbedFormats(_atmos);

            Refresh(GivenTrackedDownload());

            _downloadClient.Verify(c => c.RenameItem(It.Is<DownloadClientItem>(i => i.DownloadId == "HASH1"), GrabbedTitle), Times.Once());
            ExceptionVerification.ExpectedWarns(0);
        }

        [Test]
        public void should_not_rename_when_both_titles_score_the_same()
        {
            Refresh(GivenTrackedDownload());

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_not_rename_when_the_download_title_scores_higher()
        {
            var trackedDownload = GivenTrackedDownload();
            trackedDownload.RemoteEpisode.CustomFormats = new List<CustomFormat> { _atmos };

            Refresh(trackedDownload);

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_rename_when_the_grabbed_title_has_a_higher_quality()
        {
            var trackedDownload = GivenTrackedDownload();
            trackedDownload.RemoteEpisode.ParsedEpisodeInfo.Quality = new QualityModel(Quality.HDTV1080p);

            Refresh(trackedDownload);

            VerifyRenamed(Times.Once());
        }

        [Test]
        public void should_not_rename_when_the_grabbed_title_has_a_lower_quality_but_a_higher_score()
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();
            trackedDownload.GrabbedEvent.Quality = new QualityModel(Quality.HDTV1080p);

            Refresh(trackedDownload);

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_rename_when_the_download_is_blocked_from_importing()
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();
            trackedDownload.State = TrackedDownloadState.ImportBlocked;

            Refresh(trackedDownload);

            VerifyRenamed(Times.Once());
        }

        [TestCase(TrackedDownloadState.ImportPending)]
        [TestCase(TrackedDownloadState.Importing)]
        [TestCase(TrackedDownloadState.FailedPending)]
        public void should_not_rename_once_the_download_has_moved_on(TrackedDownloadState state)
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();
            trackedDownload.State = state;

            Refresh(trackedDownload);

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_not_rename_usenet_downloads()
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();
            trackedDownload.Protocol = DownloadProtocol.Usenet;

            Refresh(trackedDownload);

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_not_rename_downloads_from_a_client_that_could_not_be_reached()
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();
            trackedDownload.IsTrackable = false;

            Refresh(trackedDownload);

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_not_rename_without_grab_history()
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();
            trackedDownload.GrabbedEvent = null;

            Refresh(trackedDownload);

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_not_rename_when_the_grab_matched_the_series_by_id()
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();
            trackedDownload.GrabbedEvent.Data.Add(EpisodeHistory.SERIES_MATCH_TYPE, nameof(SeriesMatchType.Id));

            Refresh(trackedDownload);

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_rename_when_the_grab_matched_the_series_by_title()
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();
            trackedDownload.GrabbedEvent.Data.Add(EpisodeHistory.SERIES_MATCH_TYPE, nameof(SeriesMatchType.Title));

            Refresh(trackedDownload);

            VerifyRenamed(Times.Once());
        }

        [TestCase("remoteEpisode")]
        [TestCase("series")]
        [TestCase("parsedEpisodeInfo")]
        [TestCase("customFormats")]
        public void should_not_rename_a_download_that_is_not_fully_mapped(string missing)
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();

            switch (missing)
            {
                case "remoteEpisode":
                    trackedDownload.RemoteEpisode = null;
                    break;
                case "series":
                    trackedDownload.RemoteEpisode.Series = null;
                    break;
                case "parsedEpisodeInfo":
                    trackedDownload.RemoteEpisode.ParsedEpisodeInfo = null;
                    break;
                case "customFormats":
                    trackedDownload.RemoteEpisode.CustomFormats = null;
                    break;
            }

            Refresh(trackedDownload);

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_not_rename_when_the_names_already_match()
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();
            trackedDownload.DownloadItem.Title = "Series Title S01E01 1080p WEB-DL DDPA5 1 H 264-Group";

            Refresh(trackedDownload);

            VerifyRenamed(Times.Never());
        }

        [Test]
        public void should_rename_once_while_the_event_is_republished_with_the_old_name()
        {
            GivenGrabbedFormats(_atmos);

            var trackedDownload = GivenTrackedDownload();

            Refresh(trackedDownload);
            Refresh(trackedDownload);

            VerifyRenamed(Times.Once());
        }

        [Test]
        public void should_wait_before_retrying_a_failed_rename()
        {
            GivenGrabbedFormats(_atmos);

            _downloadClient.Setup(c => c.RenameItem(It.IsAny<DownloadClientItem>(), It.IsAny<string>()))
                           .Throws(new DownloadClientException("Incorrect torrent name"));

            var trackedDownload = GivenTrackedDownload();

            Refresh(trackedDownload);
            Refresh(trackedDownload);

            _downloadClient.Verify(c => c.RenameItem(It.IsAny<DownloadClientItem>(), It.IsAny<string>()), Times.Once());
            ExceptionVerification.ExpectedWarns(1);
        }

        [Test]
        public void should_keep_checking_other_downloads_when_one_fails()
        {
            GivenGrabbedFormats(_atmos);

            Mocker.GetMock<IProvideDownloadClient>()
                  .Setup(s => s.Get(1))
                  .Throws(new InvalidOperationException("Download client is gone"));

            Refresh(GivenTrackedDownload(1, "HASH1"), GivenTrackedDownload(2, "HASH2"));

            _downloadClient.Verify(c => c.RenameItem(It.Is<DownloadClientItem>(i => i.DownloadId == "HASH2"), GrabbedTitle), Times.Once());
            ExceptionVerification.ExpectedWarns(1);
        }
    }
}
