package datastore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openSQLite(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(t.Context(), Config{
		Engine: EngineSQLite,
		Path:   filepath.Join(dir, "sonarr.db"),
	})
	if err != nil {
		t.Fatalf("Open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// pgDSN reads SONARR_PG_DSN — the same env var the spike uses.
// Tests that need it skip when it's unset, so a developer without a
// running Postgres can still run the SQLite suite.
func pgDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SONARR_PG_DSN")
	if dsn == "" {
		t.Skip("SONARR_PG_DSN not set; skipping Postgres test")
	}
	return dsn
}

func TestOpenSQLite_AppliesPragmas(t *testing.T) {
	t.Parallel()
	db := openSQLite(t)

	// The four pragmas we set in resolveDSN must round-trip back from
	// the engine — busy_timeout in particular silently causes deadlocks
	// under contention if it never applies.
	cases := []struct {
		pragma, want string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
		{"synchronous", "1"}, // NORMAL = 1
	}
	for _, c := range cases {
		var got string
		row := db.SQL.QueryRowContext(t.Context(), "PRAGMA "+c.pragma)
		if err := row.Scan(&got); err != nil {
			t.Fatalf("pragma %s: %v", c.pragma, err)
		}
		if got != c.want {
			t.Errorf("pragma %s = %q, want %q", c.pragma, got, c.want)
		}
	}
}

func TestOpenSQLite_BadConfigErrors(t *testing.T) {
	t.Parallel()
	if _, err := Open(t.Context(), Config{Engine: EngineSQLite}); err == nil {
		t.Fatal("expected error for missing path")
	}
	if _, err := Open(t.Context(), Config{Engine: "neither"}); err == nil {
		t.Fatal("expected error for unknown engine")
	}
}

func TestResolveDSN_SQLite(t *testing.T) {
	t.Parallel()
	driver, dsn, err := resolveDSN(Config{Engine: EngineSQLite, Path: "/tmp/x.db"})
	if err != nil {
		t.Fatalf("resolveDSN: %v", err)
	}
	if driver != "sqlite" {
		t.Errorf("driver = %q, want %q", driver, "sqlite")
	}
	for _, s := range []string{
		"file:/tmp/x.db",
		"_pragma=journal_mode%28WAL%29",
		"_pragma=foreign_keys%281%29",
		"_pragma=busy_timeout%285000%29",
		"_pragma=synchronous%28NORMAL%29",
	} {
		if !strings.Contains(dsn, s) {
			t.Errorf("dsn missing %q\nfull dsn: %s", s, dsn)
		}
	}
}

func TestResolveDSN_PostgresUnixSocket(t *testing.T) {
	t.Parallel()
	_, dsn, err := resolveDSN(Config{
		Engine:   EnginePostgres,
		Host:     "/tmp",
		User:     "dean",
		Database: "sonarr_test",
	})
	if err != nil {
		t.Fatalf("resolveDSN: %v", err)
	}
	for _, s := range []string{"postgres://dean@", "/sonarr_test", "host=%2Ftmp"} {
		if !strings.Contains(dsn, s) {
			t.Errorf("dsn missing %q\nfull dsn: %s", s, dsn)
		}
	}
}

func TestResolveDSN_PostgresTCPWithPort(t *testing.T) {
	t.Parallel()
	_, dsn, err := resolveDSN(Config{
		Engine:   EnginePostgres,
		Host:     "db.example.com",
		Port:     6543,
		User:     "u",
		Password: "p",
		Database: "x",
	})
	if err != nil {
		t.Fatalf("resolveDSN: %v", err)
	}
	if !strings.Contains(dsn, "u:p@db.example.com:6543/x") {
		t.Errorf("expected user:pass@host:port/db form\nfull dsn: %s", dsn)
	}
}

func TestOpenPostgres_PingsServer(t *testing.T) {
	t.Parallel()
	dsn := pgDSN(t)
	c, err := configFromSpikeDSN(dsn)
	if err != nil {
		t.Fatalf("configFromSpikeDSN(%q): %v", dsn, err)
	}
	db, err := Open(t.Context(), c)
	if err != nil {
		t.Fatalf("Open Postgres: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.SQL.PingContext(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// configFromSpikeDSN parses the "postgres:///<dbname>" idiom used by the
// pgx spike into a Config. Real callers compose Config directly from XML/env;
// this helper keeps test ergonomics close to the spike harness.
func configFromSpikeDSN(dsn string) (Config, error) {
	const prefix = "postgres:///"
	if !strings.HasPrefix(dsn, prefix) {
		return Config{}, errors.New("datastore_test: DSN must start with postgres:///")
	}
	dbname := dsn[len(prefix):]
	if dbname == "" {
		return Config{}, errors.New("datastore_test: DSN missing database name")
	}
	return Config{
		Engine:   EnginePostgres,
		Host:     "/tmp", // libpq default Unix-socket dir on macOS Homebrew.
		Database: dbname,
		User:     os.Getenv("USER"),
	}, nil
}
