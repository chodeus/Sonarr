using System.Collections.Generic;
using System.Linq;
using FluentAssertions;
using NUnit.Framework;
using NzbDrone.Core.Profiles;
using NzbDrone.Core.Profiles.Delay;
using NzbDrone.Core.Test.Framework;

namespace NzbDrone.Core.Test.Profiles
{
    [TestFixture]
    public class ProfileOrderFixture : CoreTest
    {
        private List<DelayProfile> _profiles;

        [SetUp]
        public void Setup()
        {
            _profiles = new List<DelayProfile>
            {
                new DelayProfile { Id = 1, Order = int.MaxValue },
                new DelayProfile { Id = 2, Order = 1 },
                new DelayProfile { Id = 3, Order = 2 },
                new DelayProfile { Id = 4, Order = 3 }
            };
        }

        private bool Reorder(int id, int? afterId)
        {
            return ProfileOrder.Reorder(_profiles, id, afterId, p => p.Order, (p, order) => p.Order = order);
        }

        private IEnumerable<int> IdsInOrder()
        {
            return _profiles.OrderBy(p => p.Order).Select(p => p.Id);
        }

        [Test]
        public void should_move_first_when_after_id_is_null()
        {
            Reorder(4, null).Should().BeTrue();

            IdsInOrder().Should().Equal(4, 2, 3, 1);
        }

        [Test]
        public void should_move_up_to_directly_after_another_profile()
        {
            Reorder(4, 2);

            IdsInOrder().Should().Equal(2, 4, 3, 1);
        }

        [Test]
        public void should_move_down_to_directly_after_another_profile()
        {
            Reorder(2, 4);

            IdsInOrder().Should().Equal(3, 4, 2, 1);
        }

        [Test]
        public void should_move_last_when_after_the_default()
        {
            Reorder(2, 1);

            IdsInOrder().Should().Equal(3, 4, 2, 1);
            _profiles.Single(p => p.Id == 2).Order.Should().Be(3);
        }

        [Test]
        public void should_move_first_when_after_id_is_unknown()
        {
            Reorder(4, 99);

            IdsInOrder().Should().Equal(4, 2, 3, 1);
        }

        [TestCase(99, null)]
        [TestCase(1, null)]
        [TestCase(3, 3)]
        public void should_change_nothing_for_an_unknown_default_or_self_move(int id, int? afterId)
        {
            Reorder(id, afterId).Should().BeFalse();

            _profiles.Select(p => p.Order).Should().Equal(int.MaxValue, 1, 2, 3);
        }

        [Test]
        public void should_close_gaps_and_keep_the_default_last()
        {
            _profiles[0].Order = 0;
            _profiles[1].Order = 5;
            _profiles[2].Order = 9;
            _profiles[3].Order = 9;

            Reorder(3, null);

            _profiles.Select(p => p.Order).Should().Equal(int.MaxValue, 2, 1, 3);
        }

        [Test]
        public void renumber_should_close_gaps_and_keep_the_default_last()
        {
            _profiles.RemoveAt(2);
            _profiles[1].Order = 4;
            _profiles[2].Order = 7;

            ProfileOrder.Renumber(_profiles, p => p.Order, (p, order) => p.Order = order);

            _profiles.Select(p => p.Order).Should().Equal(int.MaxValue, 1, 2);
        }

        [Test]
        public void next_should_follow_the_highest_non_default_order()
        {
            _profiles[3].Order = 7;

            ProfileOrder.Next(_profiles, p => p.Order).Should().Be(8);
        }

        [Test]
        public void next_should_be_one_when_only_the_default_exists()
        {
            ProfileOrder.Next(_profiles.Take(1), p => p.Order).Should().Be(1);
        }

        [TestCase(int.MaxValue)]
        [TestCase(int.MaxValue - 1)]
        public void make_room_should_renumber_when_next_would_reach_the_default(int order)
        {
            _profiles[2].Order = order;

            MakeRoom().Should().BeTrue();

            _profiles.Select(p => p.Order).Should().Equal(int.MaxValue, 1, 3, 2);
            ProfileOrder.Next(_profiles, p => p.Order).Should().Be(4);
        }

        [Test]
        public void make_room_should_leave_orders_alone_below_the_default()
        {
            _profiles[2].Order = int.MaxValue - 2;

            MakeRoom().Should().BeFalse();

            _profiles.Select(p => p.Order).Should().Equal(int.MaxValue, 1, int.MaxValue - 2, 3);
        }

        private bool MakeRoom()
        {
            return ProfileOrder.MakeRoom(_profiles, p => p.Order, (p, order) => p.Order = order);
        }
    }
}
