using System;
using System.Collections.Generic;
using System.Linq;
using NzbDrone.Core.Datastore;

namespace NzbDrone.Core.Profiles
{
    // Delay profile order: tagged profiles are numbered 1..n by priority and the default (Id 1) is always last
    public static class ProfileOrder
    {
        public const int DefaultProfileId = 1;

        public static int Next<T>(IEnumerable<T> all, Func<T, int> getOrder)
            where T : ModelBase
        {
            return all.Where(p => p.Id != DefaultProfileId).Select(getOrder).DefaultIfEmpty(0).Max() + 1;
        }

        // Older reorders could leave a tagged profile at int.MaxValue, where Next overflows or ties the default
        public static bool MakeRoom<T>(List<T> all, Func<T, int> getOrder, Action<T, int> setOrder)
            where T : ModelBase
        {
            if (!all.Any(p => p.Id != DefaultProfileId && getOrder(p) >= int.MaxValue - 1))
            {
                return false;
            }

            Renumber(all, getOrder, setOrder);

            return true;
        }

        public static bool Reorder<T>(List<T> all, int id, int? afterId, Func<T, int> getOrder, Action<T, int> setOrder)
            where T : ModelBase
        {
            var ordered = all.Where(p => p.Id != DefaultProfileId).OrderBy(getOrder).ToList();
            var moving = ordered.SingleOrDefault(p => p.Id == id);

            if (moving == null || afterId == id)
            {
                return false;
            }

            ordered.Remove(moving);

            // An unknown afterId moves the profile first, as it always has
            var insertAt = afterId == DefaultProfileId ? ordered.Count : ordered.FindIndex(p => p.Id == afterId) + 1;
            ordered.Insert(insertAt, moving);

            Number(all, ordered, setOrder);

            return true;
        }

        public static void Renumber<T>(List<T> all, Func<T, int> getOrder, Action<T, int> setOrder)
            where T : ModelBase
        {
            Number(all, all.Where(p => p.Id != DefaultProfileId).OrderBy(getOrder).ToList(), setOrder);
        }

        private static void Number<T>(List<T> all, List<T> ordered, Action<T, int> setOrder)
            where T : ModelBase
        {
            for (var i = 0; i < ordered.Count; i++)
            {
                setOrder(ordered[i], i + 1);
            }

            foreach (var profile in all.Where(p => p.Id == DefaultProfileId))
            {
                setOrder(profile, int.MaxValue);
            }
        }
    }
}
