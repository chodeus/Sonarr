package users_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/users"
)

func ptrString(s string) *string { return &s }
func ptrInt(i int) *int          { return &i }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := users.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All initial: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("All initial: got %d, want 0", len(all))
	}

	// Modern row: PBKDF2 with non-nil Salt + Iterations.
	modern := &users.User{
		Identifier: "11111111-2222-3333-4444-555555555555",
		Username:   "admin",
		Password:   "pbkdf2-hash-bytes",
		Salt:       ptrString("salt-bytes"),
		Iterations: ptrInt(10000),
	}
	if _, err := repo.Insert(t.Context(), modern); err != nil {
		t.Fatalf("Insert modern: %v", err)
	}

	// Legacy row: SHA-256 with nil Salt+Iterations (lazy upgrade pending).
	legacy := &users.User{
		Identifier: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Username:   "legacy",
		Password:   "sha256-hash",
	}
	if _, err := repo.Insert(t.Context(), legacy); err != nil {
		t.Fatalf("Insert legacy: %v", err)
	}

	got, err := repo.GetByUsername(t.Context(), "admin")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if got.Salt == nil || *got.Salt != "salt-bytes" {
		t.Errorf("Salt round-trip: %v", got.Salt)
	}
	if got.Iterations == nil || *got.Iterations != 10000 {
		t.Errorf("Iterations round-trip: %v", got.Iterations)
	}

	gotLegacy, err := repo.GetByUsername(t.Context(), "legacy")
	if err != nil {
		t.Fatalf("GetByUsername(legacy): %v", err)
	}
	if gotLegacy.Salt != nil || gotLegacy.Iterations != nil {
		t.Errorf("legacy nullables: salt=%v iterations=%v, want both nil", gotLegacy.Salt, gotLegacy.Iterations)
	}

	gotByID, err := repo.GetByIdentifier(t.Context(), "11111111-2222-3333-4444-555555555555")
	if err != nil {
		t.Fatalf("GetByIdentifier: %v", err)
	}
	if gotByID.ID != modern.ID {
		t.Errorf("GetByIdentifier: got %d, want %d", gotByID.ID, modern.ID)
	}

	if _, err := repo.GetByUsername(t.Context(), "no-such"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("GetByUsername(missing): %v", err)
	}
	if _, err := repo.GetByIdentifier(t.Context(), "no-such"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("GetByIdentifier(missing): %v", err)
	}

	// UNIQUE constraints on Identifier + Username.
	if _, err := repo.Insert(t.Context(), &users.User{Identifier: modern.Identifier, Username: "other", Password: "x"}); err == nil {
		t.Errorf("Insert duplicate Identifier: expected error")
	}
	if _, err := repo.Insert(t.Context(), &users.User{Identifier: "different-id", Username: "admin", Password: "x"}); err == nil {
		t.Errorf("Insert duplicate Username: expected error")
	}

	// Simulate the lazy-upgrade flow: legacy row gets re-hashed and gains
	// Salt + Iterations after the next successful login.
	gotLegacy.Password = "pbkdf2-bytes"
	gotLegacy.Salt = ptrString("new-salt")
	gotLegacy.Iterations = ptrInt(10000)
	if err := repo.Update(t.Context(), gotLegacy); err != nil {
		t.Fatalf("Update legacy: %v", err)
	}
	postLegacy, err := repo.Get(t.Context(), gotLegacy.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if postLegacy.Salt == nil || *postLegacy.Salt != "new-salt" || postLegacy.Iterations == nil {
		t.Errorf("post-Update legacy: %+v", postLegacy)
	}

	if err := repo.Update(t.Context(), &users.User{ID: 99999, Identifier: "x", Username: "x", Password: "x"}); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	if err := repo.Delete(t.Context(), modern.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), modern.ID); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("Get(missing): %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
