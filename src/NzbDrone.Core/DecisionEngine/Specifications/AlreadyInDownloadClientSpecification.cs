using NLog;
using NzbDrone.Core.Download;
using NzbDrone.Core.Parser.Model;

namespace NzbDrone.Core.DecisionEngine.Specifications
{
    public class AlreadyInDownloadClientSpecification : IDownloadDecisionEngineSpecification
    {
        private readonly IDuplicateReleaseCache _duplicateReleaseCache;
        private readonly Logger _logger;

        public AlreadyInDownloadClientSpecification(IDuplicateReleaseCache duplicateReleaseCache, Logger logger)
        {
            _duplicateReleaseCache = duplicateReleaseCache;
            _logger = logger;
        }

        public SpecificationPriority Priority => SpecificationPriority.Default;
        public RejectionType Type => RejectionType.Permanent;

        public DownloadSpecDecision IsSatisfiedBy(RemoteEpisode subject, ReleaseDecisionInformation information)
        {
            var downloadClient = _duplicateReleaseCache.Find(subject.Release);

            if (downloadClient == null)
            {
                return DownloadSpecDecision.Accept();
            }

            _logger.Debug("{0} already has {1}, rejecting.", downloadClient, subject.Release.Title);

            return DownloadSpecDecision.Reject(DownloadRejectionReason.AlreadyInDownloadClient, "{0} already has this release", downloadClient);
        }
    }
}
