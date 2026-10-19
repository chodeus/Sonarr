using System;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Http;
using NzbDrone.Core.Download.Clients.QBittorrent;
using NzbDrone.Core.Test.Framework;

namespace NzbDrone.Core.Test.Download.DownloadClientTests.QBittorrentTests
{
    [TestFixture]
    public class QBittorrentProxyV2Fixture : CoreTest<QBittorrentProxyV2>
    {
        private QBittorrentSettings _settings;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ICacheManager>(new CacheManager());

            // An API key skips the login round trip
            _settings = new QBittorrentSettings { Host = "127.0.0.1", Port = 8080, ApiKey = "test-api-key" };
        }

        private void GivenReply(string content)
        {
            // An add asks for the API version first
            Mocker.GetMock<IHttpClient>()
                  .Setup(s => s.Execute(It.IsAny<HttpRequest>()))
                  .Returns<HttpRequest>(r => new HttpResponse(r, new HttpHeader(), r.Url.Path.EndsWith("/app/webapiVersion") ? "2.8.3" : content));
        }

        [Test]
        public void adding_by_url_should_report_a_fails_reply_as_a_failed_add()
        {
            GivenReply("Fails.");

            Assert.Throws<QBittorrentAddFailedException>(() => Subject.AddTorrentFromUrl("magnet:?xt=urn:btih:ZPBPA2P6ROZPKRHK44D5OW6NHXU5Z6KR&tr=udp://abc", null, _settings));
        }

        [Test]
        public void adding_a_file_should_report_a_fails_reply_as_a_failed_add()
        {
            GivenReply("Fails.");

            Assert.Throws<QBittorrentAddFailedException>(() => Subject.AddTorrentFromFile("Series.Title.S01E01.torrent", new byte[] { 1, 2, 3 }, null, _settings));
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
