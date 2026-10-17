using System;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.Indexers;
using NzbDrone.Core.Parser.Model;

namespace NzbDrone.Core.Download
{
    public interface IDuplicateReleaseCache
    {
        void Remember(ReleaseInfo release, string downloadClient);
        string Find(ReleaseInfo release);
    }

    public class DuplicateReleaseCache : IDuplicateReleaseCache
    {
        // HistorySpecification's window for a recent grab
        private static readonly TimeSpan Lifetime = TimeSpan.FromHours(12);

        private readonly ICached<string> _cache;

        public DuplicateReleaseCache(ICacheManager cacheManager)
        {
            _cache = cacheManager.GetCache<string>(GetType());
        }

        public void Remember(ReleaseInfo release, string downloadClient)
        {
            if (release is TorrentInfo torrentInfo && torrentInfo.InfoHash.IsNotNullOrWhiteSpace())
            {
                _cache.Set(HashKey(torrentInfo.InfoHash), downloadClient, Lifetime);
            }

            var titleKey = TitleKey(release);

            if (titleKey != null)
            {
                _cache.Set(titleKey, downloadClient, Lifetime);
            }
        }

        // Matches the way ReleaseComparer.SameTorrent matches a blocklisted torrent
        public string Find(ReleaseInfo release)
        {
            if (release.DownloadProtocol != DownloadProtocol.Torrent)
            {
                return null;
            }

            if (release is TorrentInfo torrentInfo && torrentInfo.InfoHash.IsNotNullOrWhiteSpace())
            {
                return _cache.Find(HashKey(torrentInfo.InfoHash));
            }

            var titleKey = TitleKey(release);

            return titleKey == null ? null : _cache.Find(titleKey);
        }

        private static string HashKey(string infoHash)
        {
            return $"hash:{infoHash.ToLowerInvariant()}";
        }

        // By indexer name, as ReleaseComparer.HasSameIndexer: a pushed release Sonarr can't place has IndexerId 0
        private static string TitleKey(ReleaseInfo release)
        {
            return release.Indexer.IsNullOrWhiteSpace() ? null : $"title:{release.Indexer.ToLowerInvariant()}:{release.Title}";
        }
    }
}
