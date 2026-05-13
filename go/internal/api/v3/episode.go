package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
)

// EpisodeResource mirrors
// `src/Sonarr.Api.V3/Episodes/EpisodeResource.cs`. Skeleton wire
// shape — populated-row mapping for the optional series / episodeFile
// / images fields depends on the parsingservice + episodefiles +
// mediacover ports converging; empty-DB recording has zero records
// so the deferred mapping is unobservable today.
//
// Field-source mapping (C# → Go), passthrough subset:
//
//	id, seriesId, tvdbId, episodeFileId, seasonNumber, episodeNumber
//	title, airDate, airDateUtc, lastSearchTime, runtime, overview
//	hasFile, monitored, absolute/sceneAbsolute/scene* numbering,
//	unverifiedSceneNumbering
//
// DEFERRED (populated-row only): series (full SeriesResource),
// episodeFile (EpisodeFileResource), images ([]MediaCover),
// finaleType (computed from analytics).
type EpisodeResource struct {
	ID                         int        `json:"id"`
	SeriesID                   int        `json:"seriesId"`
	TvdbID                     int        `json:"tvdbId"`
	EpisodeFileID              int        `json:"episodeFileId"`
	SeasonNumber               int        `json:"seasonNumber"`
	EpisodeNumber              int        `json:"episodeNumber"`
	Title                      string     `json:"title"`
	AirDate                    string     `json:"airDate,omitempty"`
	AirDateUTC                 *time.Time `json:"airDateUtc,omitempty"`
	LastSearchTime             *time.Time `json:"lastSearchTime,omitempty"`
	Runtime                    int        `json:"runtime"`
	FinaleType                 string     `json:"finaleType,omitempty"`
	Overview                   string     `json:"overview,omitempty"`
	HasFile                    bool       `json:"hasFile"`
	Monitored                  bool       `json:"monitored"`
	AbsoluteEpisodeNumber      *int       `json:"absoluteEpisodeNumber,omitempty"`
	SceneAbsoluteEpisodeNumber *int       `json:"sceneAbsoluteEpisodeNumber,omitempty"`
	SceneEpisodeNumber         *int       `json:"sceneEpisodeNumber,omitempty"`
	SceneSeasonNumber          *int       `json:"sceneSeasonNumber,omitempty"`
	UnverifiedSceneNumbering   bool       `json:"unverifiedSceneNumbering"`
}

// EpisodeBySeriesLister narrows the read surface to the
// seriesId-filtered path the C# controller's seriesId branch uses.
// *episodes.Service.GetEpisodesBySeries satisfies it.
type EpisodeBySeriesLister interface {
	GetEpisodesBySeries(ctx context.Context, seriesID int) ([]episodes.Episode, error)
}

// EpisodeListHandler returns the http.Handler for GET /api/v3/episode.
// Mirrors `EpisodeController.GetEpisodes` at
// `src/Sonarr.Api.V3/Episodes/EpisodeController.cs:35-66`. The full
// C# controller dispatches on seriesId / episodeIds / episodeFileId
// query params — this slice only ports the seriesId branch (the
// recorded corpus uses seriesId=1). Other branches DEFERRED to a
// follow-up.
//
// Missing seriesId returns 400 to match C#
// `throw new BadRequestException("seriesId or episodeIds must be
// provided")`.
func EpisodeListHandler(lister EpisodeBySeriesLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seriesIDStr := r.URL.Query().Get("seriesId")
		if seriesIDStr == "" {
			http.Error(w, "seriesId or episodeIds must be provided", http.StatusBadRequest)
			return
		}
		seriesID, err := strconv.Atoi(seriesIDStr)
		if err != nil {
			http.Error(w, "invalid seriesId", http.StatusBadRequest)
			return
		}
		rows, err := lister.GetEpisodesBySeries(r.Context(), seriesID)
		if err != nil {
			http.Error(w, fmt.Sprintf("episodes: %v", err), http.StatusInternalServerError)
			return
		}
		out := make([]EpisodeResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toEpisodeResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// toEpisodeResource maps the persisted Episode to the wire shape.
// The optional embedded resources (series/episodeFile/images) are
// DEFERRED — they're nil on the wire today and the empty-DB recording
// has zero rows so the gap is unobservable.
func toEpisodeResource(e episodes.Episode) EpisodeResource {
	r := EpisodeResource{
		ID:                       e.ID,
		SeriesID:                 e.SeriesID,
		SeasonNumber:             e.SeasonNumber,
		EpisodeNumber:            e.EpisodeNumber,
		Title:                    e.Title,
		AirDate:                  e.AirDate,
		Runtime:                  e.Runtime,
		FinaleType:               e.FinaleType,
		Overview:                 e.Overview,
		Monitored:                e.Monitored,
		UnverifiedSceneNumbering: e.UnverifiedSceneNumbering,
	}
	if e.TvdbID != nil {
		r.TvdbID = *e.TvdbID
	}
	if e.EpisodeFileID != nil {
		r.EpisodeFileID = *e.EpisodeFileID
		r.HasFile = *e.EpisodeFileID > 0
	}
	if e.AirDateUtc != nil && !e.AirDateUtc.IsZero() {
		t := *e.AirDateUtc
		r.AirDateUTC = &t
	}
	if e.LastSearchTime != nil && !e.LastSearchTime.IsZero() {
		t := *e.LastSearchTime
		r.LastSearchTime = &t
	}
	if e.AbsoluteEpisodeNumber != nil {
		v := *e.AbsoluteEpisodeNumber
		r.AbsoluteEpisodeNumber = &v
	}
	if e.SceneAbsoluteEpisodeNumber != nil {
		v := *e.SceneAbsoluteEpisodeNumber
		r.SceneAbsoluteEpisodeNumber = &v
	}
	if e.SceneEpisodeNumber != nil {
		v := *e.SceneEpisodeNumber
		r.SceneEpisodeNumber = &v
	}
	if e.SceneSeasonNumber != nil {
		v := *e.SceneSeasonNumber
		r.SceneSeasonNumber = &v
	}
	return r
}
