using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text.Json;
using FizzWare.NBuilder;
using FluentAssertions;
using Moq;
using NUnit.Framework;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.AutoTagging;
using NzbDrone.Core.Datastore;
using NzbDrone.Core.Exceptions;
using NzbDrone.Core.Languages;
using NzbDrone.Core.MediaFiles;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.MetadataSource;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv;
using NzbDrone.Core.Tv.Commands;
using NzbDrone.Core.Tv.Events;
using NzbDrone.Test.Common;

namespace NzbDrone.Core.Test.TvTests
{
    [TestFixture]
    public class RefreshSeriesConcurrentChangesFixture : DbTest<RefreshSeriesService, Series>
    {
        private Series _series;
        private Series _seriesInfo;
        private string _oldPath;
        private string _newPath;

        [SetUp]
        public void Setup()
        {
            Mocker.SetConstant<ISeriesRepository>(new SeriesRepository(Mocker.Resolve<IMainDatabase>(), Mocker.Resolve<IEventAggregator>()));
            Mocker.SetConstant<ISeriesService>(Mocker.Resolve<SeriesService>());

            Mocker.GetMock<IAutoTaggingService>()
                  .Setup(s => s.GetTagChanges(It.IsAny<Series>()))
                  .Returns(new AutoTaggingChanges());

            _oldPath = @"C:\Test\TV\Old Folder".AsOsAgnostic();
            _newPath = @"C:\Test\TV\New Folder".AsOsAgnostic();

            _series = Builder<Series>.CreateNew()
                                     .With(s => s.Title = "Old Title")
                                     .With(s => s.Status = SeriesStatusType.Continuing)
                                     .With(s => s.Language = Language.English)
                                     .With(s => s.Path = _oldPath)
                                     .With(s => s.PendingPath = _newPath)
                                     .With(s => s.QualityProfileId = 1)
                                     .With(s => s.Monitored = true)
                                     .With(s => s.Seasons = new List<Season> { new Season { SeasonNumber = 1, Monitored = true } })
                                     .With(s => s.Tags = new HashSet<int>())
                                     .BuildNew();

            _series = Db.Insert(_series);

            _seriesInfo = _series.JsonClone();
            _seriesInfo.Title = "New Title";
            _seriesInfo.Overview = "New overview";
            _seriesInfo.Seasons = new List<Season> { new Season { SeasonNumber = 1 }, new Season { SeasonNumber = 2 } };
        }

        // Another writer, such as the folder move, saving the series while the refresh is busy
        private void SaveChange(Action<Series> change)
        {
            var repository = Mocker.Resolve<ISeriesRepository>();
            var stored = repository.Get(_series.Id);

            change(stored);
            repository.Update(stored);
        }

        private void GivenLookup(Action duringLookup = null)
        {
            Mocker.GetMock<IProvideSeriesInfo>()
                  .Setup(s => s.GetSeriesInfo(_series.TvdbId, It.IsAny<Language>(), It.IsAny<string>()))
                  .Returns(() =>
                  {
                      duringLookup?.Invoke();

                      return new Tuple<Series, List<Episode>>(_seriesInfo, new List<Episode>());
                  });
        }

        // whileTagging runs on the first call only: a later call repeating it would repair what the refresh broke
        private void GivenAutoTag(int tagId, Action whileTagging = null)
        {
            var pending = whileTagging;

            Mocker.GetMock<IAutoTaggingService>()
                  .Setup(s => s.GetTagChanges(It.IsAny<Series>()))
                  .Returns(() =>
                  {
                      var run = pending;
                      pending = null;
                      run?.Invoke();

                      return new AutoTaggingChanges { TagsToAdd = new HashSet<int> { tagId } };
                  });
        }

        private void FinishTheMove()
        {
            SaveChange(s =>
            {
                s.Path = _newPath;
                s.PendingPath = null;
            });
        }

        private void RefreshTheSeries()
        {
            Subject.Execute(new RefreshSeriesCommand(new List<int> { _series.Id }));
        }

        [Test]
        public void should_keep_a_folder_move_that_finished_during_the_lookup()
        {
            GivenLookup(FinishTheMove);

            RefreshTheSeries();

            var stored = AllStoredModels.Single();
            stored.Path.Should().Be(_newPath);
            stored.PendingPath.Should().BeNull();
            stored.Title.Should().Be("New Title");
            stored.Overview.Should().Be("New overview");
            stored.Seasons.Select(s => s.SeasonNumber).Should().BeEquivalentTo(new[] { 1, 2 });
        }

        [Test]
        public void should_keep_a_change_saved_while_the_auto_tags_are_worked_out()
        {
            GivenLookup();
            GivenAutoTag(5, FinishTheMove);

            RefreshTheSeries();

            var stored = AllStoredModels.Single();
            stored.Path.Should().Be(_newPath);
            stored.PendingPath.Should().BeNull();
            stored.Title.Should().Be("New Title");
        }

        [Test]
        public void should_save_and_announce_auto_tag_changes_after_a_refresh()
        {
            GivenLookup();
            GivenAutoTag(5);

            RefreshTheSeries();

            AllStoredModels.Single().Tags.Should().Contain(5);

            Mocker.GetMock<IEventAggregator>()
                  .Verify(v => v.PublishEvent(It.Is<SeriesEditedEvent>(e => e.Series.Tags.Contains(5) && !e.OldSeries.Tags.Contains(5))), Times.Once());
        }

        [Test]
        public void should_finish_a_full_refresh_when_a_series_is_deleted_after_the_library_was_read()
        {
            var other = Db.Insert(Builder<Series>.CreateNew()
                                                 .With(s => s.TvdbId = _series.TvdbId + 1)
                                                 .With(s => s.TitleSlug = "other")
                                                 .With(s => s.Path = @"C:\Test\TV\Other".AsOsAgnostic())
                                                 .With(s => s.SortTitle = "zzz")
                                                 .With(s => s.Status = SeriesStatusType.Continuing)
                                                 .BuildNew());

            Mocker.GetMock<ICheckIfSeriesShouldBeRefreshed>()
                  .Setup(s => s.ShouldRefresh(It.Is<Series>(c => c.Id == _series.Id)))
                  .Callback(() => Mocker.Resolve<ISeriesRepository>().Delete(_series.Id))
                  .Returns(false);

            Subject.Execute(new RefreshSeriesCommand());

            Mocker.GetMock<IDiskScanService>()
                  .Verify(v => v.Scan(It.Is<Series>(s => s.Id == other.Id)), Times.Once());

            Mocker.GetMock<IEventAggregator>()
                  .Verify(v => v.PublishEvent(It.IsAny<SeriesRefreshCompleteEvent>()), Times.Once());
        }

        [Test]
        public void should_refresh_the_next_series_when_one_is_deleted_during_its_lookup()
        {
            var other = Db.Insert(Builder<Series>.CreateNew()
                                                 .With(s => s.TvdbId = _series.TvdbId + 1)
                                                 .With(s => s.TitleSlug = "other")
                                                 .With(s => s.Path = @"C:\Test\TV\Other".AsOsAgnostic())
                                                 .With(s => s.Status = SeriesStatusType.Continuing)
                                                 .With(s => s.Language = Language.English)
                                                 .With(s => s.Seasons = new List<Season>())
                                                 .BuildNew());

            var otherInfo = other.JsonClone();
            otherInfo.Title = "Other New Title";

            GivenLookup(() => Mocker.Resolve<ISeriesRepository>().Delete(_series.Id));

            Mocker.GetMock<IProvideSeriesInfo>()
                  .Setup(s => s.GetSeriesInfo(other.TvdbId, It.IsAny<Language>(), It.IsAny<string>()))
                  .Returns(new Tuple<Series, List<Episode>>(otherInfo, new List<Episode>()));

            Subject.Execute(new RefreshSeriesCommand(new List<int> { _series.Id, other.Id }));

            AllStoredModels.Single().Title.Should().Be("Other New Title");

            ExceptionVerification.ExpectedErrors(1);
        }

        // Guards RefreshedFields: a field RefreshSeriesInfo sets but doesn't list would differ from the stored row
        [Test]
        public void should_save_every_field_the_lookup_sets()
        {
            Series refreshed = null;

            Mocker.GetMock<IEventAggregator>()
                  .Setup(s => s.PublishEvent(It.IsAny<SeriesUpdatedEvent>()))
                  .Callback<SeriesUpdatedEvent>(e => refreshed = e.Series);

            _seriesInfo = Builder<Series>.CreateListOfSize(2).Build().Last();
            _seriesInfo.TvdbId = _series.TvdbId;
            _seriesInfo.Status = SeriesStatusType.Ended;
            _seriesInfo.FirstAired = new DateTime(2020, 1, 2, 0, 0, 0, DateTimeKind.Utc);
            _seriesInfo.LastAired = new DateTime(2024, 5, 6, 0, 0, 0, DateTimeKind.Utc);
            _seriesInfo.OriginalLanguage = Language.French;
            _seriesInfo.MalIds = new HashSet<int> { 3 };
            _seriesInfo.AniListIds = new HashSet<int> { 4 };
            _seriesInfo.Images = new List<NzbDrone.Core.MediaCover.MediaCover> { Builder<NzbDrone.Core.MediaCover.MediaCover>.CreateNew().Build() };
            _seriesInfo.Ratings = Builder<Ratings>.CreateNew().Build();
            _seriesInfo.Genres = new List<string> { "Drama" };
            _seriesInfo.Actors = new List<Actor> { Builder<Actor>.CreateNew().Build() };
            _seriesInfo.SeasonTypes = new List<SeasonType> { Builder<SeasonType>.CreateNew().Build() };
            _seriesInfo.Seasons = new List<Season> { new Season { SeasonNumber = 1 }, new Season { SeasonNumber = 3 } };

            GivenLookup();

            RefreshTheSeries();

            var stored = AllStoredModels.Single();
            var unsaved = typeof(Series).GetProperties()
                                        .Where(p => p.CanWrite && p.Name is not (nameof(Series.Translations) or nameof(Series.RootFolderPath) or nameof(Series.QualityProfile)))
                                        .Where(p => !SameValue(p.GetValue(refreshed), p.GetValue(stored)))
                                        .Select(p => p.Name);

            unsaved.Should().BeEmpty();
        }

        private static bool SameValue(object refreshed, object stored)
        {
            if (refreshed is DateTime a && stored is DateTime b)
            {
                return Math.Abs((a - b).TotalSeconds) < 1;
            }

            return JsonSerializer.Serialize(refreshed) == JsonSerializer.Serialize(stored);
        }

        [Test]
        public void should_keep_a_season_monitoring_change_made_during_the_lookup()
        {
            GivenLookup(() => SaveChange(s => s.Seasons.Single().Monitored = false));

            RefreshTheSeries();

            AllStoredModels.Single().Seasons.Single(s => s.SeasonNumber == 1).Monitored.Should().BeFalse();

            Mocker.GetMock<IEpisodeService>()
                  .Verify(v => v.SetEpisodeMonitoredBySeason(It.IsAny<int>(), It.IsAny<int>(), It.IsAny<bool>()), Times.Never());
        }

        [Test]
        public void should_keep_a_profile_and_monitoring_change_made_during_the_lookup()
        {
            GivenLookup(() => SaveChange(s =>
            {
                s.QualityProfileId = 2;
                s.Monitored = false;
            }));

            RefreshTheSeries();

            var stored = AllStoredModels.Single();
            stored.QualityProfileId.Should().Be(2);
            stored.Monitored.Should().BeFalse();
        }

        [Test]
        public void should_keep_a_change_made_during_the_lookup_when_tvdb_no_longer_has_the_series()
        {
            Mocker.GetMock<IProvideSeriesInfo>()
                  .Setup(s => s.GetSeriesInfo(_series.TvdbId, It.IsAny<Language>(), It.IsAny<string>()))
                  .Callback(FinishTheMove)
                  .Throws(new SeriesNotFoundException(_series.TvdbId));

            RefreshTheSeries();

            var stored = AllStoredModels.Single();
            stored.Status.Should().Be(SeriesStatusType.Deleted);
            stored.Path.Should().Be(_newPath);
            stored.PendingPath.Should().BeNull();

            ExceptionVerification.ExpectedErrors(1);
        }

        [Test]
        public void should_save_only_the_tags_after_a_lookup_that_failed()
        {
            Mocker.GetMock<IProvideSeriesInfo>()
                  .Setup(s => s.GetSeriesInfo(_series.TvdbId, It.IsAny<Language>(), It.IsAny<string>()))
                  .Throws(new IOException());

            GivenAutoTag(5, FinishTheMove);

            RefreshTheSeries();

            var stored = AllStoredModels.Single();
            stored.Tags.Should().Contain(5);
            stored.Path.Should().Be(_newPath);
            stored.PendingPath.Should().BeNull();

            Mocker.GetMock<IEventAggregator>()
                  .Verify(v => v.PublishEvent(It.Is<SeriesEditedEvent>(e => e.Series.Tags.Contains(5) && !e.OldSeries.Tags.Contains(5))), Times.Once());

            ExceptionVerification.ExpectedErrors(1);
        }

        [Test]
        public void should_keep_a_change_made_after_a_full_refresh_read_every_series()
        {
            Mocker.GetMock<ICheckIfSeriesShouldBeRefreshed>()
                  .Setup(s => s.ShouldRefresh(It.IsAny<Series>()))
                  .Callback(FinishTheMove)
                  .Returns(false);

            GivenAutoTag(5);

            Subject.Execute(new RefreshSeriesCommand());

            var stored = AllStoredModels.Single();
            stored.Tags.Should().Contain(5);
            stored.Path.Should().Be(_newPath);
            stored.PendingPath.Should().BeNull();
        }

        [Test]
        public void should_save_the_tidied_path_when_no_move_is_pending()
        {
            SaveChange(s =>
            {
                s.Path = @"C:\Test\TV\.\Old Folder".AsOsAgnostic();
                s.PendingPath = null;
            });

            GivenLookup();

            RefreshTheSeries();

            AllStoredModels.Single().Path.Should().Be(_oldPath);
        }

        [Test]
        public void should_leave_the_path_to_a_pending_move()
        {
            var untidyPath = @"C:\Test\TV\.\Old Folder".AsOsAgnostic();

            SaveChange(s => s.Path = untidyPath);

            GivenLookup();

            RefreshTheSeries();

            var stored = AllStoredModels.Single();
            stored.Path.Should().Be(untidyPath);
            stored.PendingPath.Should().Be(_newPath);
        }
    }
}
