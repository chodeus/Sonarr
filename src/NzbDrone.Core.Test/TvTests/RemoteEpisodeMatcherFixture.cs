using System.Collections.Generic;
using FluentAssertions;
using NUnit.Framework;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.Test.TvTests
{
    [TestFixture]
    public class RemoteEpisodeMatcherFixture : CoreTest
    {
        private static Episode Episode(int tvdbId, int seasonNumber, int episodeNumber, int id = 0)
        {
            return new Episode { Id = id, TvdbId = tvdbId, SeasonNumber = seasonNumber, EpisodeNumber = episodeNumber };
        }

        [Test]
        public void should_match_by_tvdb_id_when_episode_moved_to_another_season()
        {
            var existing = Episode(100, 1, 5, 1);
            var remoteMoved = Episode(100, 2, 1);
            var remoteNew = Episode(102, 1, 5);

            var matches = RemoteEpisodeMatcher.Match(new List<Episode> { remoteNew, remoteMoved }, new List<Episode> { existing });

            matches.Should().HaveCount(1);
            matches[remoteMoved].Should().BeSameAs(existing);
            matches.Should().NotContainKey(remoteNew);
        }

        [Test]
        public void should_swap_records_when_two_episodes_swap_numbers()
        {
            var first = Episode(100, 1, 5, 1);
            var second = Episode(101, 1, 6, 2);
            var remoteFirst = Episode(100, 1, 6);
            var remoteSecond = Episode(101, 1, 5);

            var matches = RemoteEpisodeMatcher.Match(new List<Episode> { remoteSecond, remoteFirst }, new List<Episode> { first, second });

            matches[remoteFirst].Should().BeSameAs(first);
            matches[remoteSecond].Should().BeSameAs(second);
        }

        [Test]
        public void should_match_by_season_and_episode_when_existing_episode_has_no_tvdb_id()
        {
            var existing = Episode(0, 1, 5, 1);
            var remote = Episode(100, 1, 5);

            var matches = RemoteEpisodeMatcher.Match(new List<Episode> { remote }, new List<Episode> { existing });

            matches[remote].Should().BeSameAs(existing);
            RemoteEpisodeMatcher.IsMatchedByTvdbId(remote, existing).Should().BeFalse();
        }

        [Test]
        public void should_match_by_season_and_episode_when_remote_episode_has_no_tvdb_id()
        {
            var existing = Episode(100, 1, 5, 1);
            var remote = Episode(0, 1, 5);

            var matches = RemoteEpisodeMatcher.Match(new List<Episode> { remote }, new List<Episode> { existing });

            matches[remote].Should().BeSameAs(existing);
            RemoteEpisodeMatcher.IsMatchedByTvdbId(remote, existing).Should().BeFalse();
        }

        [Test]
        public void should_match_by_season_and_episode_when_tvdb_replaced_the_episode()
        {
            var existing = Episode(100, 1, 5, 1);
            var remote = Episode(200, 1, 5);

            var matches = RemoteEpisodeMatcher.Match(new List<Episode> { remote }, new List<Episode> { existing });

            matches[remote].Should().BeSameAs(existing);
            RemoteEpisodeMatcher.IsMatchedByTvdbId(remote, existing).Should().BeFalse();
        }

        [Test]
        public void should_not_take_a_slot_from_an_episode_matched_by_tvdb_id()
        {
            var moved = Episode(100, 1, 5, 1);
            var remoteMoved = Episode(100, 1, 6);
            var remoteAtOldSlot = Episode(0, 1, 5);

            var matches = RemoteEpisodeMatcher.Match(new List<Episode> { remoteAtOldSlot, remoteMoved }, new List<Episode> { moved });

            matches[remoteMoved].Should().BeSameAs(moved);
            matches.Should().NotContainKey(remoteAtOldSlot);
        }

        [Test]
        public void should_match_only_one_existing_episode_with_a_duplicated_tvdb_id()
        {
            var first = Episode(100, 1, 5, 1);
            var duplicate = Episode(100, 1, 5, 2);
            var remote = Episode(100, 1, 5);

            var matches = RemoteEpisodeMatcher.Match(new List<Episode> { remote }, new List<Episode> { first, duplicate });

            matches.Should().HaveCount(1);
            matches[remote].Should().BeSameAs(first);
        }
    }
}
