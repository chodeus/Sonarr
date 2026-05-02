// Package qualitydefinitions is the Go port of NzbDrone.Core.Qualities
// QualityDefinition persistence. Each row associates a Quality enum value
// (the persisted identifier; see Quality.cs) with its user-facing Title.
//
// Schema at HEAD (v230): Id, Quality UNIQUE, Title UNIQUE.
//
// The C# QualityDefinition class also has GroupName, Weight, MinSize,
// MaxSize, and PreferredSize properties, but those are explicitly
// .Ignore()'d in TableMapping.cs:135-140 — they're populated at runtime
// from Quality.DefaultQualityDefinitions. This package mirrors that:
// persistence is the bare row, decoration happens at the service layer.
package qualitydefinitions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Definition is one QualityDefinitions row.
type Definition struct {
	ID      int    `json:"id"`
	Quality int    `json:"quality"`
	Title   string `json:"title"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("qualitydefinitions: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Definition]
}

var mapper = repo.Mapper[Definition]{
	Name:    "qualitydefinitions",
	Table:   "QualityDefinitions",
	Columns: []string{"Quality", "Title"},
	OrderBy: "Quality",
	Bind:    func(d *Definition) []any { return []any{d.Quality, d.Title} },
	Scan:    func(d *Definition, s repo.RowScanner) error { return s.Scan(&d.ID, &d.Quality, &d.Title) },
	GetID:   func(d *Definition) int { return d.ID },
	SetID:   func(d *Definition, id int) { d.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// GetByQuality returns the Definition for the given Quality enum value
// or ErrNotFound.
func (r *Repo) GetByQuality(ctx context.Context, quality int) (*Definition, error) {
	db := r.DB()
	q := fmt.Sprintf(`SELECT "Id","Quality","Title" FROM "QualityDefinitions" WHERE "Quality" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var d Definition
	switch err := db.SQL.QueryRowContext(ctx, q, quality).Scan(&d.ID, &d.Quality, &d.Title); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("qualitydefinitions: getByQuality(%d): %w", quality, err)
	}
	return &d, nil
}
