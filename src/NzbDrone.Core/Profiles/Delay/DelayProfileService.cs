using System;
using System.Collections.Generic;
using System.Linq;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Extensions;

namespace NzbDrone.Core.Profiles.Delay
{
    public interface IDelayProfileService
    {
        DelayProfile Add(DelayProfile profile);
        DelayProfile Update(DelayProfile profile);
        void Delete(int id);
        List<DelayProfile> All();
        DelayProfile Get(int id);
        List<DelayProfile> AllForTag(int tagId);
        List<DelayProfile> AllForTags(HashSet<int> tagIds);
        DelayProfile BestForTags(HashSet<int> tagIds);
        List<DelayProfile> Reorder(int id, int? afterId);
    }

    public class DelayProfileService : IDelayProfileService
    {
        private readonly IDelayProfileRepository _repo;
        private readonly ICached<DelayProfile> _bestForTagsCache;

        // Add, Update, Delete and Reorder each read orders, then write them back
        private readonly object _syncRoot = new object();

        public DelayProfileService(IDelayProfileRepository repo, ICacheManager cacheManager)
        {
            _repo = repo;
            _bestForTagsCache = cacheManager.GetCache<DelayProfile>(GetType(), "best");
        }

        public DelayProfile Add(DelayProfile profile)
        {
            lock (_syncRoot)
            {
                try
                {
                    var all = All();

                    if (ProfileOrder.MakeRoom(all, p => p.Order, (p, order) => p.Order = order))
                    {
                        _repo.SetFields(all, p => p.Order);
                    }

                    profile.Order = ProfileOrder.Next(all, p => p.Order);

                    return _repo.Insert(profile);
                }
                finally
                {
                    // The renumber commits even when the insert fails
                    _bestForTagsCache.Clear();
                }
            }
        }

        public DelayProfile Update(DelayProfile profile)
        {
            lock (_syncRoot)
            {
                // Order only changes through Reorder
                profile.Order = profile.Id == ProfileOrder.DefaultProfileId ? int.MaxValue : _repo.Get(profile.Id).Order;

                var result = _repo.Update(profile);
                _bestForTagsCache.Clear();
                return result;
            }
        }

        public void Delete(int id)
        {
            lock (_syncRoot)
            {
                try
                {
                    _repo.Delete(id);

                    var all = All();
                    ProfileOrder.Renumber(all, p => p.Order, (p, order) => p.Order = order);

                    // Only the order column, so a save of another profile isn't reverted
                    _repo.SetFields(all, p => p.Order);
                }
                finally
                {
                    // The delete has committed even when the renumber fails
                    _bestForTagsCache.Clear();
                }
            }
        }

        public List<DelayProfile> All()
        {
            return _repo.All().ToList();
        }

        public DelayProfile Get(int id)
        {
            return _repo.Get(id);
        }

        public List<DelayProfile> AllForTag(int tagId)
        {
            return All().Where(r => r.Tags.Contains(tagId))
                        .ToList();
        }

        public List<DelayProfile> AllForTags(HashSet<int> tagIds)
        {
            return All().Where(r => r.Tags.Intersect(tagIds).Any() || r.Tags.Empty()).ToList();
        }

        public DelayProfile BestForTags(HashSet<int> tagIds)
        {
            var key = "-" + tagIds.Select(v => v.ToString()).Join(",");

            // Under the writers' lock, so their cache clear can't land between this read and its store
            lock (_syncRoot)
            {
                return _bestForTagsCache.Get(key, () => FetchBestForTags(tagIds), TimeSpan.FromSeconds(30));
            }
        }

        private DelayProfile FetchBestForTags(HashSet<int> tagIds)
        {
            return _repo.All()
                        .Where(r => r.Tags.Intersect(tagIds).Any() || r.Tags.Empty())
                        .OrderBy(d => d.Order).First();
        }

        public List<DelayProfile> Reorder(int id, int? afterId)
        {
            lock (_syncRoot)
            {
                var all = All();

                if (ProfileOrder.Reorder(all, id, afterId, p => p.Order, (p, order) => p.Order = order))
                {
                    _repo.SetFields(all, p => p.Order);
                    _bestForTagsCache.Clear();
                }

                return All();
            }
        }
    }
}
