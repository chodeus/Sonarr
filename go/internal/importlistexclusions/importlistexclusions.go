// Package importlistexclusions is the Go port of
// NzbDrone.Core.ImportLists.Exclusions.ImportListExclusion persistence.
// Each row prevents a TVDB-id-keyed series from being added by automatic
// import-list runs even if a list returns it.
//
// Schema at HEAD (v230): Id, TvdbId UNIQUE, Title.
package importlistexclusions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Exclusion is one ImportListExclusions row.
type Exclusion struct {
	ID     int    `json:"id"`
	TvdbID int    `json:"tvdbId"`
	Title  string `json:"title"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("importlistexclusions: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Exclusion]
}

var mapper = repo.Mapper[Exclusion]{
	Name:    "importlistexclusions",
	Table:   "ImportListExclusions",
	Columns: []string{"TvdbId", "Title"},
	OrderBy: "Title",
	Bind:    func(e *Exclusion) []any { return []any{e.TvdbID, e.Title} },
	Scan:    func(e *Exclusion, s repo.RowScanner) error { return s.Scan(&e.ID, &e.TvdbID, &e.Title) },
	GetID:   func(e *Exclusion) int { return e.ID },
	SetID:   func(e *Exclusion, id int) { e.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// GetByTvdbID returns the Exclusion for the given TvdbId or ErrNotFound.
func (r *Repo) GetByTvdbID(ctx context.Context, tvdbID int) (*Exclusion, error) {
	db := r.DB()
	q := fmt.Sprintf(`SELECT "Id","TvdbId","Title" FROM "ImportListExclusions" WHERE "TvdbId" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var e Exclusion
	switch err := db.SQL.QueryRowContext(ctx, q, tvdbID).Scan(&e.ID, &e.TvdbID, &e.Title); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("importlistexclusions: getByTvdbID(%d): %w", tvdbID, err)
	}
	return &e, nil
}
