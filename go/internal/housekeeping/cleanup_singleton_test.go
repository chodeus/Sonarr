package housekeeping_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/housekeeping"
)

// rowCount returns the number of rows in `table` for the test
// assertion. Avoids importing every singleton table's repo.
func rowCount(ctx context.Context, t *testing.T, db *datastore.DB, table string) int {
	t.Helper()
	q := fmt.Sprintf(`SELECT COUNT(*) FROM %q`, table)
	var n int
	if err := db.SQL.QueryRowContext(ctx, q).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestCleanupAdditionalNamingSpecs_Sqlite(t *testing.T) {
	t.Parallel()
	runSingletonNamingSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupAdditionalNamingSpecs_Postgres(t *testing.T) {
	runSingletonNamingSuite(t, datastoretest.OpenPostgres(t))
}

func runSingletonNamingSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	// Seed two duplicate NamingConfig rows. Migration-001 inserts a
	// default row; the singleton sweep should leave exactly one
	// (whichever id wins under LIMIT 1) regardless of how many we
	// add.
	insert := `INSERT INTO "NamingConfig"
	  ("MultiEpisodeStyle","RenameEpisodes","StandardEpisodeFormat","DailyEpisodeFormat","SeasonFolderFormat","SeriesFolderFormat","AnimeEpisodeFormat","ReplaceIllegalCharacters","ColonReplacementFormat","CustomColonReplacementFormat","SpecialsFolderFormat")
	  VALUES (0, false, 'std', 'daily', 'season', 'series', 'anime', true, 0, '', 'specials')`
	for i := 0; i < 2; i++ {
		if _, err := db.SQL.ExecContext(t.Context(), insert); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	preCount := rowCount(t.Context(), t, db, "NamingConfig")
	if preCount < 2 {
		t.Fatalf("pre-count %d, need at least 2 to exercise sweep", preCount)
	}

	task, err := housekeeping.NewCleanupAdditionalNamingSpecs(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if got := rowCount(t.Context(), t, db, "NamingConfig"); got != 1 {
		t.Errorf("post-count: %d, want 1", got)
	}

	// Idempotency: running again is a no-op.
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean(rerun): %v", err)
	}
	if got := rowCount(t.Context(), t, db, "NamingConfig"); got != 1 {
		t.Errorf("post-rerun count: %d, want 1", got)
	}
}

func TestCleanupAdditionalUsers_Sqlite(t *testing.T) {
	t.Parallel()
	runSingletonUsersSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupAdditionalUsers_Postgres(t *testing.T) {
	runSingletonUsersSuite(t, datastoretest.OpenPostgres(t))
}

func runSingletonUsersSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	// Seed three Users rows; sweep should leave exactly one.
	insert := `INSERT INTO "Users" ("Identifier","Username","Password","Salt","Iterations") VALUES ($1, $2, 'p', 's', 1)`
	if db.Engine == datastore.EngineSQLite {
		insert = `INSERT INTO "Users" ("Identifier","Username","Password","Salt","Iterations") VALUES (?, ?, 'p', 's', 1)`
	}
	for i, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := db.SQL.ExecContext(t.Context(), insert, fmt.Sprintf("id-%d", i), name); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	preCount := rowCount(t.Context(), t, db, "Users")
	if preCount != 3 {
		t.Fatalf("pre-count %d, want 3", preCount)
	}

	task, err := housekeeping.NewCleanupAdditionalUsers(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if got := rowCount(t.Context(), t, db, "Users"); got != 1 {
		t.Errorf("post-count: %d, want 1", got)
	}
}

func TestNewSingletonHousekeepers_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupAdditionalNamingSpecs(nil, nil); err == nil {
		t.Errorf("AdditionalNamingSpecs(nil): want error")
	}
	if _, err := housekeeping.NewCleanupAdditionalUsers(nil, nil); err == nil {
		t.Errorf("AdditionalUsers(nil): want error")
	}
}
