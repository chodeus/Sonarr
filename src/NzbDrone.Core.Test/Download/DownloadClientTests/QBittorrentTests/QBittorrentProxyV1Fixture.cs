using System;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Http;
using NzbDrone.Core.Download.Clients;
using NzbDrone.Core.Download.Clients.QBittorrent;
using NzbDrone.Core.Test.Framework;

namespace NzbDrone.Core.Test.Download.DownloadClientTests.QBittorrentTests
{
    [TestFixture]
    public class QBittorrentProxyV1Fixture : CoreTest<QBittorrentProxyV1>
    {
        private QBittorrentSettings _settings;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ICacheManager>(new CacheManager());

            // No username skips the login round trip
            _settings = new QBittorrentSettings { Host = "127.0.0.1", Port = 8080 };
        }

        private void GivenReply(string content)
        {
            Mocker.GetMock<IHttpClient>()
                  .Setup(s => s.Execute(It.IsAny<HttpRequest>()))
                  .Returns<HttpRequest>(r => new HttpResponse(r, new HttpHeader(), content));
        }

        // API v1's torrent check can't tell a missing torrent apart, so a "Fails." reply must stay a plain failure there
        [Test]
        public void adding_by_url_should_keep_a_fails_reply_a_plain_failure()
        {
            GivenReply("Fails.");

            Assert.Throws<DownloadClientException>(() => Subject.AddTorrentFromUrl("magnet:?xt=urn:btih:ZPBPA2P6ROZPKRHK44D5OW6NHXU5Z6KR&tr=udp://abc", null, _settings));
        }

        [Test]
        public void adding_a_file_should_keep_a_fails_reply_a_plain_failure()
        {
            GivenReply("Fails.");

            Assert.Throws<DownloadClientException>(() => Subject.AddTorrentFromFile("Series.Title.S01E01.torrent", new byte[] { 1, 2, 3 }, null, _settings));
        }

        [TestCase("Ok.")]
        [TestCase("")]
        public void adding_should_accept_any_other_reply(string content)
        {
            GivenReply(content);

            Action add = () => Subject.AddTorrentFromFile("Series.Title.S01E01.torrent", new byte[] { 1, 2, 3 }, null, _settings);

            add.Should().NotThrow();
        }
    }
}
