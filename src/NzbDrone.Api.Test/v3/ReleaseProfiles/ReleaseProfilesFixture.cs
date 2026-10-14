using FluentAssertions;
using NUnit.Framework;
using NzbDrone.Common.Serializer;
using Sonarr.Api.V3.Profiles.Release;
using Sonarr.Http.REST;

namespace NzbDrone.Api.Test.v3.ReleaseProfiles
{
    [TestFixture]
    public class ReleaseProfilesFixture
    {
        [Test]
        public void should_deserialize_releaseprofile_v3_ignored_null()
        {
            var resource = STJson.Deserialize<ReleaseProfileResource>("{ \"ignored\": null, \"required\": null }");

            var model = resource.ToModel();

            model.Ignored.Should().BeEquivalentTo();
            model.Required.Should().BeEquivalentTo();
        }

        [Test]
        public void should_deserialize_releaseprofile_v3_ignored_string()
        {
            var resource = STJson.Deserialize<ReleaseProfileResource>("{ \"ignored\": \"testa,testb\", \"required\": \"testc,testd\" }");

            var model = resource.ToModel();

            model.Ignored.Should().BeEquivalentTo("testa", "testb");
            model.Required.Should().BeEquivalentTo("testc", "testd");
        }

        [Test]
        public void should_deserialize_releaseprofile_v3_ignored_string_array()
        {
            var resource = STJson.Deserialize<ReleaseProfileResource>("{ \"ignored\": [ \"testa\", \"testb\" ], \"required\": [ \"testc\", \"testd\" ] }");

            var model = resource.ToModel();

            model.Ignored.Should().BeEquivalentTo("testa", "testb");
            model.Required.Should().BeEquivalentTo("testc", "testd");
        }

        [Test]
        public void should_throw_with_bad_releaseprofile_v3_ignored_type()
        {
            var resource = STJson.Deserialize<ReleaseProfileResource>("{ \"ignored\": {} }");

            Assert.Throws<BadRequestException>(() => resource.ToModel());
        }

        [Test]
        public void should_map_indexer_zero_to_any_indexer()
        {
            var resource = STJson.Deserialize<ReleaseProfileResource>("{ \"indexerId\": 0, \"ignored\": [ \"testa\" ] }");

            resource.ToModel().IndexerIds.Should().BeEmpty();
        }

        [Test]
        public void should_map_indexer_to_that_indexer_only()
        {
            var resource = STJson.Deserialize<ReleaseProfileResource>("{ \"indexerId\": 5, \"ignored\": [ \"testa\" ] }");

            resource.ToModel().IndexerIds.Should().Equal(5);
        }
    }
}
