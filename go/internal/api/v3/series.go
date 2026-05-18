package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesResource is the JSON shape produced by GET /api/v3/series.
// Mirrors `src/Sonarr.Api.V3/Series/SeriesResource.cs`.
//
// This slice ships the wire-shape skeleton with a minimal projection
// for the scalar columns the persistence layer already carries
// (Id+Title+SortTitle+Status+Path+Monitored+...). The richer joined
// fields — Statistics (per-season counts), AlternateTitles (Series
// AlternateTitles table), Seasons (Series.Seasons opaque JSON
// decoded), Images (MediaCover list), OriginalLanguage (full
// Language DTO), Ratings (Ratings JSON), and the various computed
// flags (Ended, LanguageProfileId, EpisodesChanged) — are DEFERRED
// until the consumer ports converge. The empty-DB recording has
// zero records so the gaps are unobservable today; the full
// projection lands in a follow-up slice keyed to the next
// populated-row recording.
//
// Field-source mapping (C# → Go), passthrough subset wired here:
//
//	id              → Series.ID (omitempty per RestResource)
//	title           → Series.Title
//	sortTitle       → Series.SortTitle
//	status          → Series.Status (int enum → camelCase string)
//	overview        → Series.Overview
//	network         → Series.Network
//	airTime         → Series.AirTime
//	year            → Series.Year
//	path            → Series.Path
//	qualityProfileId→ Series.QualityProfileID
//	seasonFolder    → Series.SeasonFolder
//	monitored       → Series.Monitored
//	useSceneNumbering→ Series.UseSceneNumbering
//	runtime         → Series.Runtime
//	tvdbId          → Series.TvdbID
//	tvRageId        → Series.TvRageID
//	tvMazeId        → Series.TvMazeID
//	tmdbId          → Series.TmdbID
//	firstAired      → Series.FirstAired (omitempty)
//	lastAired       → Series.LastAired (omitempty)
//	seriesType      → Series.SeriesType (int enum → camelCase string)
//	cleanTitle      → Series.CleanTitle
//	imdbId          → Series.ImdbID
//	titleSlug       → Series.TitleSlug
//	rootFolderPath  → Series.RootFolderPath (runtime-only)
//	certification   → Series.Certification
//	added           → Series.Added (omitempty)
//	tags            → Series.Tags ([]int → emitted as set; nil → [])
//	monitorNewItems → Series.MonitorNewItems (int enum → string)
//	ended           → computed (Status == Ended)
//	languageProfileId→ hard-coded 1 (C# `=> 1`)
//
// DEFERRED (populated-row only): alternateTitles, images, originalLanguage,
// remotePoster, seasons, addOptions, ratings, statistics, episodesChanged,
// genres, folder.
type SeriesResource struct {
	ID                int      `json:"id,omitempty"`
	Title             string   `json:"title"`
	SortTitle         string   `json:"sortTitle"`
	Status            string   `json:"status"`
	Overview          string   `json:"overview"`
	Network           string   `json:"network"`
	AirTime           string   `json:"airTime"`
	Year              int      `json:"year"`
	Path              string   `json:"path"`
	QualityProfileID  int      `json:"qualityProfileId"`
	SeasonFolder      bool     `json:"seasonFolder"`
	Monitored         bool     `json:"monitored"`
	UseSceneNumbering bool     `json:"useSceneNumbering"`
	Runtime           int      `json:"runtime"`
	TvdbID            int      `json:"tvdbId"`
	TvRageID          int      `json:"tvRageId"`
	TvMazeID          int      `json:"tvMazeId"`
	TmdbID            int      `json:"tmdbId"`
	SeriesType        string   `json:"seriesType"`
	CleanTitle        string   `json:"cleanTitle"`
	ImdbID            string   `json:"imdbId"`
	TitleSlug         string   `json:"titleSlug"`
	RootFolderPath    string   `json:"rootFolderPath"`
	Certification     string   `json:"certification"`
	Tags              []int    `json:"tags"`
	MonitorNewItems   string   `json:"monitorNewItems"`
	Ended             bool     `json:"ended"`
	LanguageProfileID int      `json:"languageProfileId"`
	Genres            []string `json:"genres"`
}

// SeriesLister narrows the series.Service surface to the read path
// the handler needs. *series.Service satisfies it via GetAllSeries.
type SeriesLister interface {
	GetAllSeries(ctx context.Context) ([]series.Series, error)
}

// SeriesListHandler returns the http.Handler for GET /api/v3/series.
// Mirrors `SeriesController.AllSeries` at
// `src/Sonarr.Api.V3/Series/SeriesController.cs:109-126`. The
// `tvdbId` + `includeSeasonImages` query params from the C# branch
// aren't exercised by the recorded corpus today and are DEFERRED.
func SeriesListHandler(lister SeriesLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.GetAllSeries(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series: %w", err), nil)
			return
		}
		out := make([]SeriesResource, 0, len(rows))
		for _, s := range rows {
			out = append(out, ToSeriesResource(s))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// ToSeriesResource maps the persisted Series to the wire shape's
// scalar subset. Joined/projected fields (alternateTitles,
// statistics, seasons, images, original-language full DTO) are
// DEFERRED — see the SeriesResource doc comment for the full list.
//
// Status + SeriesType + MonitorNewItems are int enums in the
// persistence layer; the C# wire converts them via
// JsonStringEnumConverter+CamelCase. The Go port's mapping tables
// live alongside the per-domain conversion helpers (qualitySource
// in qualitydefinition.go; the series-side ones land here so the
// table stays close to the only caller).
func ToSeriesResource(s series.Series) SeriesResource {
	tags := s.Tags
	if tags == nil {
		tags = []int{}
	}
	return SeriesResource{
		ID:                s.ID,
		Title:             s.Title,
		SortTitle:         s.SortTitle,
		Status:            seriesStatusName(s.Status),
		Overview:          s.Overview,
		Network:           s.Network,
		AirTime:           s.AirTime,
		Year:              s.Year,
		Path:              s.Path,
		QualityProfileID:  s.QualityProfileID,
		SeasonFolder:      s.SeasonFolder,
		Monitored:         s.Monitored,
		UseSceneNumbering: s.UseSceneNumbering,
		Runtime:           s.Runtime,
		TvdbID:            s.TvdbID,
		TvRageID:          s.TvRageID,
		TvMazeID:          s.TvMazeID,
		TmdbID:            s.TmdbID,
		SeriesType:        seriesTypeName(s.SeriesType),
		CleanTitle:        s.CleanTitle,
		ImdbID:            s.ImdbID,
		TitleSlug:         s.TitleSlug,
		RootFolderPath:    s.RootFolderPath,
		Certification:     s.Certification,
		Tags:              tags,
		MonitorNewItems:   monitorNewItemsName(s.MonitorNewItems),
		Ended:             s.Status == seriesStatusEnded,
		LanguageProfileID: 1,
		Genres:            []string{},
	}
}

// seriesStatusEnded mirrors `SeriesStatusType.Ended = 1`
// (`src/NzbDrone.Core/Tv/SeriesStatusType.cs`). Used by the Ended
// computed flag on the wire.
const seriesStatusEnded = 1

// seriesStatusName mirrors the C# JsonStringEnumConverter +
// CamelCase applied to `SeriesStatusType`:
//
//	0 → continuing, 1 → ended, 2 → upcoming, 3 → deleted
func seriesStatusName(s int) string {
	switch s {
	case 0:
		return "continuing"
	case 1:
		return "ended"
	case 2:
		return "upcoming"
	case 3:
		return "deleted"
	}
	return "continuing"
}

// seriesTypeName mirrors `SeriesTypes` camelCase mapping:
//
//	0 → standard, 1 → daily, 2 → anime
func seriesTypeName(t int) string {
	switch t {
	case 0:
		return "standard"
	case 1:
		return "daily"
	case 2:
		return "anime"
	}
	return "standard"
}

// monitorNewItemsName mirrors `NewItemMonitorTypes` camelCase
// mapping. C# enum at `MonitorNewItems.cs`:
//
//	0 → all, 1 → none
func monitorNewItemsName(m int) string {
	switch m {
	case 0:
		return "all"
	case 1:
		return "none"
	}
	return "all"
}
