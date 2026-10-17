using System;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Extensions;
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

            var listingKey = ListingKey(release);

            if (listingKey != null)
            {
                _cache.Set(listingKey, downloadClient, Lifetime);
            }
        }

        // A known hash decides; otherwise only the same listing matches, never another one sharing its title
        public string Find(ReleaseInfo release)
        {
            if (release is TorrentInfo torrentInfo && torrentInfo.InfoHash.IsNotNullOrWhiteSpace())
            {
                return _cache.Find(HashKey(torrentInfo.InfoHash));
            }

            var listingKey = ListingKey(release);

            return listingKey == null ? null : _cache.Find(listingKey);
        }

        private static string HashKey(string infoHash)
        {
            return $"hash:{infoHash.ToLowerInvariant()}";
        }

        private static string ListingKey(ReleaseInfo release)
        {
            return release.Guid.IsNullOrWhiteSpace() ? null : $"guid:{release.Guid}";
        }
    }
}
