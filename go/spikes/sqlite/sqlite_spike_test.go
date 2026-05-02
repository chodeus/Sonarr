// Package sqlite_spike_test is a Phase 0.5 spike validating that
// modernc.org/sqlite (a pure-Go, cgo-free SQLite driver) can host the
// surface area Sonarr's C# Datastore uses against system.data.sqlite:
//
//   - WAL journal mode + busy timeout
//   - Foreign keys ON
//   - JSON1 (json_extract, json_set, json_array_length) — used heavily for
//     embedded-document storage in Sonarr (per-indexer settings, etc.)
//   - Common Table Expressions (recursive too)
//   - Unicode collation (NOCASE behavior on UTF-8 strings)
//   - Concurrent readers + single writer with transaction semantics
//
// The spike is self-contained: it creates an empty DB, applies a schema
// shaped like a representative Sonarr migration, and exercises the surfaces.
// A green run is the verdict that this driver is suitable for the port.
//
// See go/analysis/spikes/sqlite-modernc.md for the write-up.
package sqlite_spike_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// open mirrors the connection-string flags Sonarr uses on SQLite:
// WAL journal, foreign keys on, busy timeout, NOCASE-friendly collation.
func open(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
		filepath.Join(dir, "sonarr.db"))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func TestPragmasApplied(t *testing.T) {
	db := open(t)
	cases := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
	}
	for _, c := range cases {
		var got string
		if err := db.QueryRowContext(t.Context(), "PRAGMA "+c.pragma).Scan(&got); err != nil {
			t.Fatalf("pragma %s: %v", c.pragma, err)
		}
		if !strings.EqualFold(got, c.want) {
			t.Errorf("pragma %s: got %q, want %q", c.pragma, got, c.want)
		}
	}
}

// TestSchemaShape mirrors a representative Sonarr-style schema:
// integer primary keys, NOT NULL columns, foreign keys, and a JSON-blob
// column for embedded documents.
func TestSchemaShape(t *testing.T) {
	db := open(t)
	mustExec(t, db, `
		CREATE TABLE Series (
			Id INTEGER PRIMARY KEY AUTOINCREMENT,
			Title TEXT NOT NULL COLLATE NOCASE,
			TvdbId INTEGER NOT NULL UNIQUE,
			Added DATETIME NOT NULL,
			Tags TEXT NOT NULL DEFAULT '[]'
		);
	`)
	mustExec(t, db, `
		CREATE TABLE Episodes (
			Id INTEGER PRIMARY KEY AUTOINCREMENT,
			SeriesId INTEGER NOT NULL,
			SeasonNumber INTEGER NOT NULL,
			EpisodeNumber INTEGER NOT NULL,
			AirDateUtc DATETIME,
			Settings TEXT NOT NULL DEFAULT '{}',
			FOREIGN KEY (SeriesId) REFERENCES Series(Id) ON DELETE CASCADE
		);
	`)
	mustExec(t, db, `CREATE INDEX idx_episodes_series ON Episodes (SeriesId, SeasonNumber, EpisodeNumber);`)

	// Insert a row.
	mustExec(t, db, `INSERT INTO Series (Title, TvdbId, Added) VALUES (?, ?, ?)`,
		"The Wire", 79126, time.Now().UTC())

	// Foreign key cascade should work.
	mustExec(t, db, `INSERT INTO Episodes (SeriesId, SeasonNumber, EpisodeNumber) VALUES (1, 1, 1)`)
	mustExec(t, db, `DELETE FROM Series WHERE Id = 1`)
	var n int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM Episodes`).Scan(&n); err != nil {
		t.Fatalf("count episodes: %v", err)
	}
	if n != 0 {
		t.Errorf("expected cascade delete to remove episodes, got %d remaining", n)
	}

	// NOCASE collation on Title.
	mustExec(t, db, `INSERT INTO Series (Title, TvdbId, Added) VALUES (?, ?, ?)`, "Breaking Bad", 81189, time.Now().UTC())
	if err := db.QueryRowContext(t.Context(), `SELECT Id FROM Series WHERE Title = ?`, "BREAKING BAD").Scan(&n); err != nil {
		t.Fatalf("nocase lookup: %v", err)
	}
}

// TestJSON1Functions confirms json_extract / json_set / json_array_length
// — the JSON1 surface Sonarr uses for embedded-document operations.
func TestJSON1Functions(t *testing.T) {
	db := open(t)
	mustExec(t, db, `CREATE TABLE Indexers (Id INTEGER PRIMARY KEY, Settings TEXT NOT NULL)`)
	settings := `{"baseUrl":"https://example.com","apiKey":"x","categories":[5030,5040]}`
	mustExec(t, db, `INSERT INTO Indexers (Settings) VALUES (?)`, settings)

	var baseURL string
	if err := db.QueryRowContext(t.Context(),
		`SELECT json_extract(Settings, '$.baseUrl') FROM Indexers WHERE Id = 1`).Scan(&baseURL); err != nil {
		t.Fatalf("json_extract: %v", err)
	}
	if baseURL != "https://example.com" {
		t.Errorf("json_extract: got %q", baseURL)
	}

	var n int
	if err := db.QueryRowContext(t.Context(),
		`SELECT json_array_length(Settings, '$.categories') FROM Indexers WHERE Id = 1`).Scan(&n); err != nil {
		t.Fatalf("json_array_length: %v", err)
	}
	if n != 2 {
		t.Errorf("json_array_length: got %d, want 2", n)
	}

	mustExec(t, db, `UPDATE Indexers SET Settings = json_set(Settings, '$.apiKey', 'rotated') WHERE Id = 1`)
	var apiKey string
	if err := db.QueryRowContext(t.Context(),
		`SELECT json_extract(Settings, '$.apiKey') FROM Indexers WHERE Id = 1`).Scan(&apiKey); err != nil {
		t.Fatalf("json_extract after json_set: %v", err)
	}
	if apiKey != "rotated" {
		t.Errorf("json_set didn't update: got %q", apiKey)
	}
}

func TestRecursiveCTE(t *testing.T) {
	db := open(t)
	rows, err := db.QueryContext(t.Context(), `
		WITH RECURSIVE counter(n) AS (
			SELECT 1
			UNION ALL
			SELECT n + 1 FROM counter WHERE n < 5
		)
		SELECT n FROM counter
	`)
	if err != nil {
		t.Fatalf("query CTE: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var got []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, n)
	}
	if len(got) != 5 || got[0] != 1 || got[4] != 5 {
		t.Errorf("CTE: got %v", got)
	}
}

// TestConcurrentReadersSingleWriter exercises the WAL-mode pattern Sonarr
// relies on: many concurrent reads while a writer holds the write lock.
func TestConcurrentReadersSingleWriter(t *testing.T) {
	db := open(t)
	db.SetMaxOpenConns(8)

	mustExec(t, db, `CREATE TABLE Counter (V INTEGER NOT NULL)`)
	mustExec(t, db, `INSERT INTO Counter VALUES (0)`)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				var v int
				if err := db.QueryRowContext(ctx, `SELECT V FROM Counter`).Scan(&v); err != nil &&
					!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("read: %v", err)
					return
				}
			}
		}()
	}

	for i := 0; i < 100 && ctx.Err() == nil; i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE Counter SET V = V + 1`); err != nil {
			_ = tx.Rollback()
			t.Fatalf("update: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	cancel()
	wg.Wait()

	var v int
	if err := db.QueryRowContext(t.Context(), `SELECT V FROM Counter`).Scan(&v); err != nil {
		t.Fatalf("final read: %v", err)
	}
	if v == 0 {
		t.Errorf("counter never advanced")
	}
}
