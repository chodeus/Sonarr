namespace NzbDrone.Core.Download.Clients.QBittorrent
{
    // qBittorrent's "Fails." reply to an add, which older versions also send for a torrent they already have
    public class QBittorrentAddFailedException : DownloadClientException
    {
        public QBittorrentAddFailedException(string message)
            : base(message)
        {
        }
    }
}
