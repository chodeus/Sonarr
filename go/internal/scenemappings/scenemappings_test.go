package scenemappings_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/scenemappings"
)

func ptrInt(i int) *int { return &i }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := scenemappings.NewRepo(db)
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

	// Full row exercising every nullable.
	full := &scenemappings.Mapping{
		TvdbID:            12345,
		SeasonNumber:      ptrInt(2),
		SearchTerm:        "Anime Title",
		ParseTerm:         "anime.title",
		Title:             "Anime Title (Subbed)",
		Type:              "TvdbId",
		SceneSeasonNumber: ptrInt(1),
		FilterRegex:       `[Group]`,
		SceneOrigin:       "tvdb",
		SearchMode:        ptrInt(1),
		Comment:           "Manual override for season 2",
		MappingID:         "mapping-abc",
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	// Sparse — only NOT-NULL columns set.
	sparse := &scenemappings.Mapping{
		TvdbID:     12345,
		SearchTerm: "Different Search",
		ParseTerm:  "different.parse",
	}
	if _, err := repo.Insert(t.Context(), sparse); err != nil {
		t.Fatalf("Insert sparse: %v", err)
	}

	// Different TvdbId.
	other := &scenemappings.Mapping{
		TvdbID:     99999,
		SearchTerm: "Other Show",
		ParseTerm:  "other.show",
	}
	if _, err := repo.Insert(t.Context(), other); err != nil {
		t.Fatalf("Insert other: %v", err)
	}

	gotFull, err := repo.Get(t.Context(), full.ID)
	if err != nil {
		t.Fatalf("Get full: %v", err)
	}
	if gotFull.SeasonNumber == nil || *gotFull.SeasonNumber != 2 {
		t.Errorf("SeasonNumber: %v", gotFull.SeasonNumber)
	}
	if gotFull.Title != "Anime Title (Subbed)" || gotFull.MappingID != "mapping-abc" {
		t.Errorf("string columns: %+v", gotFull)
	}

	gotSparse, err := repo.Get(t.Context(), sparse.ID)
	if err != nil {
		t.Fatalf("Get sparse: %v", err)
	}
	if gotSparse.SeasonNumber != nil || gotSparse.SceneSeasonNumber != nil ||
		gotSparse.SearchMode != nil {
		t.Errorf("sparse nullable ints not nil: %+v", gotSparse)
	}
	if gotSparse.Title != "" || gotSparse.Type != "" || gotSparse.FilterRegex != "" ||
		gotSparse.SceneOrigin != "" || gotSparse.Comment != "" || gotSparse.MappingID != "" {
		t.Errorf("sparse nullable strings not empty: %+v", gotSparse)
	}

	// AllByTvdbID — TvdbId 12345 has 2 rows, 99999 has 1.
	byTvdb12345, err := repo.AllByTvdbID(t.Context(), 12345)
	if err != nil {
		t.Fatalf("AllByTvdbID(12345): %v", err)
	}
	if len(byTvdb12345) != 2 {
		t.Errorf("AllByTvdbID(12345): got %d, want 2", len(byTvdb12345))
	}
	byTvdb99999, err := repo.AllByTvdbID(t.Context(), 99999)
	if err != nil {
		t.Fatalf("AllByTvdbID(99999): %v", err)
	}
	if len(byTvdb99999) != 1 {
		t.Errorf("AllByTvdbID(99999): got %d, want 1", len(byTvdb99999))
	}
	byTvdbMissing, err := repo.AllByTvdbID(t.Context(), 0)
	if err != nil {
		t.Fatalf("AllByTvdbID(0): %v", err)
	}
	if len(byTvdbMissing) != 0 {
		t.Errorf("AllByTvdbID(0): got %d, want 0", len(byTvdbMissing))
	}

	// Update — set previously-nil int + previously-empty string.
	gotSparse.SeasonNumber = ptrInt(5)
	gotSparse.Comment = "Now annotated"
	if err := repo.Update(t.Context(), gotSparse); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), gotSparse.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.SeasonNumber == nil || *post.SeasonNumber != 5 || post.Comment != "Now annotated" {
		t.Errorf("post-Update: %+v", post)
	}

	// DeleteAll — refresh-pattern that the data-augmentation job uses.
	if err := repo.DeleteAll(t.Context()); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	all, err = repo.All(t.Context())
	if err != nil {
		t.Fatalf("All after DeleteAll: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("All after DeleteAll: got %d, want 0", len(all))
	}

	if err := repo.Update(t.Context(), &scenemappings.Mapping{ID: 99999, TvdbID: 1, SearchTerm: "x", ParseTerm: "x"}); !errors.Is(err, scenemappings.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
