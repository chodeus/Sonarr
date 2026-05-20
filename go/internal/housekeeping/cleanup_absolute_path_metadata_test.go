package housekeeping_test

import (
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/metadatafiles"
)

func TestCleanupAbsolutePathMetadataFiles_Sqlite(t *testing.T) {
	t.Parallel()
	runAbsolutePathMetadataSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupAbsolutePathMetadataFiles_Postgres(t *testing.T) {
	runAbsolutePathMetadataSuite(t, datastoretest.OpenPostgres(t))
}

// runAbsolutePathMetadataSuite seeds a MetadataFiles table with one row
// per absolute-path shape (Windows drive-letter, Windows UNC, POSIX
// rooted) plus a relative-path survivor, then runs the housekeeper
// and asserts the three absolute rows are gone while the survivor
// stays. The series-FK is unwired (SeriesID=0) deliberately — this
// housekeeper doesn't care about the FK, only about RelativePath.
func runAbsolutePathMetadataSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	keep, _, _ := seedSeriesAndFile(t, db)
	now := time.Now().UTC()

	mfRepo, err := metadatafiles.NewRepo(db)
	if err != nil {
		t.Fatalf("metadatafiles.NewRepo: %v", err)
	}
	mk := func(name string) *metadatafiles.MetadataFile {
		zero := 0
		return &metadatafiles.MetadataFile{
			SeriesID:      keep.ID,
			Consumer:      "Kodi",
			Type:          3,
			RelativePath:  name,
			LastUpdated:   now,
			Extension:     ".nfo",
			EpisodeFileID: &zero,
		}
	}

	survivor := mk("Season 01/Show.S01E01.nfo")
	driveLetter := mk(`C:\series\Show\poster.jpg`)
	uncRoot := mk(`\\server\share\foo.nfo`)
	posixRoot := mk("/var/lib/sonarr/foo.nfo")

	for i, row := range []*metadatafiles.MetadataFile{survivor, driveLetter, uncRoot, posixRoot} {
		if _, err := mfRepo.Insert(t.Context(), row); err != nil {
			t.Fatalf("metadatafiles Insert idx=%d: %v", i, err)
		}
	}

	task, err := housekeeping.NewCleanupAbsolutePathMetadataFiles(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := task.Name(); got != "CleanupAbsolutePathMetadataFiles" {
		t.Errorf("Name() = %q, want CleanupAbsolutePathMetadataFiles", got)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	for _, gone := range []int{driveLetter.ID, uncRoot.ID, posixRoot.ID} {
		if _, err := mfRepo.Get(t.Context(), gone); err == nil {
			t.Errorf("absolute-path row id=%d survived sweep", gone)
		}
	}
	if _, err := mfRepo.Get(t.Context(), survivor.ID); err != nil {
		t.Errorf("relative-path survivor dropped: %v", err)
	}
}

func TestCleanupAbsolutePathMetadataFiles_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupAbsolutePathMetadataFiles(nil, nil); err == nil {
		t.Errorf("expected ErrNilDB")
	}
}

func TestCleanupAbsolutePathMetadataFiles_NoRowsToDelete(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	task, err := housekeeping.NewCleanupAbsolutePathMetadataFiles(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Empty table → Clean is a no-op with nil error.
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean (empty): %v", err)
	}
}
