using System.Collections.Generic;
using System.Linq;

namespace NzbDrone.Core.Tv
{
    public static class RemoteEpisodeMatcher
    {
        public static List<Episode> Distinct(Series series, IEnumerable<Episode> remoteEpisodes)
        {
            var distinct = remoteEpisodes.DistinctBy(m => new { m.SeasonNumber, m.EpisodeNumber }).ToList();

            if (series.SeriesType != SeriesTypes.Anime)
            {
                return distinct;
            }

            // Return all episodes with no abs number, but distinct for those with abs number
            return distinct.Where(e => e.AbsoluteEpisodeNumber.HasValue)
                           .OrderByDescending(e => e.SeasonNumber)
                           .DistinctBy(e => e.AbsoluteEpisodeNumber.Value)
                           .Concat(distinct.Where(e => !e.AbsoluteEpisodeNumber.HasValue))
                           .ToList();
        }

        // Keyed by remote episode. TVDB IDs match first, so an episode moved to another season and number keeps its record and file.
        public static Dictionary<Episode, Episode> Match(IEnumerable<Episode> remoteEpisodes, IEnumerable<Episode> existingEpisodes)
        {
            var remote = remoteEpisodes.ToList();
            var unmatched = existingEpisodes.ToList();
            var matches = new Dictionary<Episode, Episode>(ReferenceEqualityComparer.Instance);

            var existingByTvdbId = unmatched.Where(e => e.TvdbId > 0)
                                            .GroupBy(e => e.TvdbId)
                                            .ToDictionary(g => g.Key, g => g.First());

            foreach (var episode in remote.Where(e => e.TvdbId > 0))
            {
                if (existingByTvdbId.Remove(episode.TvdbId, out var existing))
                {
                    matches.Add(episode, existing);
                    unmatched.Remove(existing);
                }
            }

            foreach (var episode in remote.Where(e => !matches.ContainsKey(e)))
            {
                var existing = unmatched.FirstOrDefault(e => e.SeasonNumber == episode.SeasonNumber && e.EpisodeNumber == episode.EpisodeNumber);

                if (existing != null)
                {
                    matches.Add(episode, existing);
                    unmatched.Remove(existing);
                }
            }

            return matches;
        }

        public static bool IsMatchedByTvdbId(Episode remoteEpisode, Episode existingEpisode)
        {
            return existingEpisode.TvdbId > 0 && existingEpisode.TvdbId == remoteEpisode.TvdbId;
        }

        public static void Apply(Episode episodeToUpdate, Episode episode)
        {
            episodeToUpdate.TvdbId = episode.TvdbId;
            episodeToUpdate.EpisodeNumber = episode.EpisodeNumber;
            episodeToUpdate.SeasonNumber = episode.SeasonNumber;
            episodeToUpdate.AbsoluteEpisodeNumber = episode.AbsoluteEpisodeNumber;
            episodeToUpdate.AiredAfterSeasonNumber = episode.AiredAfterSeasonNumber;
            episodeToUpdate.AiredBeforeSeasonNumber = episode.AiredBeforeSeasonNumber;
            episodeToUpdate.AiredBeforeEpisodeNumber = episode.AiredBeforeEpisodeNumber;
            episodeToUpdate.Title = episode.Title ?? "TBA";
            episodeToUpdate.Overview = episode.Overview;
            episodeToUpdate.AirDate = episode.AirDate;
            episodeToUpdate.AirDateUtc = episode.AirDateUtc;
            episodeToUpdate.Runtime = episode.Runtime;
            episodeToUpdate.FinaleType = episode.FinaleType;
            episodeToUpdate.Ratings = episode.Ratings;
            episodeToUpdate.Images = episode.Images;
        }
    }
}
