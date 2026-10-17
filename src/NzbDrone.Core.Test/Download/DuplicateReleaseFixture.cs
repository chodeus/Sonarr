using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Cache;
using NzbDrone.Core.DecisionEngine;
using NzbDrone.Core.DecisionEngine.Specifications;
using NzbDrone.Core.Download;
using NzbDrone.Core.Exceptions;
using NzbDrone.Core.Indexers;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.Test.Download
{
    // Real DownloadService, cache and specification; only the download client is mocked
    [TestFixture]
    public class DuplicateReleaseFixture : CoreTest<DownloadService>
    {
        private const string Title = "Series.Title.S10.REPACK.1080p.WEB-DL.DDP2.0.H.264-GROUP";
        private const string Hash = "CBC2F069FE8BB2F544EAE707D75BCD3DE9DCF951";

        private Mock<IDownloadClient> _downloadClient;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ICacheManager>(new CacheManager());
            Mocker.SetConstant<IDuplicateReleaseCache>(Mocker.Resolve<DuplicateReleaseCache>());

            _downloadClient = new Mock<IDownloadClient>();
            _downloadClient.SetupGet(c => c.Definition).Returns(new DownloadClientDefinition { Id = 1, Name = "qBittorrent" });
            _downloadClient.SetupGet(c => c.Protocol).Returns(DownloadProtocol.Torrent);

            Mocker.GetMock<IProvideDownloadClient>()
                  .Setup(p => p.GetDownloadClients(It.IsAny<DownloadProtocol>(), It.IsAny<int>(), It.IsAny<bool>(), It.IsAny<HashSet<int>>()))
                  .Returns(new List<IDownloadClient> { _downloadClient.Object });
        }

        private static RemoteEpisode RemoteEpisode(int indexerId, string infoHash, string guid = null)
        {
            return new RemoteEpisode
            {
                Series = new Series { Id = 5, Tags = new HashSet<int>() },
                Episodes = new List<Episode> { new Episode { Id = 12 } },
                ParsedEpisodeInfo = new ParsedEpisodeInfo(),
                Release = new TorrentInfo
                {
                    Title = Title,
                    Guid = guid ?? $"indexer{indexerId}-listing1",
                    Indexer = $"Indexer {indexerId}",
                    IndexerId = indexerId,
                    InfoHash = infoHash,
                    DownloadUrl = $"https://indexer{indexerId}.test/download/1",
                    DownloadProtocol = DownloadProtocol.Torrent
                }
            };
        }

        private async Task GivenRefusedByTheClient(Exception refusal, RemoteEpisode grabbed = null)
        {
            grabbed ??= RemoteEpisode(7, null);

            _downloadClient.Setup(c => c.Download(It.IsAny<RemoteEpisode>(), It.IsAny<IIndexer>()))
                           .Callback<RemoteEpisode, IIndexer>((r, i) => ((TorrentInfo)r.Release).InfoHash = Hash)
                           .ThrowsAsync(refusal);

            await FluentActions.Awaiting(() => Subject.DownloadReport(grabbed, null)).Should().ThrowAsync<DownloadClientRejectedReleaseException>();
        }

        private Task GivenTheClientAlreadyHasIt()
        {
            return GivenRefusedByTheClient(new ReleaseAlreadyInDownloadClientException(RemoteEpisode(7, null).Release, "qBittorrent already has this torrent", null));
        }

        private DownloadSpecDecision Decide(RemoteEpisode remoteEpisode)
        {
            return Mocker.Resolve<AlreadyInDownloadClientSpecification>().IsSatisfiedBy(remoteEpisode, new ReleaseDecisionInformation(false, null));
        }

        [Test]
        public async Task should_reject_the_same_listing_on_a_later_sync()
        {
            await GivenTheClientAlreadyHasIt();

            var decision = Decide(RemoteEpisode(7, null));

            decision.Accepted.Should().BeFalse();
            decision.Reason.Should().Be(DownloadRejectionReason.AlreadyInDownloadClient);
            decision.Message.Should().Be("qBittorrent already has this release");
        }

        [Test]
        public async Task should_reject_a_listing_with_the_same_hash()
        {
            await GivenTheClientAlreadyHasIt();

            Decide(RemoteEpisode(9, Hash.ToLowerInvariant())).Accepted.Should().BeFalse();
        }

        [Test]
        public async Task should_accept_the_same_title_from_another_indexer()
        {
            await GivenTheClientAlreadyHasIt();

            Decide(RemoteEpisode(9, null)).Accepted.Should().BeTrue();
        }

        [Test]
        public async Task should_accept_the_same_title_and_indexer_with_another_hash()
        {
            await GivenTheClientAlreadyHasIt();

            Decide(RemoteEpisode(7, "0000000000000000000000000000000000000000")).Accepted.Should().BeTrue();
        }

        [Test]
        public async Task should_accept_another_listing_with_the_same_title_on_the_same_indexer()
        {
            await GivenTheClientAlreadyHasIt();

            Decide(RemoteEpisode(7, null, "indexer7-listing2")).Accepted.Should().BeTrue();
        }

        [Test]
        public async Task should_tell_pushed_listings_apart_by_guid()
        {
            var pushed = RemoteEpisode(0, null, "PUSH-https://tracker-a.test/download/1");
            await GivenRefusedByTheClient(new ReleaseAlreadyInDownloadClientException(pushed.Release, "qBittorrent already has this torrent", null), pushed);

            Decide(RemoteEpisode(0, null, "PUSH-https://tracker-a.test/download/1")).Accepted.Should().BeFalse();
            Decide(RemoteEpisode(0, null, "PUSH-https://tracker-b.test/download/1")).Accepted.Should().BeTrue();
        }

        [Test]
        public async Task should_match_a_listing_with_no_guid_by_hash_only()
        {
            var unidentified = RemoteEpisode(0, null, string.Empty);
            await GivenRefusedByTheClient(new ReleaseAlreadyInDownloadClientException(unidentified.Release, "qBittorrent already has this torrent", null), unidentified);

            Decide(RemoteEpisode(0, null, string.Empty)).Accepted.Should().BeTrue();
            Decide(RemoteEpisode(0, Hash, string.Empty)).Accepted.Should().BeFalse();
        }

        [Test]
        public async Task should_accept_a_usenet_release_with_the_same_title()
        {
            await GivenTheClientAlreadyHasIt();

            var nzb = RemoteEpisode(7, null);
            nzb.Release = new ReleaseInfo { Title = Title, Indexer = "Indexer 7", IndexerId = 7, DownloadProtocol = DownloadProtocol.Usenet, DownloadUrl = "https://indexer7.test/nzb/1" };

            Decide(nzb).Accepted.Should().BeTrue();
        }

        [Test]
        public async Task should_not_remember_any_other_rejection()
        {
            await GivenRefusedByTheClient(new DownloadClientRejectedReleaseException(RemoteEpisode(7, null).Release, "QBittorrent rejected the torrent file due to a conflict"));

            Decide(RemoteEpisode(7, null)).Accepted.Should().BeTrue();
        }

        [Test]
        public void should_accept_releases_nothing_refused()
        {
            Decide(RemoteEpisode(7, Hash)).Accepted.Should().BeTrue();
        }
    }
}
