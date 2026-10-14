using System.Collections.Generic;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Core.Profiles.Releases;
using NzbDrone.Test.Common;
using Sonarr.Api.V3.Profiles.Release;

namespace NzbDrone.Api.Test.v3.ReleaseProfiles
{
    [TestFixture]
    public class ReleaseProfileControllerFixture : TestBase<ReleaseProfileController>
    {
        private ReleaseProfile _existing;

        [SetUp]
        public void Setup()
        {
            _existing = new ReleaseProfile
            {
                Id = 1,
                Name = "Profile",
                Enabled = true,
                Ignored = new List<string> { "testa" },
                IndexerIds = new List<int> { 3, 5 }
            };

            Mocker.GetMock<IReleaseProfileService>()
                  .Setup(s => s.Get(1))
                  .Returns(_existing);
        }

        private ReleaseProfile GivenUpdate(int indexerId)
        {
            ReleaseProfile updated = null;

            Mocker.GetMock<IReleaseProfileService>()
                  .Setup(s => s.Update(It.IsAny<ReleaseProfile>()))
                  .Callback<ReleaseProfile>(p => updated = p)
                  .Returns<ReleaseProfile>(p => p);

            var resource = _existing.ToResource();
            resource.IndexerId = indexerId;

            Subject.Update(resource);

            return updated;
        }

        [Test]
        public void should_keep_all_indexers_when_the_first_indexer_is_unchanged()
        {
            GivenUpdate(3).IndexerIds.Should().Equal(3, 5);
        }

        [Test]
        public void should_use_the_new_indexer_when_it_changes()
        {
            GivenUpdate(7).IndexerIds.Should().Equal(7);
        }

        [Test]
        public void should_use_any_indexer_when_changed_to_zero()
        {
            GivenUpdate(0).IndexerIds.Should().BeEmpty();
        }

        [Test]
        public void should_clear_a_stored_zero_indexer_when_saved_with_zero()
        {
            _existing.IndexerIds = new List<int> { 0 };

            GivenUpdate(0).IndexerIds.Should().BeEmpty();
        }
    }
}
