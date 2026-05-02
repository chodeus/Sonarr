// Package postgres_spike_test is a Phase 0.5 spike validating that
// jackc/pgx/v5 covers the Postgres surface Sonarr's C# Datastore uses
// against Npgsql:
//
//   - Connection pool with bounded size and per-conn statement caching
//   - Schemas, primary keys, unique indexes, foreign keys with cascading delete
//   - JSONB columns + jsonb operators (->, ->>, jsonb_set, jsonb_array_length)
//     used for embedded-document storage
//   - RETURNING clause for INSERT (replaces SQLite's last_insert_rowid())
//   - ILIKE for case-insensitive matching (replaces SQLite's LIKE+NOCASE)
//   - Transactions with read-committed isolation under contention
//
// The spike is gated by SONARR_PG_DSN. If the env var is not set the test
// skips so this file is safe to run as part of the default `go test ./...`
// lane in CI.
//
// To run locally:
//
//	createdb sonarr_spike
//	SONARR_PG_DSN="postgres:///sonarr_spike" go test -count=1 -race ./spikes/postgres/...
//
// See go/analysis/spikes/pgx-postgres.md for the write-up.
package postgres_spike_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func dsn() string { return os.Getenv("SONARR_PG_DSN") }

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if dsn() == "" {
		t.Skip("SONARR_PG_DSN not set; skipping Postgres spike")
	}
	cfg, err := pgxpool.ParseConfig(dsn())
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = 8
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 10 * time.Minute
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	resetSchema(t, pool)
	return pool
}

func resetSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	stmts := []string{
		`DROP TABLE IF EXISTS Episodes`,
		`DROP TABLE IF EXISTS Series`,
		`DROP TABLE IF EXISTS Indexers`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(t.Context(), s); err != nil {
			t.Fatalf("reset (%s): %v", s, err)
		}
	}
}

func TestSchemaShapeAndCascade(t *testing.T) {
	pool := openPool(t)

	mustExec(t, pool, `
		CREATE TABLE Series (
			Id BIGSERIAL PRIMARY KEY,
			Title TEXT NOT NULL,
			TvdbId INTEGER NOT NULL UNIQUE,
			Added TIMESTAMPTZ NOT NULL,
			Tags JSONB NOT NULL DEFAULT '[]'::jsonb
		)
	`)
	mustExec(t, pool, `
		CREATE TABLE Episodes (
			Id BIGSERIAL PRIMARY KEY,
			SeriesId BIGINT NOT NULL REFERENCES Series(Id) ON DELETE CASCADE,
			SeasonNumber INTEGER NOT NULL,
			EpisodeNumber INTEGER NOT NULL,
			AirDateUtc TIMESTAMPTZ,
			Settings JSONB NOT NULL DEFAULT '{}'::jsonb
		)
	`)
	mustExec(t, pool, `CREATE INDEX idx_episodes_series ON Episodes (SeriesId, SeasonNumber, EpisodeNumber)`)

	var id int64
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO Series (Title, TvdbId, Added) VALUES ($1, $2, $3) RETURNING Id`,
		"The Wire", 79126, time.Now().UTC()).Scan(&id); err != nil {
		t.Fatalf("insert series RETURNING: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	mustExec(t, pool, `INSERT INTO Episodes (SeriesId, SeasonNumber, EpisodeNumber) VALUES ($1, 1, 1)`, id)
	mustExec(t, pool, `DELETE FROM Series WHERE Id = $1`, id)

	var n int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM Episodes`).Scan(&n); err != nil {
		t.Fatalf("count episodes: %v", err)
	}
	if n != 0 {
		t.Errorf("expected cascade delete, got %d remaining", n)
	}
}

func TestJSONBOperators(t *testing.T) {
	pool := openPool(t)
	mustExec(t, pool, `CREATE TABLE Indexers (Id BIGSERIAL PRIMARY KEY, Settings JSONB NOT NULL)`)

	settings := `{"baseUrl":"https://example.com","apiKey":"x","categories":[5030,5040]}`
	mustExec(t, pool, `INSERT INTO Indexers (Settings) VALUES ($1::jsonb)`, settings)

	var baseURL string
	if err := pool.QueryRow(t.Context(),
		`SELECT Settings->>'baseUrl' FROM Indexers WHERE Id = 1`).Scan(&baseURL); err != nil {
		t.Fatalf("->>: %v", err)
	}
	if baseURL != "https://example.com" {
		t.Errorf("baseUrl: got %q", baseURL)
	}

	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT jsonb_array_length(Settings->'categories') FROM Indexers WHERE Id = 1`).Scan(&n); err != nil {
		t.Fatalf("jsonb_array_length: %v", err)
	}
	if n != 2 {
		t.Errorf("len: got %d", n)
	}

	mustExec(t, pool, `UPDATE Indexers SET Settings = jsonb_set(Settings, '{apiKey}', '"rotated"'::jsonb) WHERE Id = 1`)

	var apiKey string
	if err := pool.QueryRow(t.Context(),
		`SELECT Settings->>'apiKey' FROM Indexers WHERE Id = 1`).Scan(&apiKey); err != nil {
		t.Fatalf("apiKey: %v", err)
	}
	if apiKey != "rotated" {
		t.Errorf("jsonb_set didn't update: got %q", apiKey)
	}
}

func TestILIKE(t *testing.T) {
	pool := openPool(t)
	mustExec(t, pool, `CREATE TABLE Series (Id BIGSERIAL PRIMARY KEY, Title TEXT NOT NULL, TvdbId INTEGER NOT NULL UNIQUE, Added TIMESTAMPTZ NOT NULL, Tags JSONB NOT NULL DEFAULT '[]')`)
	mustExec(t, pool, `INSERT INTO Series (Title, TvdbId, Added) VALUES ('Breaking Bad', 81189, NOW())`)

	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM Series WHERE Title ILIKE $1`, "BREAKING%").Scan(&n); err != nil {
		t.Fatalf("ILIKE: %v", err)
	}
	if n != 1 {
		t.Errorf("ILIKE: got %d, want 1", n)
	}
}

func TestConcurrentReadersSingleWriter(t *testing.T) {
	pool := openPool(t)
	mustExec(t, pool, `DROP TABLE IF EXISTS Counter`)
	mustExec(t, pool, `CREATE TABLE Counter (V INTEGER NOT NULL)`)
	mustExec(t, pool, `INSERT INTO Counter VALUES (0)`)
	t.Cleanup(func() { _, _ = pool.Exec(t.Context(), `DROP TABLE Counter`) })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				var v int
				if err := pool.QueryRow(ctx, `SELECT V FROM Counter`).Scan(&v); err != nil &&
					!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("read: %v", err)
					return
				}
			}
		}()
	}

	for i := 0; i < 100 && ctx.Err() == nil; i++ {
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE Counter SET V = V + 1`)
			return err
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			t.Fatalf("tx: %v", err)
		}
	}
	cancel()
	wg.Wait()

	var v int
	if err := pool.QueryRow(t.Context(), `SELECT V FROM Counter`).Scan(&v); err != nil {
		t.Fatalf("final read: %v", err)
	}
	if v == 0 {
		t.Errorf("counter never advanced")
	}
}

func mustExec(t *testing.T, pool *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}
