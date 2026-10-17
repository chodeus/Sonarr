using System;
using NzbDrone.Core.Parser.Model;

namespace NzbDrone.Core.Exceptions
{
    // The client holds this exact torrent already, so adding it again can't succeed
    public class ReleaseAlreadyInDownloadClientException : DownloadClientRejectedReleaseException
    {
        public ReleaseAlreadyInDownloadClientException(ReleaseInfo release, string message, Exception innerException)
            : base(release, message, innerException)
        {
        }
    }
}
