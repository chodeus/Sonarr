using System;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Core.Download;
using NzbDrone.Core.Download.Clients;
using NzbDrone.Core.Download.Clients.QBittorrent;

namespace NzbDrone.Core.Test.Download.DownloadClientTests.QBittorrentTests
{
    [TestFixture]
    public class QBittorrentRenameFixture : DownloadClientFixtureBase<QBittorrent>
    {
        private const string Hash = "CBC2F069FE8BB2F544EAE707D75BCD3DE9DCF951";
        private const string GrabbedTitle = "Droned.S01E01.Pilot.1080p.AMZN.WEB-DL-DRONE";

        private DownloadClientItem _downloadItem;

        [SetUp]
        public void Setup()
        {
            Subject.Definition = new DownloadClientDefinition();
            Subject.Definition.Settings = new QBittorrentSettings
            {
                Host = "127.0.0.1",
                Port = 2222,
                Username = "admin",
                Password = "pass",
                TvCategory = "tv"
            };

            Mocker.GetMock<IQBittorrentProxySelector>()
                  .Setup(s => s.GetProxy(It.IsAny<QBittorrentSettings>(), It.IsAny<bool>()))
                  .Returns(Mocker.GetMock<IQBittorrentProxy>().Object);

            GivenApiVersion(new Version(2, 11));

            _downloadItem = new DownloadClientItem { DownloadId = Hash, Title = _title };
        }

        private void GivenRenameToReleaseTitle()
        {
            Subject.Definition.Settings.As<QBittorrentSettings>().RenameToReleaseTitle = true;
        }

        private void GivenApiVersion(Version version)
        {
            Mocker.GetMock<IQBittorrentProxySelector>()
                  .Setup(s => s.GetApiVersion(It.IsAny<QBittorrentSettings>(), It.IsAny<bool>()))
                  .Returns(version);
        }

        [Test]
        public void should_not_rename_torrent_on_api_v1()
        {
            GivenRenameToReleaseTitle();
            GivenApiVersion(new Version(1, 2));

            Subject.RenameItem(_downloadItem, GrabbedTitle);

            Mocker.GetMock<IQBittorrentProxy>()
                  .Verify(v => v.RenameTorrent(It.IsAny<string>(), It.IsAny<string>(), It.IsAny<QBittorrentSettings>()), Times.Never());
        }

        [Test]
        public void should_rename_torrent_when_rename_to_release_title_is_enabled()
        {
            GivenRenameToReleaseTitle();

            Subject.RenameItem(_downloadItem, GrabbedTitle);

            Mocker.GetMock<IQBittorrentProxy>()
                  .Verify(v => v.RenameTorrent(Hash.ToLowerInvariant(), GrabbedTitle, It.IsAny<QBittorrentSettings>()), Times.Once());
        }

        [Test]
        public void should_not_rename_torrent_when_rename_to_release_title_is_disabled()
        {
            Subject.RenameItem(_downloadItem, GrabbedTitle);

            Mocker.GetMock<IQBittorrentProxy>()
                  .Verify(v => v.RenameTorrent(It.IsAny<string>(), It.IsAny<string>(), It.IsAny<QBittorrentSettings>()), Times.Never());
        }

        [Test]
        public void should_leave_a_rejected_rename_to_the_caller()
        {
            GivenRenameToReleaseTitle();

            Mocker.GetMock<IQBittorrentProxy>()
                  .Setup(v => v.RenameTorrent(It.IsAny<string>(), It.IsAny<string>(), It.IsAny<QBittorrentSettings>()))
                  .Throws(new DownloadClientException("Incorrect torrent name"));

            Assert.Throws<DownloadClientException>(() => Subject.RenameItem(_downloadItem, GrabbedTitle));
        }
    }
}
