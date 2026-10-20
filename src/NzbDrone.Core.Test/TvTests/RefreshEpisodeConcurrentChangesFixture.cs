using System;
using System.Collections.Generic;
using System.Linq;
using System.Linq.Expressions;
using System.Reflection;
using System.Text.Json;
using FizzWare.NBuilder;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Cache;
using NzbDrone.Core.Datastore;
using NzbDrone.Core.MediaCover;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.Test.TvTests
{
    // Real episode service over the real test database; the mock only forwards, or lets another writer save mid-refresh
    [TestFixture]
    public class RefreshEpisodeConcurrentChangesFixture : DbTest<RefreshEpisodeService, Episode>
    {
        private EpisodeRepository _repository;
        private Series _series;
        private Episode _stored;
        private Episode _remote;
        private List<Episode> _read;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ICacheManager>(new CacheManager());
            _repository = Mocker.Resolve<EpisodeRepository>();
            Mocker.SetConstant<IEpisodeRepository>(_repository);

            _series = Builder<Series>.CreateNew()
                                     .With(s => s.Id = 5)
                                     .With(s => s.SeriesType = SeriesTypes.Standard)
                                     .With(s => s.Status = SeriesStatusType.Continuing)
                                     .With(s => s.Seasons = new List<Season> { new Season { SeasonNumber = 1, Monitored = true } })
                                     .Build();

            // Two built episodes differ in every simple column, so the remote one changes everything the refresh copies
            var episodes = Builder<Episode>.CreateListOfSize(2)
                                           .All()
                                           .With(e => e.SeriesId = _series.Id)
                                           .With(e => e.SeasonNumber = 1)
                                           .With(e => e.EpisodeNumber = 1)
                                           .BuildListOfNew();

            _stored = episodes[0];
            _stored.AirDateUtc = new DateTime(2026, 1, 1, 20, 0, 0, DateTimeKind.Utc);
            _stored.Ratings = new Ratings { Votes = 1, Value = 1 };
            _stored.Images = new List<MediaCover.MediaCover> { new MediaCover.MediaCover(MediaCoverTypes.Screenshot, "https://example.test/old.jpg") };
            _stored = Db.Insert(_stored);

            _remote = episodes[1];
            _remote.AirDateUtc = new DateTime(2026, 2, 1, 20, 0, 0, DateTimeKind.Utc);
            _remote.Ratings = new Ratings { Votes = 2, Value = 2 };
            _remote.Images = new List<MediaCover.MediaCover> { new MediaCover.MediaCover(MediaCoverTypes.Screenshot, "https://example.test/new.jpg") };
        }

        // whileReading runs once the refresh has read the episodes: another writer saving in that window
        private void GivenEpisodeService(Action whileReading = null)
        {
            var real = Mocker.Resolve<EpisodeService>();
            var mock = new Mock<IEpisodeService>();

            mock.Setup(s => s.GetEpisodeBySeries(It.IsAny<int>())).Returns<int>(id =>
            {
                var episodes = real.GetEpisodeBySeries(id);
                _read = episodes.ToList();
                whileReading?.Invoke();

                return episodes;
            });
            mock.Setup(s => s.DeleteMany(It.IsAny<List<Episode>>())).Callback<List<Episode>>(real.DeleteMany);
            mock.Setup(s => s.UpdateMany(It.IsAny<List<Episode>>())).Callback<List<Episode>>(real.UpdateMany);
            mock.Setup(s => s.UpdateEpisodeFields(It.IsAny<List<Episode>>(), It.IsAny<Expression<Func<Episode, object>>[]>()))
                .Callback<List<Episode>, Expression<Func<Episode, object>>[]>(real.UpdateEpisodeFields);
            mock.Setup(s => s.InsertMany(It.IsAny<List<Episode>>())).Callback<List<Episode>>(real.InsertMany);

            Mocker.SetConstant<IEpisodeService>(mock.Object);
        }

        private static List<PropertyInfo> MappedColumns()
        {
            var excluded = TableMapping.Mapper.ExcludeProperties(typeof(Episode)).Select(p => p.Name).ToList();

            return typeof(Episode).GetProperties()
                                  .Where(p => p.IsMappableProperty() && !excluded.Contains(p.Name) && p.Name != nameof(Episode.Id))
                                  .ToList();
        }

        private static string Comparable(PropertyInfo column, Episode episode)
        {
            var value = column.GetValue(episode);

            return value is DateTime dateTime ? dateTime.Ticks.ToString() : JsonSerializer.Serialize(value);
        }

        [Test]
        public void should_keep_changes_saved_to_an_episode_while_the_refresh_runs()
        {
            var searched = new DateTime(2026, 3, 1, 12, 0, 0, DateTimeKind.Utc);

            GivenEpisodeService(() =>
            {
                var current = _repository.Get(_stored.Id);
                current.Monitored = !_stored.Monitored;
                current.EpisodeFileId = 99;
                current.LastSearchTime = searched;
                _repository.Update(current);
            });

            Subject.RefreshEpisodeInfo(_series, new List<Episode> { _remote });

            var saved = _repository.Get(_stored.Id);
            saved.Title.Should().Be(_remote.Title);
            saved.Monitored.Should().Be(!_stored.Monitored);
            saved.EpisodeFileId.Should().Be(99);
            saved.LastSearchTime.Should().Be(searched);
        }

        [Test]
        public void should_save_every_column_the_refresh_sets()
        {
            GivenEpisodeService();
            var before = _repository.Get(_stored.Id);

            Subject.RefreshEpisodeInfo(_series, new List<Episode> { _remote });

            var refreshed = _read.Single();
            var after = _repository.Get(_stored.Id);
            var set = MappedColumns().Where(c => Comparable(c, refreshed) != Comparable(c, before)).ToList();

            set.Select(c => c.Name).Should().Contain(new[] { nameof(Episode.Title), nameof(Episode.AirDateUtc), nameof(Episode.Images) });
            set.Where(c => Comparable(c, after) == Comparable(c, before)).Select(c => c.Name).Should().BeEmpty("the refresh set these columns but didn't save them");
        }
    }
}
