using System;
using System.Linq;
using System.Threading;
using FluentAssertions;
using NUnit.Framework;
using NzbDrone.Core.Messaging.Commands;
using NzbDrone.Core.Test.Framework;
using NzbDrone.Core.Tv.Commands;

namespace NzbDrone.Core.Test.TvTests
{
    [TestFixture]
    public class ChangeEpisodeOrderCommandFixture : CoreTest
    {
        private static CommandModel Model(Command body, CommandStatus status)
        {
            return new CommandModel { Name = body.Name, Body = body, Status = status, QueuedAt = DateTime.UtcNow };
        }

        private static bool QueuedCommandStarts(Command started, Command queued)
        {
            var queue = new CommandQueue();
            queue.Add(Model(started, CommandStatus.Started));
            queue.Add(Model(queued, CommandStatus.Queued));

            using var cancellation = new CancellationTokenSource(TimeSpan.FromMilliseconds(500));

            return queue.GetConsumingEnumerable(cancellation.Token).Any();
        }

        private static ChangeEpisodeOrderCommand ChangeOrder()
        {
            return new ChangeEpisodeOrderCommand { SeriesId = 1, SeasonType = "dvd", RenameFiles = true };
        }

        [Test]
        public void should_wait_while_a_series_refresh_runs()
        {
            QueuedCommandStarts(new RefreshSeriesCommand(), ChangeOrder()).Should().BeFalse();
        }

        [Test]
        public void should_hold_back_a_series_refresh_while_it_runs()
        {
            QueuedCommandStarts(ChangeOrder(), new RefreshSeriesCommand()).Should().BeFalse();
        }

        [Test]
        public void should_let_a_second_series_refresh_start_beside_a_running_one()
        {
            QueuedCommandStarts(new RefreshSeriesCommand(), new RefreshSeriesCommand(new System.Collections.Generic.List<int> { 2 })).Should().BeTrue();
        }
    }
}
