using System;
using System.Collections.Generic;
using System.Linq;
using System.Linq.Expressions;
using System.Threading;
using System.Threading.Tasks;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Cache;
using NzbDrone.Core.Profiles.Delay;
using NzbDrone.Core.Test.Framework;

namespace NzbDrone.Core.Test.Profiles.Delay
{
    // Real service over the real test database; the mock only forwards or injects a second writer
    [TestFixture]
    public class DelayProfileServiceDbFixture : DbTest<DelayProfileService, DelayProfile>
    {
        private DelayProfileRepository _realRepo;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ICacheManager>(new CacheManager());
            _realRepo = Mocker.Resolve<DelayProfileRepository>();
            Mocker.SetConstant<IDelayProfileRepository>(_realRepo);
        }

        private DelayProfile InsertProfile(int order, params int[] tags)
        {
            return Db.Insert(new DelayProfile { EnableUsenet = true, EnableTorrent = true, Order = order, Tags = new HashSet<int>(tags) });
        }

        private static DelayProfileService NewService(IDelayProfileRepository repo)
        {
            return new DelayProfileService(repo, new CacheManager());
        }

        private Mock<IDelayProfileRepository> Forwarding(Action<IList<DelayProfile>> beforeManyWrite = null)
        {
            var mock = new Mock<IDelayProfileRepository>();
            mock.Setup(r => r.All()).Returns(() => _realRepo.All());
            mock.Setup(r => r.Get(It.IsAny<int>())).Returns<int>(id => _realRepo.Get(id));
            mock.Setup(r => r.Insert(It.IsAny<DelayProfile>())).Returns<DelayProfile>(p => _realRepo.Insert(p));
            mock.Setup(r => r.Update(It.IsAny<DelayProfile>())).Returns<DelayProfile>(p => _realRepo.Update(p));
            mock.Setup(r => r.Delete(It.IsAny<int>())).Callback<int>(id => _realRepo.Delete(id));
            mock.Setup(r => r.UpdateMany(It.IsAny<IList<DelayProfile>>()))
                .Callback<IList<DelayProfile>>(m =>
                {
                    beforeManyWrite?.Invoke(m);
                    _realRepo.UpdateMany(m);
                });
            mock.Setup(r => r.SetFields(It.IsAny<IList<DelayProfile>>(), It.IsAny<Expression<Func<DelayProfile, object>>[]>()))
                .Callback<IList<DelayProfile>, Expression<Func<DelayProfile, object>>[]>((m, p) =>
                {
                    beforeManyWrite?.Invoke(m);
                    _realRepo.SetFields(m, p);
                });

            return mock;
        }

        private bool ToggleSeededDefaultBypass()
        {
            var seeded = _realRepo.Get(1);
            seeded.BypassIfHighestQuality = !seeded.BypassIfHighestQuality;
            NewService(_realRepo).Update(seeded);

            return seeded.BypassIfHighestQuality;
        }

        [Test]
        public void seeded_default_should_be_id_1_and_last()
        {
            _realRepo.All().Should().ContainSingle(p => p.Id == 1).Which.Order.Should().Be(int.MaxValue);
        }

        [Test]
        public void add_should_rank_below_existing_profiles_when_one_holds_the_maximum_order()
        {
            var existing = InsertProfile(1, 5);
            var legacy = InsertProfile(int.MaxValue, 6);

            var added = Subject.Add(new DelayProfile { Tags = new HashSet<int> { 5 } });

            var stored = _realRepo.All().Where(p => p.Id != 1).ToList();
            stored.Single(p => p.Id == existing.Id).Order.Should().Be(1);
            stored.Single(p => p.Id == legacy.Id).Order.Should().Be(2);
            stored.Single(p => p.Id == added.Id).Order.Should().Be(3);
            Subject.BestForTags(new HashSet<int> { 5 }).Id.Should().Be(existing.Id);
        }

        [Test]
        public void concurrent_adds_should_get_different_orders()
        {
            InsertProfile(1, 5);

            var inWindow = new ManualResetEventSlim();
            var secondDone = new ManualResetEventSlim();
            var first = true;
            var repo = Forwarding();
            repo.Setup(r => r.Insert(It.IsAny<DelayProfile>()))
                .Returns<DelayProfile>(p =>
                {
                    if (first)
                    {
                        first = false;
                        inWindow.Set();

                        // Unserialised, the second add reads the orders here; serialised, it waits for this insert
                        secondDone.Wait(TimeSpan.FromMilliseconds(500));
                    }

                    return _realRepo.Insert(p);
                });

            var subject = NewService(repo.Object);

            var add1 = Task.Run(() => subject.Add(new DelayProfile { Tags = new HashSet<int> { 6 } }));
            inWindow.Wait(TimeSpan.FromSeconds(5)).Should().BeTrue();
            var add2 = Task.Run(() =>
            {
                subject.Add(new DelayProfile { Tags = new HashSet<int> { 7 } });
                secondDone.Set();
            });

            Task.WaitAll(new[] { add1, add2 }, TimeSpan.FromSeconds(10)).Should().BeTrue();

            _realRepo.All().Where(p => p.Id != 1).Select(p => p.Order).Should().BeEquivalentTo(new[] { 1, 2, 3 });
        }

        [Test]
        public void add_should_not_rewrite_existing_orders_when_there_is_room()
        {
            InsertProfile(1, 5);
            InsertProfile(7, 6);
            var repo = Forwarding();

            var added = NewService(repo.Object).Add(new DelayProfile { Tags = new HashSet<int> { 7 } });

            added.Order.Should().Be(8);
            repo.Verify(r => r.SetFields(It.IsAny<IList<DelayProfile>>(), It.IsAny<Expression<Func<DelayProfile, object>>[]>()), Times.Never());
            repo.Verify(r => r.UpdateMany(It.IsAny<IList<DelayProfile>>()), Times.Never());
        }

        [Test]
        public void reorder_should_not_revert_a_default_profile_save_that_lands_mid_write()
        {
            var a = InsertProfile(1, 5);
            InsertProfile(2, 6);

            var saved = (bool?)null;
            var repo = Forwarding(_ => saved ??= ToggleSeededDefaultBypass());

            NewService(repo.Object).Reorder(a.Id, 1);

            saved.Should().NotBeNull();
            _realRepo.Get(1).BypassIfHighestQuality.Should().Be(saved.Value);
        }

        [Test]
        public void reorder_then_default_save_in_sequence_should_keep_both()
        {
            var a = InsertProfile(1, 5);
            var b = InsertProfile(2, 6);

            Subject.Reorder(a.Id, 1);
            var saved = ToggleSeededDefaultBypass();

            _realRepo.Get(1).BypassIfHighestQuality.Should().Be(saved);
            _realRepo.Get(a.Id).Order.Should().BeGreaterThan(_realRepo.Get(b.Id).Order);
        }

        [Test]
        public void reorder_should_drop_the_cached_best_profile()
        {
            var a = InsertProfile(1, 5);
            var b = InsertProfile(2, 5);

            Subject.BestForTags(new HashSet<int> { 5 }).Id.Should().Be(a.Id);
            Subject.Reorder(b.Id, null);

            Subject.BestForTags(new HashSet<int> { 5 }).Id.Should().Be(b.Id);
        }

        [Test]
        public void update_should_not_revert_a_reorder_from_another_request()
        {
            InsertProfile(1, 5);
            InsertProfile(2, 6);
            var c = InsertProfile(3, 7);

            var inWindow = new ManualResetEventSlim();
            var reorderDone = new ManualResetEventSlim();
            var repo = Forwarding();
            repo.Setup(r => r.Update(It.IsAny<DelayProfile>()))
                .Returns<DelayProfile>(p =>
                {
                    inWindow.Set();

                    // Unserialised, the other request's reorder lands here; serialised, it waits for this update
                    reorderDone.Wait(TimeSpan.FromMilliseconds(500));

                    return _realRepo.Update(p);
                });

            var subject = NewService(repo.Object);
            var edit = _realRepo.Get(c.Id);
            edit.UsenetDelay = 60;

            var update = Task.Run(() => subject.Update(edit));
            inWindow.Wait(TimeSpan.FromSeconds(5)).Should().BeTrue();
            var reorder = Task.Run(() =>
            {
                subject.Reorder(c.Id, null);
                reorderDone.Set();
            });

            Task.WaitAll(new[] { update, reorder }, TimeSpan.FromSeconds(10)).Should().BeTrue();

            var stored = _realRepo.All().Where(p => p.Id != 1).ToList();
            stored.Single(p => p.Id == c.Id).Order.Should().Be(1);
            stored.Single(p => p.Id == c.Id).UsenetDelay.Should().Be(60);
            stored.Select(p => p.Order).Should().OnlyHaveUniqueItems();
        }

        [Test]
        public void update_then_reorder_in_sequence_should_keep_both()
        {
            InsertProfile(1, 5);
            InsertProfile(2, 6);
            var c = InsertProfile(3, 7);

            var edit = _realRepo.Get(c.Id);
            edit.UsenetDelay = 60;
            Subject.Update(edit);
            Subject.Reorder(c.Id, null);

            var stored = _realRepo.All().Where(p => p.Id != 1).ToList();
            stored.Single(p => p.Id == c.Id).Order.Should().Be(1);
            stored.Single(p => p.Id == c.Id).UsenetDelay.Should().Be(60);
            stored.Select(p => p.Order).Should().OnlyHaveUniqueItems();
        }

        [Test]
        public void update_should_keep_the_stored_order_when_the_edit_was_loaded_before_a_reorder()
        {
            InsertProfile(1, 5);
            InsertProfile(2, 6);
            var c = InsertProfile(3, 7);

            var edit = _realRepo.Get(c.Id);
            Subject.Reorder(c.Id, null);

            edit.UsenetDelay = 60;
            Subject.Update(edit);

            var stored = _realRepo.All().Where(p => p.Id != 1).ToList();
            stored.Single(p => p.Id == c.Id).Order.Should().Be(1);
            stored.Single(p => p.Id == c.Id).UsenetDelay.Should().Be(60);
            stored.Select(p => p.Order).Should().OnlyHaveUniqueItems();
        }

        [Test]
        public void update_should_keep_the_default_last()
        {
            InsertProfile(1, 5);

            var edit = _realRepo.Get(1);
            edit.Order = 0;
            Subject.Update(edit);

            _realRepo.Get(1).Order.Should().Be(int.MaxValue);
        }

        [Test]
        public void update_should_put_a_default_stored_out_of_place_back_last()
        {
            InsertProfile(1, 5);

            // An older save could store the order the client sent for the default
            var seeded = _realRepo.Get(1);
            seeded.Order = 0;
            _realRepo.SetFields(seeded, p => p.Order);

            Subject.Update(_realRepo.Get(1));

            _realRepo.Get(1).Order.Should().Be(int.MaxValue);
        }

        [Test]
        public void delete_should_not_revert_a_reorder_from_another_request()
        {
            var a = InsertProfile(1, 5);
            var b = InsertProfile(2, 6);
            var c = InsertProfile(3, 7);

            var inWindow = new ManualResetEventSlim();
            var reorderDone = new ManualResetEventSlim();
            var first = true;
            var repo = Forwarding(_ =>
            {
                if (!first)
                {
                    return;
                }

                first = false;
                inWindow.Set();

                // Unserialised, the other request's reorder lands here; serialised, it waits for this delete
                reorderDone.Wait(TimeSpan.FromMilliseconds(500));
            });

            var subject = NewService(repo.Object);

            var delete = Task.Run(() => subject.Delete(a.Id));
            inWindow.Wait(TimeSpan.FromSeconds(5)).Should().BeTrue();
            var reorder = Task.Run(() =>
            {
                subject.Reorder(c.Id, null);
                reorderDone.Set();
            });

            Task.WaitAll(new[] { delete, reorder }, TimeSpan.FromSeconds(10)).Should().BeTrue();

            _realRepo.Get(c.Id).Order.Should().Be(1);
            _realRepo.Get(b.Id).Order.Should().Be(2);
        }

        [Test]
        public void delete_should_not_leave_the_deleted_profile_cached_when_the_renumber_fails()
        {
            var a = InsertProfile(1, 5);
            InsertProfile(2, 6);

            var subject = NewService(Forwarding(_ => throw new InvalidOperationException("database is locked")).Object);

            subject.BestForTags(new HashSet<int> { 5 }).Id.Should().Be(a.Id);

            Assert.Throws<InvalidOperationException>(() => subject.Delete(a.Id));

            _realRepo.Find(a.Id).Should().BeNull();
            subject.BestForTags(new HashSet<int> { 5 }).Id.Should().NotBe(a.Id);
        }

        [Test]
        public void delete_should_not_revert_a_default_profile_save_that_lands_mid_write()
        {
            var a = InsertProfile(1, 5);
            InsertProfile(2, 6);

            var saved = (bool?)null;
            var repo = Forwarding(_ => saved ??= ToggleSeededDefaultBypass());

            NewService(repo.Object).Delete(a.Id);

            saved.Should().NotBeNull();
            _realRepo.Get(1).BypassIfHighestQuality.Should().Be(saved.Value);
        }

        [Test]
        public void delete_should_renumber_and_drop_the_profile_from_best_for_tags()
        {
            var a = InsertProfile(1, 5);
            var b = InsertProfile(2, 6);

            Subject.BestForTags(new HashSet<int> { 5 }).Id.Should().Be(a.Id);
            Subject.Delete(a.Id);

            Subject.BestForTags(new HashSet<int> { 5 }).Id.Should().Be(1);
            _realRepo.Get(b.Id).Order.Should().Be(1);
        }

        [Test]
        public void reorder_and_delete_should_persist_dense_orders_with_the_default_last()
        {
            var a = InsertProfile(4, 5);
            var b = InsertProfile(9, 6);
            var c = InsertProfile(9, 7);

            Subject.Reorder(c.Id, 1);

            _realRepo.All().OrderBy(p => p.Order).Select(p => p.Id).Should().Equal(a.Id, b.Id, c.Id, 1);

            Subject.Delete(a.Id);

            var stored = _realRepo.All().ToList();
            stored.Single(p => p.Id == b.Id).Order.Should().Be(1);
            stored.Single(p => p.Id == c.Id).Order.Should().Be(2);
            stored.Single(p => p.Id == 1).Order.Should().Be(int.MaxValue);
        }
    }
}
