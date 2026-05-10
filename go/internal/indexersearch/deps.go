package indexersearch

import (
	"context"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/scenemappings"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesGetter is the narrow projection of internal/series.Service the
// orchestrator needs. Used by EpisodeSearch / SeasonSearch entry points
// to look up the Series for a given episode/series id, mirroring the C#
// ISeriesService dependency at ReleaseSearchService.cs:31.
type SeriesGetter interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// EpisodeRepo is the narrow projection of internal/episodes.Service the
// orchestrator needs. Mirrors the C# IEpisodeService usage subset — the
// rest of the IEpisodeService surface (ParsingService etc.) is used by
// the decisionengine front-end at queue [7c], not here.
//
// UpdateLastSearchTime persists the LastSearchTime field that the
// caller stamps in-memory before invocation. Mirrors
// ReleaseSearchService.cs:545-546 + EpisodeService.UpdateLastSearchTime
// at :199.
type EpisodeRepo interface {
	GetEpisode(ctx context.Context, id int) (*episodes.Episode, error)
	GetEpisodesBySeason(ctx context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error)
	UpdateLastSearchTime(ctx context.Context, list []episodes.Episode) error
}

// SceneMapper is the narrow projection of internal/scenemappings.Service.
// Mirrors the C# ISceneMappingService usage at
// ReleaseSearchService.cs:32 — exactly two methods:
//
//   - FindByTvdbID: the raw mapping list for a series, used by
//     scene_mapping.go's getSceneEpisodeMappings / getSceneSeasonMappings.
//   - GetSceneNames: the deduped scene-title list for a (tvdbId, seasons,
//     sceneSeasons) trio, used by Get<TSpec>(series, []Episode, ...) when
//     the search target is a list of episodes (daily / anime / special).
type SceneMapper interface {
	FindByTvdbID(ctx context.Context, tvdbID int) ([]scenemappings.Mapping, error)
	GetSceneNames(ctx context.Context, tvdbID int, seasonNumbers, sceneSeasonNumbers []int) ([]string, error)
}
