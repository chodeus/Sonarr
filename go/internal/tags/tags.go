// Package tags is the Go port of NzbDrone.Core.Tags. It owns the small
// Tag table (Id, Label) and a repository with the CRUD surface that the
// rest of Sonarr's domain code calls into. The C# side uses Dapper +
// IBasicRepository<Tag>; the Go side uses database/sql directly with
// hand-written SQL because the Tag model is too small to justify a
// generic ORM layer (the broader IBasicRepository<T> equivalent will
// land separately when a model with embedded-doc fields needs it).
package tags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
)

// Tag is the persisted model. Mirrors src/NzbDrone.Core/Tags/Tag.cs:
// { Id INT PK, Label STRING UNIQUE }. JSON tags use camelCase to match
// the System.Text.Json wire contract documented in
// analysis/embedded-docs/catalogue.md.
type Tag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

// ErrNotFound is returned by Get / GetByLabel when no row matches.
// Callers can errors.Is against it.
var ErrNotFound = errors.New("tags: not found")

// Repo is a thin CRUD wrapper over the Tags table. It is engine-aware
// only at the placeholder-syntax level: SQLite uses '?' and Postgres
// uses '$N'.
type Repo struct {
	db *datastore.DB
}

// NewRepo returns a Repo bound to the given DB. The DB must point at a
// main-role database (RoleMain) — Tags lives in main, not log.
func NewRepo(db *datastore.DB) (*Repo, error) {
	if db == nil || db.SQL == nil {
		return nil, errors.New("tags: nil DB")
	}
	if db.Role != datastore.RoleMain {
		return nil, fmt.Errorf("tags: expected RoleMain, got %q", db.Role)
	}
	return &Repo{db: db}, nil
}

// Insert persists a new Tag and populates t.ID from the engine's
// generated identity column. Returns the same pointer for chaining.
func (r *Repo) Insert(ctx context.Context, t *Tag) (*Tag, error) {
	if t == nil {
		return nil, errors.New("tags: nil Tag")
	}
	q := fmt.Sprintf(`INSERT INTO "Tags" ("Label") VALUES (%s) RETURNING "Id"`, datastore.Placeholder(r.db.Engine, 1))
	if err := r.db.SQL.QueryRowContext(ctx, q, t.Label).Scan(&t.ID); err != nil {
		return nil, fmt.Errorf("tags: insert: %w", err)
	}
	return t, nil
}

// Get returns the Tag with the given ID or ErrNotFound.
func (r *Repo) Get(ctx context.Context, id int) (*Tag, error) {
	q := fmt.Sprintf(`SELECT "Id", "Label" FROM "Tags" WHERE "Id" = %s`, datastore.Placeholder(r.db.Engine, 1))
	var t Tag
	switch err := r.db.SQL.QueryRowContext(ctx, q, id).Scan(&t.ID, &t.Label); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("tags: get(%d): %w", id, err)
	}
	return &t, nil
}

// GetByLabel returns the Tag whose Label matches exactly, or ErrNotFound.
// Tags.Label is UNIQUE so at most one row matches.
func (r *Repo) GetByLabel(ctx context.Context, label string) (*Tag, error) {
	q := fmt.Sprintf(`SELECT "Id", "Label" FROM "Tags" WHERE "Label" = %s`, datastore.Placeholder(r.db.Engine, 1))
	var t Tag
	switch err := r.db.SQL.QueryRowContext(ctx, q, label).Scan(&t.ID, &t.Label); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("tags: getByLabel(%q): %w", label, err)
	}
	return &t, nil
}

// All returns every Tag, ordered by Id ascending.
func (r *Repo) All(ctx context.Context) ([]Tag, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `SELECT "Id", "Label" FROM "Tags" ORDER BY "Id"`)
	if err != nil {
		return nil, fmt.Errorf("tags: all: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Label); err != nil {
			return nil, fmt.Errorf("tags: all scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tags: all rows: %w", err)
	}
	return out, nil
}

// Update writes a new Label for an existing Tag. Returns ErrNotFound if
// no row has that ID.
func (r *Repo) Update(ctx context.Context, t *Tag) error {
	if t == nil {
		return errors.New("tags: nil Tag")
	}
	q := fmt.Sprintf(`UPDATE "Tags" SET "Label" = %s WHERE "Id" = %s`, datastore.Placeholder(r.db.Engine, 1), datastore.Placeholder(r.db.Engine, 2))
	res, err := r.db.SQL.ExecContext(ctx, q, t.Label, t.ID)
	if err != nil {
		return fmt.Errorf("tags: update(%d): %w", t.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("tags: update(%d) rowsAffected: %w", t.ID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the Tag with the given ID. Returns ErrNotFound if no
// row matched.
func (r *Repo) Delete(ctx context.Context, id int) error {
	q := fmt.Sprintf(`DELETE FROM "Tags" WHERE "Id" = %s`, datastore.Placeholder(r.db.Engine, 1))
	res, err := r.db.SQL.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("tags: delete(%d): %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("tags: delete(%d) rowsAffected: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
