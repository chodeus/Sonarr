using System;
using NLog;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.Parser.Model;

namespace NzbDrone.Core.Download
{
    public interface IDuplicateReleaseCache
    {
        void Remember(ReleaseInfo release, string infoHash, int downloadClientId, string downloadClient);
        string Find(ReleaseInfo release);
    }

    public class DuplicateReleaseCache : IDuplicateReleaseCache
    {
        // HistorySpecification's window for a recent grab
        private static readonly TimeSpan Lifetime = TimeSpan.FromHours(12);

        private readonly IProvideDownloadClient _downloadClientProvider;
        private readonly ICached<DuplicateRelease> _cache;
        private readonly Logger _logger;

        public DuplicateReleaseCache(IProvideDownloadClient downloadClientProvider, ICacheManager cacheManager, Logger logger)
        {
            _downloadClientProvider = downloadClientProvider;
            _cache = cacheManager.GetCache<DuplicateRelease>(GetType());
            _logger = logger;
        }

        public void Remember(ReleaseInfo release, string infoHash, int downloadClientId, string downloadClient)
        {
            var duplicate = new DuplicateRelease(downloadClientId, downloadClient, infoHash);

            if (infoHash.IsNotNullOrWhiteSpace())
            {
                _cache.Set(HashKey(infoHash), duplicate, Lifetime);
            }

            var listingKey = ListingKey(release);

            if (listingKey != null)
            {
                _cache.Set(listingKey, duplicate, Lifetime);
            }
        }

        // A known hash decides; otherwise only the same listing matches, never another one sharing its title
        public string Find(ReleaseInfo release)
        {
            var key = release is TorrentInfo torrentInfo && torrentInfo.InfoHash.IsNotNullOrWhiteSpace()
                ? HashKey(torrentInfo.InfoHash)
                : ListingKey(release);

            var duplicate = key == null ? null : _cache.Find(key);

            if (duplicate == null)
            {
                return null;
            }

            if (IsStillHeld(duplicate))
            {
                return duplicate.DownloadClient;
            }

            _cache.Remove(key);

            return null;
        }

        // The torrent may have been deleted since it was refused, and a check that can't be made lets the grab through
        private bool IsStillHeld(DuplicateRelease duplicate)
        {
            try
            {
                return duplicate.InfoHash.IsNotNullOrWhiteSpace() &&
                       _downloadClientProvider.Get(duplicate.DownloadClientId) is ITorrentPresenceCheck client &&
                       client.HasTorrent(duplicate.InfoHash);
            }
            catch (Exception ex)
            {
                _logger.Debug(ex, "Unable to check whether {0} still has {1}", duplicate.DownloadClient, duplicate.InfoHash);

                return false;
            }
        }

        private static string HashKey(string infoHash)
        {
            return $"hash:{infoHash.ToLowerInvariant()}";
        }

        private static string ListingKey(ReleaseInfo release)
        {
            return release.Guid.IsNullOrWhiteSpace() ? null : $"guid:{release.DownloadProtocol}:{release.IndexerId}:{release.Guid}";
        }

        private class DuplicateRelease
        {
            public DuplicateRelease(int downloadClientId, string downloadClient, string infoHash)
            {
                DownloadClientId = downloadClientId;
                DownloadClient = downloadClient;
                InfoHash = infoHash;
            }

            public int DownloadClientId { get; }
            public string DownloadClient { get; }
            public string InfoHash { get; }
        }
    }
}
