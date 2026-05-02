// Package rootfolders is the Go port of NzbDrone.Core.RootFolders' RootFolder
// table layer. The shape mirrors src/NzbDrone.Core/RootFolders/RootFolder.cs:
// { Id, Path } with Path UNIQUE.
//
// The C# RootFolder type also has a non-persisted Accessible bool and
// FreeSpace long that the service computes at read time from the live
// filesystem. Those live in the service layer (yet to be ported), not
// the persistence layer this package owns.
package rootfolders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
)

// RootFolder is one persisted row.
type RootFolder struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

// ErrNotFound is returned by Get / GetByPath when no row matches.
var ErrNotFound = errors.New("rootfolders: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	db *datastore.DB
}

// NewRepo returns a Repo bound to the given DB. Rejects nil and non-main role.
func NewRepo(db *datastore.DB) (*Repo, error) {
	if db == nil || db.SQL == nil {
		return nil, errors.New("rootfolders: nil DB")
	}
	if db.Role != datastore.RoleMain {
		return nil, fmt.Errorf("rootfolders: expected RoleMain, got %q", db.Role)
	}
	return &Repo{db: db}, nil
}

// Insert persists a new RootFolder and populates rf.ID.
func (r *Repo) Insert(ctx context.Context, rf *RootFolder) (*RootFolder, error) {
	if rf == nil {
		return nil, errors.New("rootfolders: nil RootFolder")
	}
	q := fmt.Sprintf(`INSERT INTO "RootFolders" ("Path") VALUES (%s) RETURNING "Id"`, datastore.Placeholder(r.db.Engine, 1))
	if err := r.db.SQL.QueryRowContext(ctx, q, rf.Path).Scan(&rf.ID); err != nil {
		return nil, fmt.Errorf("rootfolders: insert: %w", err)
	}
	return rf, nil
}

// Get returns the RootFolder with the given ID or ErrNotFound.
func (r *Repo) Get(ctx context.Context, id int) (*RootFolder, error) {
	q := fmt.Sprintf(`SELECT "Id","Path" FROM "RootFolders" WHERE "Id" = %s`, datastore.Placeholder(r.db.Engine, 1))
	var rf RootFolder
	switch err := r.db.SQL.QueryRowContext(ctx, q, id).Scan(&rf.ID, &rf.Path); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("rootfolders: get(%d): %w", id, err)
	}
	return &rf, nil
}

// GetByPath returns the RootFolder whose Path matches exactly, or
// ErrNotFound. Path is UNIQUE so at most one row matches.
func (r *Repo) GetByPath(ctx context.Context, path string) (*RootFolder, error) {
	q := fmt.Sprintf(`SELECT "Id","Path" FROM "RootFolders" WHERE "Path" = %s`, datastore.Placeholder(r.db.Engine, 1))
	var rf RootFolder
	switch err := r.db.SQL.QueryRowContext(ctx, q, path).Scan(&rf.ID, &rf.Path); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("rootfolders: getByPath(%q): %w", path, err)
	}
	return &rf, nil
}

// All returns every RootFolder, ordered by Id.
func (r *Repo) All(ctx context.Context) ([]RootFolder, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `SELECT "Id","Path" FROM "RootFolders" ORDER BY "Id"`)
	if err != nil {
		return nil, fmt.Errorf("rootfolders: all: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RootFolder
	for rows.Next() {
		var rf RootFolder
		if err := rows.Scan(&rf.ID, &rf.Path); err != nil {
			return nil, fmt.Errorf("rootfolders: all scan: %w", err)
		}
		out = append(out, rf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rootfolders: all rows: %w", err)
	}
	return out, nil
}

// Delete removes the RootFolder with the given ID. Returns ErrNotFound
// if no row matched.
func (r *Repo) Delete(ctx context.Context, id int) error {
	q := fmt.Sprintf(`DELETE FROM "RootFolders" WHERE "Id" = %s`, datastore.Placeholder(r.db.Engine, 1))
	res, err := r.db.SQL.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("rootfolders: delete(%d): %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rootfolders: delete(%d) rowsAffected: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
