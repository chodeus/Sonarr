using NzbDrone.Core.Tv;

namespace Sonarr.Api.V5.Series;

public class SeriesEpisodeOrderResource
{
    public int SeriesId { get; set; }
    public string? SeasonType { get; set; }
    public bool RefreshRequired { get; set; }
    public bool HasAbsoluteEpisodeNumbers { get; set; }
    public int EpisodesAdded { get; set; }
    public int EpisodesRemoved { get; set; }
    public List<SeriesEpisodeOrderFileResource> Files { get; set; } = [];
}

public class SeriesEpisodeOrderFileResource
{
    public int EpisodeFileId { get; set; }
    public string? ExistingPath { get; set; }
    public string? NewPath { get; set; }
    public List<SeriesEpisodeOrderEpisodeResource> ExistingEpisodes { get; set; } = [];
    public List<SeriesEpisodeOrderEpisodeResource> NewEpisodes { get; set; } = [];
    public EpisodeOrderPreviewFileStatus Status { get; set; }
}

public class SeriesEpisodeOrderEpisodeResource
{
    public int SeasonNumber { get; set; }
    public int EpisodeNumber { get; set; }
}

public static class SeriesEpisodeOrderResourceMapper
{
    public static SeriesEpisodeOrderResource ToResource(this EpisodeOrderPreview model)
    {
        return new SeriesEpisodeOrderResource
        {
            SeriesId = model.SeriesId,
            SeasonType = model.SeasonType,
            RefreshRequired = model.RefreshRequired,
            HasAbsoluteEpisodeNumbers = model.HasAbsoluteEpisodeNumbers,
            EpisodesAdded = model.EpisodesAdded,
            EpisodesRemoved = model.EpisodesRemoved,
            Files = model.Files.Select(ToResource).ToList()
        };
    }

    private static SeriesEpisodeOrderFileResource ToResource(EpisodeOrderPreviewFile model)
    {
        return new SeriesEpisodeOrderFileResource
        {
            EpisodeFileId = model.EpisodeFileId,
            ExistingPath = model.ExistingPath,
            NewPath = model.NewPath,
            ExistingEpisodes = model.ExistingEpisodes.Select(ToResource).ToList(),
            NewEpisodes = model.NewEpisodes.Select(ToResource).ToList(),
            Status = model.Status
        };
    }

    private static SeriesEpisodeOrderEpisodeResource ToResource(EpisodeOrderPreviewEpisode model)
    {
        return new SeriesEpisodeOrderEpisodeResource
        {
            SeasonNumber = model.SeasonNumber,
            EpisodeNumber = model.EpisodeNumber
        };
    }
}
