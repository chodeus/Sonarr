// Package datastoretest provides test-only helpers shared across packages
// that exercise repositories at HEAD against real engines. The helpers
// hide the boilerplate of "open a fresh DB, migrate to head, hand it back
// with t.Cleanup wired" so each package's tests can focus on its repo.
//
// The package name has the conventional "test" suffix; per Go convention
// it can only be imported from _test.go files.
package datastoretest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
)

// OpenSQLite returns a fresh SQLite DB with all migrations applied.
// The caller doesn't need to defer Close — t.Cleanup is wired.
func OpenSQLite(t *testing.T) *datastore.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := datastore.Open(t.Context(), datastore.Config{
		Engine: datastore.EngineSQLite,
		Path:   filepath.Join(dir, "sonarr.db"),
	})
	if err != nil {
		t.Fatalf("Open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := datastore.Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate SQLite: %v", err)
	}
	return db
}

// OpenSQLiteLog returns a RoleLog SQLite DB with the log-DB migrations
// applied — the separate log database (Logs / UpdateHistory tables).
func OpenSQLiteLog(t *testing.T) *datastore.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := datastore.Open(t.Context(), datastore.Config{
		Engine: datastore.EngineSQLite,
		Role:   datastore.RoleLog,
		Path:   filepath.Join(dir, "logs.db"),
	})
	if err != nil {
		t.Fatalf("Open SQLite log: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := datastore.Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate SQLite log: %v", err)
	}
	return db
}

// OpenPostgres returns a Postgres DB with all migrations applied. It
// skips the calling test if SONARR_PG_DSN is unset. The DB's public
// schema is dropped + recreated each call so tests are hermetic. Cannot
// be used with t.Parallel within the same package because tests share
// the spike DB.
func OpenPostgres(t *testing.T) *datastore.DB {
	t.Helper()
	dsn := os.Getenv("SONARR_PG_DSN")
	if dsn == "" {
		t.Skip("SONARR_PG_DSN not set; skipping Postgres test")
	}
	const prefix = "postgres:///"
	if !strings.HasPrefix(dsn, prefix) {
		t.Fatalf("DSN must start with postgres:///")
	}
	dbname := dsn[len(prefix):]
	db, err := datastore.Open(t.Context(), datastore.Config{
		Engine:   datastore.EnginePostgres,
		Host:     "/tmp",
		Database: dbname,
		User:     os.Getenv("USER"),
	})
	if err != nil {
		t.Fatalf("Open Postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{`DROP SCHEMA IF EXISTS public CASCADE`, `CREATE SCHEMA public`} {
		if _, err := db.SQL.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("reset Postgres %q: %v", q, err)
		}
	}
	if err := datastore.Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate Postgres: %v", err)
	}
	return db
}
