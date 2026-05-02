// Package scenemappings is the Go port of NzbDrone.Core.DataAugmentation
// .Scene.SceneMapping persistence. The SceneMappings table caches scene-
// alias data fetched from services.sonarr.tv: alternate parse terms,
// season-number overrides, and indexer-specific search-term hints. The
// data-augmentation refresh job rewrites the table whole-sale on each
// successful fetch; runtime reads do high-volume lookups by TvdbId
// (the parser path's hot lookup).
//
// Schema at HEAD (v230): Id, TvdbId NOT NULL, SeasonNumber nullable,
// SearchTerm NOT NULL, ParseTerm NOT NULL, Title nullable, Type nullable,
// SceneSeasonNumber nullable, FilterRegex nullable, SceneOrigin nullable,
// SearchMode nullable, Comment nullable, MappingId nullable.
//
// Type-mapping note: the C# class declares only SeasonNumber,
// SceneSeasonNumber, and SearchMode as nullable (`int?` / `SearchMode?`);
// every other string column on the C# side is `string` (nullable by
// reference). Modeling matches the schema directly: nullable ints as
// *int, nullable strings as plain `string` with NULL coerced to "".
// The empty-string ⇄ NULL ambiguity matches the C# behaviour because
// the data-augmentation source treats them identically.
package scenemappings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Mapping is one SceneMappings row.
type Mapping struct {
	ID                int    `json:"id"`
	TvdbID            int    `json:"tvdbId"`
	SeasonNumber      *int   `json:"seasonNumber,omitempty"`
	SearchTerm        string `json:"searchTerm"`
	ParseTerm         string `json:"parseTerm"`
	Title             string `json:"title,omitempty"`
	Type              string `json:"type,omitempty"`
	SceneSeasonNumber *int   `json:"sceneSeasonNumber,omitempty"`
	FilterRegex       string `json:"filterRegex,omitempty"`
	SceneOrigin       string `json:"sceneOrigin,omitempty"`
	SearchMode        *int   `json:"searchMode,omitempty"`
	Comment           string `json:"comment,omitempty"`
	MappingID         string `json:"mappingId,omitempty"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("scenemappings: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Mapping]
}

var mapper = repo.Mapper[Mapping]{
	Name:  "scenemappings",
	Table: "SceneMappings",
	Columns: []string{
		"TvdbId", "SeasonNumber", "SearchTerm", "ParseTerm", "Title", "Type",
		"SceneSeasonNumber", "FilterRegex", "SceneOrigin", "SearchMode",
		"Comment", "MappingId",
	},
	OrderBy: "Id",
	Bind: func(m *Mapping) []any {
		return []any{
			m.TvdbID,
			nullableInt(m.SeasonNumber),
			m.SearchTerm,
			m.ParseTerm,
			nullableStringEmpty(m.Title),
			nullableStringEmpty(m.Type),
			nullableInt(m.SceneSeasonNumber),
			nullableStringEmpty(m.FilterRegex),
			nullableStringEmpty(m.SceneOrigin),
			nullableInt(m.SearchMode),
			nullableStringEmpty(m.Comment),
			nullableStringEmpty(m.MappingID),
		}
	},
	Scan: func(m *Mapping, s repo.RowScanner) error {
		var season, sceneSeason, searchMode sql.NullInt64
		var title, typ, filterRegex, sceneOrigin, comment, mappingID sql.NullString
		if err := s.Scan(
			&m.ID, &m.TvdbID, &season, &m.SearchTerm, &m.ParseTerm,
			&title, &typ, &sceneSeason, &filterRegex, &sceneOrigin,
			&searchMode, &comment, &mappingID,
		); err != nil {
			return err
		}
		m.SeasonNumber = unwrapInt(season)
		m.SceneSeasonNumber = unwrapInt(sceneSeason)
		m.SearchMode = unwrapInt(searchMode)
		m.Title = title.String
		m.Type = typ.String
		m.FilterRegex = filterRegex.String
		m.SceneOrigin = sceneOrigin.String
		m.Comment = comment.String
		m.MappingID = mappingID.String
		return nil
	},
	GetID: func(m *Mapping) int { return m.ID },
	SetID: func(m *Mapping, id int) { m.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// AllByTvdbID returns every Mapping with the given TvdbId. The parser
// path calls this on every release lookup, so callers should cache.
func (r *Repo) AllByTvdbID(ctx context.Context, tvdbID int) ([]Mapping, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","TvdbId","SeasonNumber","SearchTerm","ParseTerm","Title","Type","SceneSeasonNumber","FilterRegex","SceneOrigin","SearchMode","Comment","MappingId" FROM "SceneMappings" WHERE "TvdbId" = %s ORDER BY "Id"`,
		datastore.Placeholder(db.Engine, 1))
	rows, err := db.SQL.QueryContext(ctx, q, tvdbID)
	if err != nil {
		return nil, fmt.Errorf("scenemappings: allByTvdbID(%d): %w", tvdbID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Mapping
	for rows.Next() {
		var m Mapping
		if err := mapper.Scan(&m, rows); err != nil {
			return nil, fmt.Errorf("scenemappings: allByTvdbID scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scenemappings: allByTvdbID rows: %w", err)
	}
	return out, nil
}

// DeleteAll removes every row in the table. This is the data-augmentation
// refresh path: drop everything, then re-insert from the latest fetch.
// Wrapping it in a transaction is the caller's responsibility.
func (r *Repo) DeleteAll(ctx context.Context) error {
	db := r.DB()
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM "SceneMappings"`); err != nil {
		return fmt.Errorf("scenemappings: deleteAll: %w", err)
	}
	return nil
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

// nullableStringEmpty persists "" as NULL. The data-augmentation source
// makes "" and NULL semantically identical, so callers don't need to
// distinguish them.
func nullableStringEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
