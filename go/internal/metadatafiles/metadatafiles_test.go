package metadatafiles_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/metadatafiles"
)

func ptrInt(i int) *int              { return &i }
func ptrTime(t time.Time) *time.Time { return &t }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := metadatafiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)

	// SeriesMetadata — top-level series-level NFO; SeasonNumber + EpisodeFileId nullable.
	seriesMd := &metadatafiles.MetadataFile{
		SeriesID:     50,
		Consumer:     "XbmcMetadata",
		Type:         1, // SeriesMetadata
		RelativePath: "tvshow.nfo",
		LastUpdated:  now,
		Hash:         "abcdef",
		Added:        ptrTime(now),
		Extension:    ".nfo",
	}
	if _, err := repo.Insert(t.Context(), seriesMd); err != nil {
		t.Fatalf("Insert seriesMd: %v", err)
	}

	// EpisodeMetadata — Season + EpisodeFileId set.
	episodeMd := &metadatafiles.MetadataFile{
		SeriesID:      50,
		Consumer:      "XbmcMetadata",
		Type:          5, // EpisodeMetadata
		RelativePath:  "Season 1/Foo.S01E01.nfo",
		LastUpdated:   now,
		SeasonNumber:  ptrInt(1),
		EpisodeFileID: ptrInt(100),
		Extension:     ".nfo",
	}
	if _, err := repo.Insert(t.Context(), episodeMd); err != nil {
		t.Fatalf("Insert episodeMd: %v", err)
	}

	gotSeries, err := repo.Get(t.Context(), seriesMd.ID)
	if err != nil {
		t.Fatalf("Get seriesMd: %v", err)
	}
	if gotSeries.SeasonNumber != nil || gotSeries.EpisodeFileID != nil {
		t.Errorf("series-level nullables not nil: %+v", gotSeries)
	}
	if gotSeries.Hash != "abcdef" || gotSeries.Added == nil {
		t.Errorf("series Hash/Added: %+v", gotSeries)
	}

	gotEpisode, err := repo.Get(t.Context(), episodeMd.ID)
	if err != nil {
		t.Fatalf("Get episodeMd: %v", err)
	}
	if gotEpisode.SeasonNumber == nil || *gotEpisode.SeasonNumber != 1 ||
		gotEpisode.EpisodeFileID == nil || *gotEpisode.EpisodeFileID != 100 {
		t.Errorf("episode pointers: %+v", gotEpisode)
	}
	if gotEpisode.Hash != "" || gotEpisode.Added != nil {
		t.Errorf("episode unset fields: %+v", gotEpisode)
	}

	if err := repo.Update(t.Context(), &metadatafiles.MetadataFile{ID: 99999, Consumer: "x", RelativePath: "x", Extension: "x", LastUpdated: now}); !errors.Is(err, metadatafiles.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), seriesMd.ID); err != nil {
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
