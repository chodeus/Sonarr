// Package series is the Go port of NzbDrone.Core.Tv.Series persistence.
// The Series table is the most-cross-cut model in the schema — every
// subsystem (parser, indexer, decision engine, scheduler, renamer,
// notifications, metadata, history) reads or joins it.
//
// Schema at HEAD (v230) is 41 columns wide. See the Bind/Scan funcs in
// this file for the canonical ordering. Notable shape:
//
//   - 7 nullable timestamps (LastInfoSync, LastDiskSync, FirstAired,
//     NextAiring, Added, LastAired). All round-trip via *time.Time +
//     sql.NullTime, UTC-normalised on both write and read.
//   - 3 boolean columns persisted as boolean / INTEGER 0-or-1:
//     Monitored, SeasonFolder, UseSceneNumbering. All NOT NULL.
//   - 6 opaque embedded-JSON columns: Images, Seasons, Actors, Ratings,
//     Genres, AddOptions. Stored as raw `string` until Phase 2 ports
//     the typed wrappers.
//   - 2 embedded-doc id-array columns: MalIds and AniListIds carry
//     `[]int` JSON. Required NOT NULL with DEFAULT '[]'.
//   - Tags is the same `[]int` JSON shape, but nullable in the schema.
//   - Year, QualityProfileId are nullable ints.
//   - TitleSlug, ImdbId, AirTime, Network, OriginalCountry, Certification,
//     SortTitle are nullable strings; modeled as plain `string` with
//     NULL coerced to "" because callers don't need the distinction.
package series

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// SeriesType mirrors the C# `NzbDrone.Core.Tv.SeriesTypes` enum at
// `src/NzbDrone.Core/Tv/SeriesTypes.cs`. Persisted as an `int` in the
// `SeriesType` column; integer values are frozen so renumbering would
// corrupt user databases.
const (
	SeriesTypeStandard = 0
	SeriesTypeDaily    = 1
	SeriesTypeAnime    = 2
)

// SeriesStatus mirrors the C# `NzbDrone.Core.Tv.SeriesStatusType`
// enum at `src/NzbDrone.Core/Tv/SeriesStatusType.cs`. Persisted as
// `int` in the `Status` column; integer values are frozen.
const (
	SeriesStatusDeleted    = -1
	SeriesStatusContinuing = 0
	SeriesStatusEnded      = 1
	SeriesStatusUpcoming   = 2
)

// Series is one Series row.
type Series struct {
	ID                int        `json:"id"`
	TvdbID            int        `json:"tvdbId"`
	TvRageID          int        `json:"tvRageId"`
	ImdbID            string     `json:"imdbId"`
	Title             string     `json:"title"`
	TitleSlug         string     `json:"titleSlug"`
	CleanTitle        string     `json:"cleanTitle"`
	Status            int        `json:"status"`
	Overview          string     `json:"overview"`
	AirTime           string     `json:"airTime"`
	Images            string     `json:"images"`
	Path              string     `json:"path"`
	Monitored         bool       `json:"monitored"`
	SeasonFolder      bool       `json:"seasonFolder"`
	LastInfoSync      *time.Time `json:"lastInfoSync"`
	LastDiskSync      *time.Time `json:"lastDiskSync"`
	Runtime           int        `json:"runtime"`
	SeriesType        int        `json:"seriesType"`
	Network           string     `json:"network"`
	UseSceneNumbering bool       `json:"useSceneNumbering"`
	FirstAired        *time.Time `json:"firstAired"`
	NextAiring        *time.Time `json:"nextAiring"`
	// Year mirrors C# `Series.Year` (non-nullable `int`) per
	// go/JSON_CONVENTIONS.md Option A. Schema column is nullable;
	// Scan coerces NULL → 0 to match the C# tree's read-side
	// behaviour (its `int` property cannot hold NULL).
	Year          int    `json:"year"`
	Seasons       string `json:"seasons"`
	Actors        string `json:"actors"`
	Ratings       string `json:"ratings"`
	Genres        string `json:"genres"`
	Certification string `json:"certification"`
	SortTitle     string `json:"sortTitle"`
	// QualityProfileID mirrors C# `Series.QualityProfileId` (non-
	// nullable `int`) per go/JSON_CONVENTIONS.md Option A. Schema
	// column is NOT NULL.
	QualityProfileID int        `json:"qualityProfileId"`
	Tags             []int      `json:"tags"`
	Added            *time.Time `json:"added"`
	AddOptions       string     `json:"addOptions"`
	TvMazeID         int        `json:"tvMazeId"`
	OriginalLanguage int        `json:"originalLanguage"`
	LastAired        *time.Time `json:"lastAired"`
	MonitorNewItems  int        `json:"monitorNewItems"`
	TmdbID           int        `json:"tmdbId"`
	MalIDs           []int      `json:"malIds"`
	AniListIDs       []int      `json:"aniListIds"`
	OriginalCountry  string     `json:"originalCountry"`

	// RootFolderPath is a runtime-only field — not persisted in the
	// Series table. The C# `Series` class carries it as a regular
	// property but `TableMapping` doesn't bind it to a column; the
	// import / add-series flow sets it, and the bulk-update path uses
	// it (via `SeriesPathBuilder.BuildPath`) to recompute `Path`.
	// Unbound from the Bind/Scan path at the mapper level.
	RootFolderPath string `json:"rootFolderPath"`
}

// ApplyChanges ports `Series.ApplyChanges(Series otherSeries)` at
// `src/NzbDrone.Core/Tv/Series.cs:70-86`. Copies the 11 caller-
// supplied fields from `other` onto the receiver — used by
// `AddSeriesService.AddSkyhookData` (C# `AddSeriesService.cs:130`)
// to overlay the user-supplied add-shape onto the SkyHook-loaded
// row, and by the future PUT /api/v3/series Update flow on
// existing rows.
//
// The fields C# copies are exactly the 11 below. Notable omissions:
// every column SkyHook itself owns (TvRageId, ImdbId, Title,
// CleanTitle, SortTitle, Status, Overview, Network, AirTime, Runtime,
// Year, FirstAired, NextAiring, LastAired, Images, Genres, Actors,
// Ratings, TitleSlug, OriginalLanguage, OriginalCountry, MalIds,
// AniListIds, TmdbId, TvMazeId, Certification, UseSceneNumbering,
// LastInfoSync, LastDiskSync) — those are the SkyHook-authoritative
// shape and stay untouched by the merge.
func (s *Series) ApplyChanges(other *Series) {
	if s == nil || other == nil {
		return
	}
	s.TvdbID = other.TvdbID
	s.Seasons = other.Seasons
	s.Path = other.Path
	s.QualityProfileID = other.QualityProfileID
	s.SeasonFolder = other.SeasonFolder
	s.Monitored = other.Monitored
	s.MonitorNewItems = other.MonitorNewItems
	s.SeriesType = other.SeriesType
	s.RootFolderPath = other.RootFolderPath
	s.Tags = other.Tags
	s.AddOptions = other.AddOptions
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("series: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Series]
}

var mapper = repo.Mapper[Series]{
	Name:  "series",
	Table: "Series",
	Columns: []string{
		"TvdbId", "TvRageId", "ImdbId", "Title", "TitleSlug", "CleanTitle",
		"Status", "Overview", "AirTime", "Images", "Path", "Monitored",
		"SeasonFolder", "LastInfoSync", "LastDiskSync", "Runtime",
		"SeriesType", "Network", "UseSceneNumbering", "FirstAired",
		"NextAiring", "Year", "Seasons", "Actors", "Ratings", "Genres",
		"Certification", "SortTitle", "QualityProfileId", "Tags",
		"Added", "AddOptions", "TvMazeId", "OriginalLanguage", "LastAired",
		"MonitorNewItems", "TmdbId", "MalIds", "AniListIds", "OriginalCountry",
	},
	OrderBy: "Id",
	Bind: func(s *Series) []any {
		return []any{
			s.TvdbID, s.TvRageID,
			nullableStringEmpty(s.ImdbID),
			s.Title,
			nullableStringEmpty(s.TitleSlug),
			s.CleanTitle, s.Status,
			nullableStringEmpty(s.Overview),
			nullableStringEmpty(s.AirTime),
			defaultJSON(s.Images, "[]"),
			s.Path, s.Monitored, s.SeasonFolder,
			nullableTime(s.LastInfoSync),
			nullableTime(s.LastDiskSync),
			s.Runtime, s.SeriesType,
			nullableStringEmpty(s.Network),
			s.UseSceneNumbering,
			nullableTime(s.FirstAired),
			nullableTime(s.NextAiring),
			// Year + QualityProfileID per Option A: non-nullable
			// Go ints written directly. Schema's nullable Year column
			// receives 0 instead of NULL after Option A; matches what
			// the C# tree's `int` property would have written.
			s.Year,
			nullableStringEmpty(s.Seasons),
			nullableStringEmpty(s.Actors),
			nullableStringEmpty(s.Ratings),
			nullableStringEmpty(s.Genres),
			nullableStringEmpty(s.Certification),
			nullableStringEmpty(s.SortTitle),
			s.QualityProfileID,
			nullableTagsJSON(s.Tags),
			nullableTime(s.Added),
			nullableStringEmpty(s.AddOptions),
			s.TvMazeID, s.OriginalLanguage,
			nullableTime(s.LastAired),
			s.MonitorNewItems, s.TmdbID,
			marshalInts(s.MalIDs),
			marshalInts(s.AniListIDs),
			nullableStringEmpty(s.OriginalCountry),
		}
	},
	Scan: func(s *Series, sc repo.RowScanner) error {
		var imdbID, titleSlug, overview, airTime, network sql.NullString
		var seasons, actors, ratings, genres, certification, sortTitle sql.NullString
		var addOptions, originalCountry, tagsJSON sql.NullString
		var year, qualityProfileID sql.NullInt64
		var lastInfoSync, lastDiskSync, firstAired, nextAiring, added, lastAired sql.NullTime
		var malIDs, aniListIDs string
		if err := sc.Scan(
			&s.ID, &s.TvdbID, &s.TvRageID, &imdbID, &s.Title, &titleSlug,
			&s.CleanTitle, &s.Status, &overview, &airTime, &s.Images,
			&s.Path, &s.Monitored, &s.SeasonFolder,
			&lastInfoSync, &lastDiskSync, &s.Runtime, &s.SeriesType,
			&network, &s.UseSceneNumbering, &firstAired, &nextAiring,
			&year, &seasons, &actors, &ratings, &genres, &certification,
			&sortTitle, &qualityProfileID, &tagsJSON, &added, &addOptions,
			&s.TvMazeID, &s.OriginalLanguage, &lastAired,
			&s.MonitorNewItems, &s.TmdbID, &malIDs, &aniListIDs, &originalCountry,
		); err != nil {
			return err
		}
		s.ImdbID = imdbID.String
		s.TitleSlug = titleSlug.String
		s.Overview = overview.String
		s.AirTime = airTime.String
		s.Network = network.String
		s.Seasons = seasons.String
		s.Actors = actors.String
		s.Ratings = ratings.String
		s.Genres = genres.String
		s.Certification = certification.String
		s.SortTitle = sortTitle.String
		s.AddOptions = addOptions.String
		s.OriginalCountry = originalCountry.String
		// Year + QualityProfileID per Option A: NULL → 0 coercion
		// matches C# `int` property semantics.
		if year.Valid {
			s.Year = int(year.Int64)
		}
		if qualityProfileID.Valid {
			s.QualityProfileID = int(qualityProfileID.Int64)
		}
		s.LastInfoSync = unwrapTime(lastInfoSync)
		s.LastDiskSync = unwrapTime(lastDiskSync)
		s.FirstAired = unwrapTime(firstAired)
		s.NextAiring = unwrapTime(nextAiring)
		s.Added = unwrapTime(added)
		s.LastAired = unwrapTime(lastAired)
		// Tags is nullable in schema; tagsJSON.Valid → unmarshal, else nil.
		if tagsJSON.Valid && tagsJSON.String != "" {
			if err := datastore.UnmarshalEmbedded(tagsJSON.String, &s.Tags); err != nil {
				return fmt.Errorf("unmarshal Tags: %w", err)
			}
		}
		if err := datastore.UnmarshalEmbedded(malIDs, &s.MalIDs); err != nil {
			return fmt.Errorf("unmarshal MalIds: %w", err)
		}
		if err := datastore.UnmarshalEmbedded(aniListIDs, &s.AniListIDs); err != nil {
			return fmt.Errorf("unmarshal AniListIds: %w", err)
		}
		return nil
	},
	GetID: func(s *Series) int { return s.ID },
	SetID: func(s *Series, id int) { s.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// GetByTvdbID returns the Series with the given TvdbId or ErrNotFound.
// TvdbId is UNIQUE in the schema and is the primary external identifier
// the parser/import path uses.
func (r *Repo) GetByTvdbID(ctx context.Context, tvdbID int) (*Series, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Series" WHERE "TvdbId" = %s`,
		selectColumnList(), datastore.Placeholder(db.Engine, 1))
	var s Series
	if err := mapper.Scan(&s, db.SQL.QueryRowContext(ctx, q, tvdbID)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("series: getByTvdbID(%d): %w", tvdbID, err)
	}
	return &s, nil
}

func selectColumnList() string {
	return `"Id","TvdbId","TvRageId","ImdbId","Title","TitleSlug","CleanTitle",` +
		`"Status","Overview","AirTime","Images","Path","Monitored","SeasonFolder",` +
		`"LastInfoSync","LastDiskSync","Runtime","SeriesType","Network",` +
		`"UseSceneNumbering","FirstAired","NextAiring","Year","Seasons","Actors",` +
		`"Ratings","Genres","Certification","SortTitle","QualityProfileId","Tags",` +
		`"Added","AddOptions","TvMazeId","OriginalLanguage","LastAired",` +
		`"MonitorNewItems","TmdbId","MalIds","AniListIds","OriginalCountry"`
}

// nullableTagsJSON serialises Series.Tags as JSON. Schema permits NULL,
// but C# defaults to an empty list; we persist "[]" for nil to match
// the typical UI representation.
func nullableTagsJSON(xs []int) string {
	if xs == nil {
		xs = []int{}
	}
	out, err := datastore.MarshalEmbedded(xs)
	if err != nil {
		return `<marshal-error: ` + err.Error() + `>`
	}
	return out
}

func marshalInts(xs []int) string {
	if xs == nil {
		xs = []int{}
	}
	out, err := datastore.MarshalEmbedded(xs)
	if err != nil {
		return `<marshal-error: ` + err.Error() + `>`
	}
	return out
}

func nullableStringEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullableTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func unwrapTime(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time.UTC()
	return &v
}

func defaultJSON(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
