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

        public override bool IsLongRunning => true;
    }
}
