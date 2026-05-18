package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httperr"
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
			httperr.BadRequest(w, "seriesId or episodeIds must be provided")
			return
		}
		seriesID, err := strconv.Atoi(seriesIDStr)
		if err != nil {
			httperr.BadRequest(w, "invalid seriesId")
			return
		}
		rows, err := lister.GetEpisodesBySeries(r.Context(), seriesID)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episodes: %w", err), nil)
			return
		}
		out := make([]EpisodeResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, ToEpisodeResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// EpisodeMonitorWriter is the write surface for PUT /episode/monitor.
// Satisfied directly by *episodes.Service.
type EpisodeMonitorWriter interface {
	EpisodeBySeriesLister
	GetEpisodes(ctx context.Context, ids []int) ([]episodes.Episode, error)
	SetEpisodeMonitored(ctx context.Context, episodeID int, monitored bool) error
	SetMonitored(ctx context.Context, ids []int, monitored bool) error
}

// episodesMonitoredBody mirrors C# `EpisodesMonitoredResource` —
// `{episodeIds: int[], monitored: bool}`.
type episodesMonitoredBody struct {
	EpisodeIDs []int `json:"episodeIds"`
	Monitored  bool  `json:"monitored"`
}

// EpisodeMonitorHandler returns PUT /api/v3/episode/monitor. Mirrors
// `EpisodeController.SetEpisodesMonitored` at `:62-78` — sets
// monitored flag for a single id or a batch, then returns the
// updated rows projected to EpisodeResource. C# returns 202 Accepted.
//
// DEFERRED: includeImages query param (the EpisodeResource shape
// doesn't yet model the embedded image set — same gap as the GET).
func EpisodeMonitorHandler(svc EpisodeMonitorWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body episodesMonitoredBody
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("episode/monitor: malformed body: %v", err))
			return
		}
		if len(body.EpisodeIDs) == 0 {
			httperr.BadRequest(w, "episode/monitor: episodeIds is required")
			return
		}
		ctx := r.Context()
		var err error
		if len(body.EpisodeIDs) == 1 {
			err = svc.SetEpisodeMonitored(ctx, body.EpisodeIDs[0], body.Monitored)
		} else {
			err = svc.SetMonitored(ctx, body.EpisodeIDs, body.Monitored)
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episode/monitor: %w", err), nil)
			return
		}
		rows, err := svc.GetEpisodes(ctx, body.EpisodeIDs)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episode/monitor: reload: %w", err), nil)
			return
		}
		out := make([]EpisodeResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, ToEpisodeResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// ToEpisodeResource maps the persisted Episode to the wire shape.
// Exported so the cmd/sonarr wanted-episodes adapter can reuse the
// same projection rather than re-rolling the field copy.
// The optional embedded resources (series/episodeFile/images) are
// DEFERRED — they're nil on the wire today and the empty-DB recording
// has zero rows so the gap is unobservable.
func ToEpisodeResource(e episodes.Episode) EpisodeResource {
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
