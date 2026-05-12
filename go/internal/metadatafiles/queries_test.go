package metadatafiles_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/metadatafiles"
)

func runAllBySeriesIDSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := metadatafiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// Seed: 3 rows for series 7 + 2 rows for series 8 + 1 row for
	// series 99 to ensure filtering is exact.
	for _, m := range []metadatafiles.MetadataFile{
		{SeriesID: 7, Consumer: "XbmcMetadata", Type: 1, RelativePath: "tvshow.nfo", Extension: ".nfo"},
		{SeriesID: 7, Consumer: "XbmcMetadata", Type: 3, RelativePath: "poster.jpg", Extension: ".jpg"},
		{SeriesID: 7, Consumer: "PlexMetadata", Type: 1, RelativePath: ".plexmatch", Extension: ""},
		{SeriesID: 8, Consumer: "XbmcMetadata", Type: 1, RelativePath: "tvshow.nfo", Extension: ".nfo"},
		{SeriesID: 8, Consumer: "XbmcMetadata", Type: 3, RelativePath: "poster.jpg", Extension: ".jpg"},
		{SeriesID: 99, Consumer: "XbmcMetadata", Type: 1, RelativePath: "tvshow.nfo", Extension: ".nfo"},
	} {
		row := m
		if _, err := repo.Insert(t.Context(), &row); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	// Series 7 → 3 rows.
	got, err := repo.AllBySeriesID(t.Context(), 7)
	if err != nil {
		t.Fatalf("AllBySeriesID(7): %v", err)
	}
	if len(got) != 3 {
		t.Errorf("AllBySeriesID(7) = %d rows, want 3", len(got))
	}
	for _, m := range got {
		if m.SeriesID != 7 {
			t.Errorf("filter leaked SeriesID=%d", m.SeriesID)
		}
	}

	// Series 8 → 2 rows.
	got, err = repo.AllBySeriesID(t.Context(), 8)
	if err != nil {
		t.Fatalf("AllBySeriesID(8): %v", err)
	}
	if len(got) != 2 {
		t.Errorf("AllBySeriesID(8) = %d rows, want 2", len(got))
	}

	// Series 1234 (no rows) → empty slice, no error.
	got, err = repo.AllBySeriesID(t.Context(), 1234)
	if err != nil {
		t.Fatalf("AllBySeriesID(1234): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing series should yield empty slice, got %d", len(got))
	}
	// Empty result should NOT be nil — explicit empty slice (matches
	// the C# IEnumerable<MetadataFile> contract).
	if got == nil {
		t.Errorf("empty result should be non-nil")
	}

	// Cancelled-context should surface as a non-nil error.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = repo.AllBySeriesID(cancelled, 7)
	if err == nil {
		t.Errorf("expected error on cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error chain should include context.Canceled, got: %v", err)
	}
}

func TestRepo_AllBySeriesID_SQLite(t *testing.T) {
	t.Parallel()
	runAllBySeriesIDSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_AllBySeriesID_Postgres(t *testing.T) {
	runAllBySeriesIDSuite(t, datastoretest.OpenPostgres(t))
}
