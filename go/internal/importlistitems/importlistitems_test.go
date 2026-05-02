package importlistitems_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/importlistitems"
)

func ptrInt(i int) *int              { return &i }
func ptrString(s string) *string     { return &s }
func ptrTime(t time.Time) *time.Time { return &t }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := importlistitems.NewRepo(db)
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

	releaseDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	full := &importlistitems.Item{
		ImportListID: 1,
		Title:        "The Mandalorian",
		TvdbID:       361753,
		Year:         ptrInt(2019),
		TmdbID:       ptrInt(82856),
		ImdbID:       ptrString("tt8111088"),
		MalID:        ptrInt(0),
		AniListID:    ptrInt(0),
		ReleaseDate:  ptrTime(releaseDate),
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	// Sparse — most nullables nil, exercises NULL persistence.
	sparse := &importlistitems.Item{
		ImportListID: 1,
		Title:        "Some Anime",
		TvdbID:       999,
	}
	if _, err := repo.Insert(t.Context(), sparse); err != nil {
		t.Fatalf("Insert sparse: %v", err)
	}

	// Different list, different items.
	other := &importlistitems.Item{
		ImportListID: 2,
		Title:        "Foundation",
		TvdbID:       360299,
	}
	if _, err := repo.Insert(t.Context(), other); err != nil {
		t.Fatalf("Insert other-list: %v", err)
	}

	gotFull, err := repo.Get(t.Context(), full.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotFull.Year == nil || *gotFull.Year != 2019 {
		t.Errorf("Year round-trip: %v", gotFull.Year)
	}
	if gotFull.ImdbID == nil || *gotFull.ImdbID != "tt8111088" {
		t.Errorf("ImdbID round-trip: %v", gotFull.ImdbID)
	}
	if gotFull.ReleaseDate == nil || !gotFull.ReleaseDate.Equal(releaseDate) {
		t.Errorf("ReleaseDate round-trip: %v", gotFull.ReleaseDate)
	}

	gotSparse, err := repo.Get(t.Context(), sparse.ID)
	if err != nil {
		t.Fatalf("Get sparse: %v", err)
	}
	if gotSparse.Year != nil || gotSparse.TmdbID != nil || gotSparse.ImdbID != nil ||
		gotSparse.MalID != nil || gotSparse.AniListID != nil || gotSparse.ReleaseDate != nil {
		t.Errorf("sparse nullables not nil: %+v", gotSparse)
	}

	// AllByImportListID — list 1 has 2 items, list 2 has 1.
	listOne, err := repo.AllByImportListID(t.Context(), 1)
	if err != nil {
		t.Fatalf("AllByImportListID(1): %v", err)
	}
	if len(listOne) != 2 {
		t.Errorf("listOne: got %d, want 2", len(listOne))
	}
	listTwo, err := repo.AllByImportListID(t.Context(), 2)
	if err != nil {
		t.Fatalf("AllByImportListID(2): %v", err)
	}
	if len(listTwo) != 1 || listTwo[0].Title != "Foundation" {
		t.Errorf("listTwo: %+v", listTwo)
	}
	listEmpty, err := repo.AllByImportListID(t.Context(), 999)
	if err != nil {
		t.Fatalf("AllByImportListID(999): %v", err)
	}
	if len(listEmpty) != 0 {
		t.Errorf("listEmpty: got %d, want 0", len(listEmpty))
	}

	// Update — set previously-nil ImdbID, clear Year.
	gotSparse.ImdbID = ptrString("tt9999999")
	gotSparse.Year = nil
	if err := repo.Update(t.Context(), gotSparse); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), gotSparse.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.ImdbID == nil || *post.ImdbID != "tt9999999" || post.Year != nil {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Update(t.Context(), &importlistitems.Item{ID: 99999, ImportListID: 1, Title: "x", TvdbID: 1}); !errors.Is(err, importlistitems.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); !errors.Is(err, importlistitems.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
