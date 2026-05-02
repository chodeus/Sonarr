// Package config is the Go port of NzbDrone.Core.Configuration's Config
// table layer (the key/value persisted store, NOT the static XML config
// at startup — that lives elsewhere). The shape mirrors src/NzbDrone.Core
// /Configuration/Config.cs: { Id, Key, Value } with Key UNIQUE.
package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
)

// Entry is one persisted config row. Mirrors Config.cs: { Id, Key, Value }.
type Entry struct {
	ID    int    `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ErrNotFound is returned by Get / GetByKey when no row matches.
var ErrNotFound = errors.New("config: not found")

// Repo is the CRUD wrapper over the Config table.
type Repo struct {
	db *datastore.DB
}

// NewRepo returns a Repo bound to the given DB. Rejects nil and non-main role.
func NewRepo(db *datastore.DB) (*Repo, error) {
	if db == nil || db.SQL == nil {
		return nil, errors.New("config: nil DB")
	}
	if db.Role != datastore.RoleMain {
		return nil, fmt.Errorf("config: expected RoleMain, got %q", db.Role)
	}
	return &Repo{db: db}, nil
}

// Set upserts a key/value pair. If a row with the given Key exists its
// Value is updated; otherwise a new row is inserted. The C# IConfigService
// .SetValue calls into this kind of upsert. Returns the post-write Entry
// with its ID populated.
func (r *Repo) Set(ctx context.Context, key, value string) (*Entry, error) {
	// Both engines support ON CONFLICT (SQLite 3.24+, Postgres 9.5+) which
	// gives us a clean upsert.
	q := fmt.Sprintf(
		`INSERT INTO "Config" ("Key","Value") VALUES (%s,%s)
		 ON CONFLICT ("Key") DO UPDATE SET "Value" = EXCLUDED."Value"
		 RETURNING "Id"`,
		datastore.Placeholder(r.db.Engine, 1), datastore.Placeholder(r.db.Engine, 2))
	e := &Entry{Key: key, Value: value}
	if err := r.db.SQL.QueryRowContext(ctx, q, key, value).Scan(&e.ID); err != nil {
		return nil, fmt.Errorf("config: set(%q): %w", key, err)
	}
	return e, nil
}

// Get returns the Entry with the given ID, or ErrNotFound.
func (r *Repo) Get(ctx context.Context, id int) (*Entry, error) {
	q := fmt.Sprintf(`SELECT "Id","Key","Value" FROM "Config" WHERE "Id" = %s`, datastore.Placeholder(r.db.Engine, 1))
	var e Entry
	switch err := r.db.SQL.QueryRowContext(ctx, q, id).Scan(&e.ID, &e.Key, &e.Value); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("config: get(%d): %w", id, err)
	}
	return &e, nil
}

// GetByKey returns the Entry whose Key matches exactly, or ErrNotFound.
// The Key column is UNIQUE so at most one row matches.
func (r *Repo) GetByKey(ctx context.Context, key string) (*Entry, error) {
	q := fmt.Sprintf(`SELECT "Id","Key","Value" FROM "Config" WHERE "Key" = %s`, datastore.Placeholder(r.db.Engine, 1))
	var e Entry
	switch err := r.db.SQL.QueryRowContext(ctx, q, key).Scan(&e.ID, &e.Key, &e.Value); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("config: getByKey(%q): %w", key, err)
	}
	return &e, nil
}

// All returns every Entry, ordered by Key ascending. The C# side returns
// rows in arbitrary order; key-ordered output is deterministic for tests
// and harmless for callers (the data is a flat lookup table).
func (r *Repo) All(ctx context.Context) ([]Entry, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `SELECT "Id","Key","Value" FROM "Config" ORDER BY "Key"`)
	if err != nil {
		return nil, fmt.Errorf("config: all: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Key, &e.Value); err != nil {
			return nil, fmt.Errorf("config: all scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("config: all rows: %w", err)
	}
	return out, nil
}

// Delete removes the Entry with the given Key. Returns ErrNotFound if
// no row matched. Delete-by-key (rather than by id) matches the C#
// IConfigService surface — callers think in keys, not ids.
func (r *Repo) Delete(ctx context.Context, key string) error {
	q := fmt.Sprintf(`DELETE FROM "Config" WHERE "Key" = %s`, datastore.Placeholder(r.db.Engine, 1))
	res, err := r.db.SQL.ExecContext(ctx, q, key)
	if err != nil {
		return fmt.Errorf("config: delete(%q): %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("config: delete(%q) rowsAffected: %w", key, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
