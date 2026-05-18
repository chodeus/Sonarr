// Package episodes is the Go port of NzbDrone.Core.Tv.Episode persistence.
// Each row is one episode of a series — the cross-cut model that the
// scheduler, decision engine, parser, and renamer all consult.
//
// Schema at HEAD (v230): Id, SeriesId NOT NULL, SeasonNumber NOT NULL,
// EpisodeNumber NOT NULL, Title nullable, Overview nullable,
// EpisodeFileId nullable, AbsoluteEpisodeNumber nullable,
// SceneAbsoluteEpisodeNumber nullable, SceneSeasonNumber nullable,
// SceneEpisodeNumber nullable, Monitored nullable BOOL,
// AirDateUtc nullable, AirDate nullable TEXT, Ratings nullable TEXT,
// Images nullable TEXT, UnverifiedSceneNumbering BOOL NOT NULL DEFAULT 0,
// LastSearchTime nullable, AiredAfterSeasonNumber nullable,
// AiredBeforeSeasonNumber nullable, AiredBeforeEpisodeNumber nullable,
// TvdbId nullable, Runtime INT NOT NULL DEFAULT 0, FinaleType nullable.
//
// Two columns hold opaque embedded JSON: Ratings (the Ratings struct
// {votes, value}) and Images (List<MediaCover.MediaCover>). Both are
// stored as raw `string` until Phase 2 ports the typed wrappers.
//
// Monitored is nullable bool; modeled as plain bool with NULL coerced
// to false (matches the C# default for unset rows).
package episodes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Episode is one Episodes row.
type Episode struct {
	ID                         int        `json:"id"`
	SeriesID                   int        `json:"seriesId"`
	SeasonNumber               int        `json:"seasonNumber"`
	EpisodeNumber              int        `json:"episodeNumber"`
	Title                      string     `json:"title"`
	Overview                   string     `json:"overview"`
	EpisodeFileID              *int       `json:"episodeFileId"`
	AbsoluteEpisodeNumber      *int       `json:"absoluteEpisodeNumber"`
	SceneAbsoluteEpisodeNumber *int       `json:"sceneAbsoluteEpisodeNumber"`
	SceneSeasonNumber          *int       `json:"sceneSeasonNumber"`
	SceneEpisodeNumber         *int       `json:"sceneEpisodeNumber"`
	Monitored                  bool       `json:"monitored"`
	AirDateUtc                 *time.Time `json:"airDateUtc"`
	AirDate                    string     `json:"airDate"`
	Ratings                    string     `json:"ratings"`
	Images                     string     `json:"images"`
	UnverifiedSceneNumbering   bool       `json:"unverifiedSceneNumbering"`
	LastSearchTime             *time.Time `json:"lastSearchTime"`
	AiredAfterSeasonNumber     *int       `json:"airedAfterSeasonNumber"`
	AiredBeforeSeasonNumber    *int       `json:"airedBeforeSeasonNumber"`
	AiredBeforeEpisodeNumber   *int       `json:"airedBeforeEpisodeNumber"`
	TvdbID                     *int       `json:"tvdbId"`
	Runtime                    int        `json:"runtime"`
	FinaleType                 string     `json:"finaleType"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("episodes: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Episode]
}

var mapper = repo.Mapper[Episode]{
	Name:  "episodes",
	Table: "Episodes",
	Columns: []string{
		"SeriesId", "SeasonNumber", "EpisodeNumber", "Title", "Overview",
		"EpisodeFileId", "AbsoluteEpisodeNumber", "SceneAbsoluteEpisodeNumber",
		"SceneSeasonNumber", "SceneEpisodeNumber", "Monitored",
		"AirDateUtc", "AirDate", "Ratings", "Images",
		"UnverifiedSceneNumbering", "LastSearchTime",
		"AiredAfterSeasonNumber", "AiredBeforeSeasonNumber",
		"AiredBeforeEpisodeNumber", "TvdbId", "Runtime", "FinaleType",
	},
	OrderBy: "Id",
	Bind: func(e *Episode) []any {
		return []any{
			e.SeriesID, e.SeasonNumber, e.EpisodeNumber,
			nullableStringEmpty(e.Title),
			nullableStringEmpty(e.Overview),
			nullableInt(e.EpisodeFileID),
			nullableInt(e.AbsoluteEpisodeNumber),
			nullableInt(e.SceneAbsoluteEpisodeNumber),
			nullableInt(e.SceneSeasonNumber),
			nullableInt(e.SceneEpisodeNumber),
			sql.NullBool{Bool: e.Monitored, Valid: true},
			nullableTime(e.AirDateUtc),
			nullableStringEmpty(e.AirDate),
			nullableStringEmpty(e.Ratings),
			nullableStringEmpty(e.Images),
			e.UnverifiedSceneNumbering,
			nullableTime(e.LastSearchTime),
			nullableInt(e.AiredAfterSeasonNumber),
			nullableInt(e.AiredBeforeSeasonNumber),
			nullableInt(e.AiredBeforeEpisodeNumber),
			nullableInt(e.TvdbID),
			e.Runtime,
			nullableStringEmpty(e.FinaleType),
		}
	},
	Scan: func(e *Episode, s repo.RowScanner) error {
		var title, overview, airDate, ratings, images, finaleType sql.NullString
		var episodeFileID, absoluteEN, sceneAbsoluteEN, sceneSN, sceneEN sql.NullInt64
		var airedAfterSN, airedBeforeSN, airedBeforeEN, tvdbID sql.NullInt64
		var monitored sql.NullBool
		var airDateUtc, lastSearch sql.NullTime
		if err := s.Scan(
			&e.ID, &e.SeriesID, &e.SeasonNumber, &e.EpisodeNumber,
			&title, &overview, &episodeFileID, &absoluteEN, &sceneAbsoluteEN,
			&sceneSN, &sceneEN, &monitored, &airDateUtc, &airDate,
			&ratings, &images, &e.UnverifiedSceneNumbering, &lastSearch,
			&airedAfterSN, &airedBeforeSN, &airedBeforeEN, &tvdbID,
			&e.Runtime, &finaleType,
		); err != nil {
			return err
		}
		e.Title = title.String
		e.Overview = overview.String
		e.AirDate = airDate.String
		e.Ratings = ratings.String
		e.Images = images.String
		e.FinaleType = finaleType.String
		e.EpisodeFileID = unwrapInt(episodeFileID)
		e.AbsoluteEpisodeNumber = unwrapInt(absoluteEN)
		e.SceneAbsoluteEpisodeNumber = unwrapInt(sceneAbsoluteEN)
		e.SceneSeasonNumber = unwrapInt(sceneSN)
		e.SceneEpisodeNumber = unwrapInt(sceneEN)
		e.AiredAfterSeasonNumber = unwrapInt(airedAfterSN)
		e.AiredBeforeSeasonNumber = unwrapInt(airedBeforeSN)
		e.AiredBeforeEpisodeNumber = unwrapInt(airedBeforeEN)
		e.TvdbID = unwrapInt(tvdbID)
		e.Monitored = monitored.Valid && monitored.Bool
		e.AirDateUtc = unwrapTime(airDateUtc)
		e.LastSearchTime = unwrapTime(lastSearch)
		return nil
	},
	GetID: func(e *Episode) int { return e.ID },
	SetID: func(e *Episode, id int) { e.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// AllBySeriesID returns every Episode in the given Series, ordered by
// SeasonNumber + EpisodeNumber. This is the canonical sort for Series
// detail views and renamer iteration.
func (r *Repo) AllBySeriesID(ctx context.Context, seriesID int) ([]Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "SeriesId" = %s ORDER BY "SeasonNumber", "EpisodeNumber"`,
		selectColumnList(), datastore.Placeholder(db.Engine, 1))
	return r.scanQuery(ctx, q, seriesID, "allBySeriesID")
}

// DeleteBySeriesID issues a single `DELETE FROM "Episodes" WHERE
// "SeriesId" = ?` and returns the number of rows affected. Mirrors
// the C# cascade behaviour at `EpisodeService.HandleAsync(SeriesDeletedEvent)`
// (which calls `GetEpisodesBySeriesIds` + `DeleteMany`) — collapsed
// into one SQL round-trip since the Go port doesn't need to surface
// per-row entities to a downstream consumer (no IHandle<EpisodeDeletedEvent>
// fan-out is wired today; episode-file rows + history rows that
// reference episodes carry their own cascading deletes when their
// series goes away).
//
// Slice 5.D3 port pre-work for the eventual /series/{id}?deleteFiles=true
// cascade wired in slice 5.D5.
func (r *Repo) DeleteBySeriesID(ctx context.Context, seriesID int) (int64, error) {
	db := r.DB()
	q := fmt.Sprintf(`DELETE FROM "Episodes" WHERE "SeriesId" = %s`, datastore.Placeholder(db.Engine, 1))
	res, err := db.SQL.ExecContext(ctx, q, seriesID)
	if err != nil {
		return 0, fmt.Errorf("episodes: deleteBySeriesID(%d): %w", seriesID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("episodes: deleteBySeriesID(%d): rowsAffected: %w", seriesID, err)
	}
	return affected, nil
}

// AllBySeriesAndSeason returns episodes for one season, ordered by EpisodeNumber.
func (r *Repo) AllBySeriesAndSeason(ctx context.Context, seriesID, seasonNumber int) ([]Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "SeriesId" = %s AND "SeasonNumber" = %s ORDER BY "EpisodeNumber"`,
		selectColumnList(),
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2))
	rows, err := db.SQL.QueryContext(ctx, q, seriesID, seasonNumber)
	if err != nil {
		return nil, fmt.Errorf("episodes: allBySeriesAndSeason(%d,%d): %w", seriesID, seasonNumber, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Episode
	for rows.Next() {
		var e Episode
		if err := mapper.Scan(&e, rows); err != nil {
			return nil, fmt.Errorf("episodes: allBySeriesAndSeason scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episodes: allBySeriesAndSeason rows: %w", err)
	}
	return out, nil
}

func (r *Repo) scanQuery(ctx context.Context, query string, arg any, op string) ([]Episode, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("episodes: %s: %w", op, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Episode
	for rows.Next() {
		var e Episode
		if err := mapper.Scan(&e, rows); err != nil {
			return nil, fmt.Errorf("episodes: %s scan: %w", op, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episodes: %s rows: %w", op, err)
	}
	return out, nil
}

// selectColumnList returns the canonical SELECT list for full-row reads.
// Hand-rolled to keep the per-method SELECTs aligned with the Mapper's
// scan order without re-deriving from the Columns slice.
func selectColumnList() string {
	return `"Id","SeriesId","SeasonNumber","EpisodeNumber","Title","Overview",` +
		`"EpisodeFileId","AbsoluteEpisodeNumber","SceneAbsoluteEpisodeNumber",` +
		`"SceneSeasonNumber","SceneEpisodeNumber","Monitored","AirDateUtc","AirDate",` +
		`"Ratings","Images","UnverifiedSceneNumbering","LastSearchTime",` +
		`"AiredAfterSeasonNumber","AiredBeforeSeasonNumber","AiredBeforeEpisodeNumber",` +
		`"TvdbId","Runtime","FinaleType"`
}

func nullableInt(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func unwrapInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
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
