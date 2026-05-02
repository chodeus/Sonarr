// Package importlistitems is the Go port of NzbDrone.Core.Parser.Model
// ImportListItemInfo persistence (added by migration 193). Each row is a
// series candidate returned by an automatic import-list run; the
// ImportListSyncCommand walks these and decides which to add as series.
//
// Schema at HEAD (v230): Id, ImportListId NOT NULL, Title NOT NULL,
// TvdbId NOT NULL, Year nullable, TmdbId nullable, ImdbId nullable,
// MalId nullable, AniListId nullable, ReleaseDate nullable.
//
// Type-mapping note: the C# class declares int and DateTime properties
// (not int? / DateTime?) for Year, TmdbId, MalId, AniListId, ReleaseDate,
// but the schema columns are nullable. Sonarr's Dapper bridge tolerates
// the mismatch by reading NULL as zero-value. The Go port models them
// as pointer types so callers can distinguish "unset by the import-list
// provider" from "explicitly zero" — important because TVDB metadata
// for short-runners commonly omits Year, and the duplicate-detection
// logic during ImportListSyncCommand needs that distinction.
package importlistitems

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Item is one ImportListItems row.
type Item struct {
	ID           int        `json:"id"`
	ImportListID int        `json:"importListId"`
	Title        string     `json:"title"`
	TvdbID       int        `json:"tvdbId"`
	Year         *int       `json:"year,omitempty"`
	TmdbID       *int       `json:"tmdbId,omitempty"`
	ImdbID       *string    `json:"imdbId,omitempty"`
	MalID        *int       `json:"malId,omitempty"`
	AniListID    *int       `json:"aniListId,omitempty"`
	ReleaseDate  *time.Time `json:"releaseDate,omitempty"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("importlistitems: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Item]
}

var mapper = repo.Mapper[Item]{
	Name:    "importlistitems",
	Table:   "ImportListItems",
	Columns: []string{"ImportListId", "Title", "TvdbId", "Year", "TmdbId", "ImdbId", "MalId", "AniListId", "ReleaseDate"},
	OrderBy: "Id",
	Bind: func(it *Item) []any {
		return []any{
			it.ImportListID,
			it.Title,
			it.TvdbID,
			nullableInt(it.Year),
			nullableInt(it.TmdbID),
			nullableString(it.ImdbID),
			nullableInt(it.MalID),
			nullableInt(it.AniListID),
			nullableTime(it.ReleaseDate),
		}
	},
	Scan: func(it *Item, s repo.RowScanner) error {
		var year, tmdb, mal, anilist sql.NullInt64
		var imdb sql.NullString
		var release sql.NullTime
		if err := s.Scan(
			&it.ID, &it.ImportListID, &it.Title, &it.TvdbID,
			&year, &tmdb, &imdb, &mal, &anilist, &release,
		); err != nil {
			return err
		}
		it.Year = unwrapInt(year)
		it.TmdbID = unwrapInt(tmdb)
		it.MalID = unwrapInt(mal)
		it.AniListID = unwrapInt(anilist)
		it.ImdbID = unwrapString(imdb)
		it.ReleaseDate = unwrapTime(release)
		return nil
	},
	GetID: func(it *Item) int { return it.ID },
	SetID: func(it *Item, id int) { it.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// AllByImportListID returns every Item attached to the given ImportListId.
// This is the canonical lookup the ImportListSyncCommand uses to walk one
// list's candidates after a refresh.
func (r *Repo) AllByImportListID(ctx context.Context, importListID int) ([]Item, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","ImportListId","Title","TvdbId","Year","TmdbId","ImdbId","MalId","AniListId","ReleaseDate" FROM "ImportListItems" WHERE "ImportListId" = %s ORDER BY "Id"`,
		datastore.Placeholder(db.Engine, 1))
	rows, err := db.SQL.QueryContext(ctx, q, importListID)
	if err != nil {
		return nil, fmt.Errorf("importlistitems: allByImportListID(%d): %w", importListID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Item
	for rows.Next() {
		var it Item
		if err := mapper.Scan(&it, rows); err != nil {
			return nil, fmt.Errorf("importlistitems: allByImportListID scan: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("importlistitems: allByImportListID rows: %w", err)
	}
	return out, nil
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

func nullableString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func unwrapString(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
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
