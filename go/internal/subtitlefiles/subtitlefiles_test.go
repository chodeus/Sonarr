package subtitlefiles_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/subtitlefiles"
)

func ptrTime(t time.Time) *time.Time { return &t }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := subtitlefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)

	full := &subtitlefiles.SubtitleFile{
		SeriesID:      50,
		SeasonNumber:  1,
		EpisodeFileID: 100,
		RelativePath:  "Season 1/Foo.S01E01.en.forced.srt",
		Extension:     ".srt",
		Added:         ptrTime(now),
		LastUpdated:   now,
		Language:      1,
		LanguageTags:  []string{"forced", "sdh"},
		Title:         "Director's Cut",
		Copy:          0,
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}
	sparse := &subtitlefiles.SubtitleFile{
		SeriesID:      50,
		SeasonNumber:  1,
		EpisodeFileID: 100,
		RelativePath:  "Season 1/Foo.S01E02.srt",
		Extension:     ".srt",
		LastUpdated:   now,
		Language:      1,
	}
	if _, err := repo.Insert(t.Context(), sparse); err != nil {
		t.Fatalf("Insert sparse: %v", err)
	}

	got, err := repo.Get(t.Context(), full.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got.LanguageTags, []string{"forced", "sdh"}) {
		t.Errorf("LanguageTags: %v", got.LanguageTags)
	}
	if got.Title != "Director's Cut" {
		t.Errorf("Title: %q", got.Title)
	}
	if got.Added == nil || !got.Added.Equal(now) {
		t.Errorf("Added: %v", got.Added)
	}

	gotSparse, err := repo.Get(t.Context(), sparse.ID)
	if err != nil {
		t.Fatalf("Get sparse: %v", err)
	}
	if gotSparse.Added != nil {
		t.Errorf("sparse Added: %v, want nil", gotSparse.Added)
	}
	if gotSparse.Title != "" {
		t.Errorf("sparse Title: %q", gotSparse.Title)
	}
	if gotSparse.LanguageTags == nil || len(gotSparse.LanguageTags) != 0 {
		t.Errorf("sparse LanguageTags: %v, want empty slice", gotSparse.LanguageTags)
	}

	if err := repo.Update(t.Context(), &subtitlefiles.SubtitleFile{ID: 99999, LastUpdated: now}); !errors.Is(err, subtitlefiles.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
