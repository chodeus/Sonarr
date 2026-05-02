package tags_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/tags"
)

func openSQLiteAtHead(t *testing.T) *datastore.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := datastore.Open(t.Context(), datastore.Config{
		Engine: datastore.EngineSQLite,
		Path:   filepath.Join(dir, "sonarr.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := datastore.Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func openPostgresAtHead(t *testing.T) *datastore.DB {
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
	// Wipe public so each test starts hermetic.
	for _, q := range []string{`DROP SCHEMA IF EXISTS public CASCADE`, `CREATE SCHEMA public`} {
		if _, err := db.SQL.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("reset %q: %v", q, err)
		}
	}
	if err := datastore.Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// runRepoSuite is the shared assertion set, parameterized by engine. Each
// engine runs the suite in its own (sub)test against its own DB.
func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := tags.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// Empty: All should return nil/empty.
	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All empty: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("All on empty DB: got %d rows, want 0", len(all))
	}

	// Insert two tags; Id is auto-generated and populated on the struct.
	a := &tags.Tag{Label: "anime"}
	if _, err := repo.Insert(t.Context(), a); err != nil {
		t.Fatalf("Insert anime: %v", err)
	}
	if a.ID == 0 {
		t.Errorf("Insert: ID not populated")
	}
	b := &tags.Tag{Label: "documentary"}
	if _, err := repo.Insert(t.Context(), b); err != nil {
		t.Fatalf("Insert documentary: %v", err)
	}
	if b.ID == a.ID {
		t.Errorf("Insert: IDs collided (%d == %d)", a.ID, b.ID)
	}

	// Get by ID round-trips.
	got, err := repo.Get(t.Context(), a.ID)
	if err != nil {
		t.Fatalf("Get(%d): %v", a.ID, err)
	}
	if got.Label != "anime" {
		t.Errorf("Get: Label = %q, want anime", got.Label)
	}

	// Get with bogus id returns ErrNotFound.
	if _, err := repo.Get(t.Context(), 999_999); !errors.Is(err, tags.ErrNotFound) {
		t.Errorf("Get(999999): err = %v, want ErrNotFound", err)
	}

	// GetByLabel.
	gotByLabel, err := repo.GetByLabel(t.Context(), "documentary")
	if err != nil {
		t.Fatalf("GetByLabel: %v", err)
	}
	if gotByLabel.ID != b.ID {
		t.Errorf("GetByLabel: ID = %d, want %d", gotByLabel.ID, b.ID)
	}
	if _, err := repo.GetByLabel(t.Context(), "no-such-tag"); !errors.Is(err, tags.ErrNotFound) {
		t.Errorf("GetByLabel(missing): err = %v, want ErrNotFound", err)
	}

	// All returns both rows ordered by Id.
	all, err = repo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 || all[0].ID != a.ID || all[1].ID != b.ID {
		t.Errorf("All: got %v, want [{%d anime} {%d documentary}]", all, a.ID, b.ID)
	}

	// Update changes the label and survives a re-Get.
	a.Label = "anime-updated"
	if err := repo.Update(t.Context(), a); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.Get(t.Context(), a.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.Label != "anime-updated" {
		t.Errorf("post-Update Label = %q, want anime-updated", got.Label)
	}

	// Update on missing row returns ErrNotFound.
	if err := repo.Update(t.Context(), &tags.Tag{ID: 999_999, Label: "ghost"}); !errors.Is(err, tags.ErrNotFound) {
		t.Errorf("Update(missing): err = %v, want ErrNotFound", err)
	}

	// Delete the first; Get now returns ErrNotFound; All returns one row.
	if err := repo.Delete(t.Context(), a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(t.Context(), a.ID); !errors.Is(err, tags.ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(t.Context(), a.ID); !errors.Is(err, tags.ErrNotFound) {
		t.Errorf("Delete idempotent: err = %v, want ErrNotFound", err)
	}
	all, err = repo.All(t.Context())
	if err != nil {
		t.Fatalf("All after Delete: %v", err)
	}
	if len(all) != 1 || all[0].ID != b.ID {
		t.Errorf("All after Delete: got %v, want [{%d documentary}]", all, b.ID)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, openSQLiteAtHead(t))
}

func TestRepo_Postgres(t *testing.T) {
	// Cannot t.Parallel — shares spike DB and rebuilds public schema.
	runRepoSuite(t, openPostgresAtHead(t))
}

func TestRepo_RejectsLogRole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logDB, err := datastore.Open(t.Context(), datastore.Config{
		Engine: datastore.EngineSQLite,
		Role:   datastore.RoleLog,
		Path:   filepath.Join(dir, "logs.db"),
	})
	if err != nil {
		t.Fatalf("Open log: %v", err)
	}
	t.Cleanup(func() { _ = logDB.Close() })
	if _, err := tags.NewRepo(logDB); err == nil {
		t.Error("NewRepo accepted RoleLog DB; want error")
	}
}

func TestRepo_RejectsNilDB(t *testing.T) {
	t.Parallel()
	if _, err := tags.NewRepo(nil); err == nil {
		t.Error("NewRepo accepted nil DB; want error")
	}
}

// TestUniqueLabelConstraint exercises the IX_Tags_Label unique constraint
// from migration 079. Inserting a duplicate label must surface as a SQL
// error (not silently succeed). Concrete error class is engine-specific,
// so we just assert err is non-nil.
func TestRepo_UniqueLabel_SQLite(t *testing.T) {
	t.Parallel()
	db := openSQLiteAtHead(t)
	repo, _ := tags.NewRepo(db)
	if _, err := repo.Insert(t.Context(), &tags.Tag{Label: "dup"}); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	if _, err := repo.Insert(t.Context(), &tags.Tag{Label: "dup"}); err == nil {
		t.Error("duplicate Insert succeeded; want UNIQUE constraint error")
	}
}
