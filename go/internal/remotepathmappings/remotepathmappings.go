// Package remotepathmappings is the Go port of NzbDrone.Core.RemotePathMappings'
// RemotePathMapping table layer. The shape mirrors src/NzbDrone.Core/
// RemotePathMappings/RemotePathMapping.cs: { Id, Host, RemotePath, LocalPath }.
//
// The runtime translation logic (longest-prefix match across mappings,
// direction-aware substitution) lives in the service layer, not here.
package remotepathmappings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
)

// Mapping is one persisted row.
type Mapping struct {
	ID         int    `json:"id"`
	Host       string `json:"host"`
	RemotePath string `json:"remotePath"`
	LocalPath  string `json:"localPath"`
}

// ErrNotFound is returned by Get when no row matches.
var ErrNotFound = errors.New("remotepathmappings: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	db *datastore.DB
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	if db == nil || db.SQL == nil {
		return nil, errors.New("remotepathmappings: nil DB")
	}
	if db.Role != datastore.RoleMain {
		return nil, fmt.Errorf("remotepathmappings: expected RoleMain, got %q", db.Role)
	}
	return &Repo{db: db}, nil
}

// Insert persists a new Mapping and populates m.ID.
func (r *Repo) Insert(ctx context.Context, m *Mapping) (*Mapping, error) {
	if m == nil {
		return nil, errors.New("remotepathmappings: nil Mapping")
	}
	q := fmt.Sprintf(
		`INSERT INTO "RemotePathMappings" ("Host","RemotePath","LocalPath") VALUES (%s,%s,%s) RETURNING "Id"`,
		datastore.Placeholder(r.db.Engine, 1), datastore.Placeholder(r.db.Engine, 2), datastore.Placeholder(r.db.Engine, 3))
	if err := r.db.SQL.QueryRowContext(ctx, q, m.Host, m.RemotePath, m.LocalPath).Scan(&m.ID); err != nil {
		return nil, fmt.Errorf("remotepathmappings: insert: %w", err)
	}
	return m, nil
}

// Get returns the Mapping with the given ID or ErrNotFound.
func (r *Repo) Get(ctx context.Context, id int) (*Mapping, error) {
	q := fmt.Sprintf(`SELECT "Id","Host","RemotePath","LocalPath" FROM "RemotePathMappings" WHERE "Id" = %s`, datastore.Placeholder(r.db.Engine, 1))
	var m Mapping
	switch err := r.db.SQL.QueryRowContext(ctx, q, id).Scan(&m.ID, &m.Host, &m.RemotePath, &m.LocalPath); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("remotepathmappings: get(%d): %w", id, err)
	}
	return &m, nil
}

// All returns every Mapping, ordered by Id.
func (r *Repo) All(ctx context.Context) ([]Mapping, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `SELECT "Id","Host","RemotePath","LocalPath" FROM "RemotePathMappings" ORDER BY "Id"`)
	if err != nil {
		return nil, fmt.Errorf("remotepathmappings: all: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Mapping
	for rows.Next() {
		var m Mapping
		if err := rows.Scan(&m.ID, &m.Host, &m.RemotePath, &m.LocalPath); err != nil {
			return nil, fmt.Errorf("remotepathmappings: all scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("remotepathmappings: all rows: %w", err)
	}
	return out, nil
}

// Update writes new field values for an existing Mapping. ID must be set.
// Returns ErrNotFound if no row matched.
func (r *Repo) Update(ctx context.Context, m *Mapping) error {
	if m == nil {
		return errors.New("remotepathmappings: nil Mapping")
	}
	q := fmt.Sprintf(
		`UPDATE "RemotePathMappings" SET "Host" = %s, "RemotePath" = %s, "LocalPath" = %s WHERE "Id" = %s`,
		datastore.Placeholder(r.db.Engine, 1), datastore.Placeholder(r.db.Engine, 2), datastore.Placeholder(r.db.Engine, 3), datastore.Placeholder(r.db.Engine, 4))
	res, err := r.db.SQL.ExecContext(ctx, q, m.Host, m.RemotePath, m.LocalPath, m.ID)
	if err != nil {
		return fmt.Errorf("remotepathmappings: update(%d): %w", m.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remotepathmappings: update(%d) rowsAffected: %w", m.ID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the Mapping with the given ID.
func (r *Repo) Delete(ctx context.Context, id int) error {
	q := fmt.Sprintf(`DELETE FROM "RemotePathMappings" WHERE "Id" = %s`, datastore.Placeholder(r.db.Engine, 1))
	res, err := r.db.SQL.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("remotepathmappings: delete(%d): %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remotepathmappings: delete(%d) rowsAffected: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
