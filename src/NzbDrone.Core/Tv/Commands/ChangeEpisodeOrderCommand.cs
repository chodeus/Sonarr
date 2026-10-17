using NzbDrone.Core.Messaging.Commands;

namespace NzbDrone.Core.Tv.Commands
{
    public class ChangeEpisodeOrderCommand : Command
    {
        public int SeriesId { get; set; }
        public string SeasonType { get; set; }
        public bool RenameFiles { get; set; }

        public override bool SendUpdatesToClient => true;

        public override bool RequiresDiskAccess => true;

        // A refresh running beside it writes back the series it read before the order changed
        public override bool IsExclusive => true;

        public override bool IsLongRunning => true;
    }
}
