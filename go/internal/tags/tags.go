// Package tags is the Go port of NzbDrone.Core.Tags. It owns the small
// Tag table (Id, Label) and a repository with the CRUD surface that the
// rest of Sonarr's domain code calls into. The C# side uses Dapper +
// IBasicRepository<Tag>; the Go side uses internal/repo's generic
// Repository[T] for the standard CRUD operations and adds GetByLabel as a
// domain-specific helper.
package tags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Tag is the persisted model. Mirrors src/NzbDrone.Core/Tags/Tag.cs:
// { Id INT PK, Label STRING UNIQUE }.
type Tag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

// ErrNotFound is returned by Get / GetByLabel when no row matches.
var ErrNotFound = errors.New("tags: not found")

// Repo is a thin CRUD wrapper over the Tags table.
type Repo struct {
	*repo.Repository[Tag]
}

var mapper = repo.Mapper[Tag]{
	Name:    "tags",
	Table:   "Tags",
	Columns: []string{"Label"},
	OrderBy: "Id",
	Bind:    func(t *Tag) []any { return []any{t.Label} },
	Scan:    func(t *Tag, s repo.RowScanner) error { return s.Scan(&t.ID, &t.Label) },
	GetID:   func(t *Tag) int { return t.ID },
	SetID:   func(t *Tag, id int) { t.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// GetByLabel returns the Tag whose Label matches exactly, or ErrNotFound.
// Tags.Label is UNIQUE so at most one row matches.
func (r *Repo) GetByLabel(ctx context.Context, label string) (*Tag, error) {
	db := r.DB()
	q := fmt.Sprintf(`SELECT "Id","Label" FROM "Tags" WHERE "Label" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var t Tag
	switch err := db.SQL.QueryRowContext(ctx, q, label).Scan(&t.ID, &t.Label); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("tags: getByLabel(%q): %w", label, err)
	}
	return &t, nil
}
