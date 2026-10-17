namespace NzbDrone.Core.Download
{
    public interface ITorrentPresenceCheck
    {
        // False when the client can't be asked, so a failed check never blocks a grab
        bool HasTorrent(string infoHash);
    }
}
