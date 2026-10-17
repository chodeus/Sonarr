using System.Collections.Generic;

namespace NzbDrone.Core.Tv
{
    public class EpisodeOrderPreview
    {
        public int SeriesId { get; set; }
        public string SeasonType { get; set; }
        public bool CanChange { get; set; }
        public bool RefreshRequired { get; set; }
        public int SceneNumberedEpisodes { get; set; }
        public int SceneSeasonTitles { get; set; }
        public bool HasAbsoluteEpisodeNumbers { get; set; }
        public int EpisodesAdded { get; set; }
        public int EpisodesRemoved { get; set; }
        public List<EpisodeOrderPreviewFile> Files { get; set; }
    }

    public class EpisodeOrderPreviewFile
    {
        public int EpisodeFileId { get; set; }
        public string ExistingPath { get; set; }
        public string NewPath { get; set; }
        public List<EpisodeOrderPreviewEpisode> ExistingEpisodes { get; set; }
        public List<EpisodeOrderPreviewEpisode> NewEpisodes { get; set; }
        public EpisodeOrderPreviewFileStatus Status { get; set; }
    }

    public class EpisodeOrderPreviewEpisode
    {
        public int SeasonNumber { get; set; }
        public int EpisodeNumber { get; set; }
    }

    public enum EpisodeOrderPreviewFileStatus
    {
        Unchanged,
        Renamed,
        Conflict,
        Unmatched,
        Unnameable
    }
}
