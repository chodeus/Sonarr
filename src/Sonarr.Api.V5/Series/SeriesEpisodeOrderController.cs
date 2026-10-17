using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.AspNetCore.Mvc;
using NzbDrone.Core.Tv;
using Sonarr.Http;

namespace Sonarr.Api.V5.Series;

[V5ApiController("series")]
public class SeriesEpisodeOrderController : Controller
{
    private readonly IEpisodeOrderChangeService _episodeOrderChangeService;

    public SeriesEpisodeOrderController(IEpisodeOrderChangeService episodeOrderChangeService)
    {
        _episodeOrderChangeService = episodeOrderChangeService;
    }

    [HttpGet("{id:int}/episodeorder")]
    [Produces("application/json")]
    public Ok<SeriesEpisodeOrderResource> GetEpisodeOrderPreview([FromRoute] int id, [FromQuery] string seasonType)
    {
        return TypedResults.Ok(_episodeOrderChangeService.Preview(id, seasonType).ToResource());
    }
}
