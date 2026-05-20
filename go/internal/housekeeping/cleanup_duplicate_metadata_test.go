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

func TestCleanupDuplicateMetadataFiles_Sqlite(t *testing.T) {
	t.Parallel()
	runDuplicateMetadataSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupDuplicateMetadataFiles_Postgres(t *testing.T) {
	runDuplicateMetadataSuite(t, datastoretest.OpenPostgres(t))
}

// runDuplicateMetadataSuite seeds the MetadataFiles table with one
// duplicate group per swept Type plus untouched groups:
//
//   - Type 1 (SeriesMetadata), Consumer "Kodi", SeriesID=keep — 3 rows.
//     One pass removes 1 → 2 remain. Two more passes converge to 1.
//   - Type 2 (EpisodeMetadata), Consumer "Kodi", EpisodeFileID=k — 2
//     rows. Single pass converges to 1.
//   - Type 5 (EpisodeImage), Consumer "Kodi", EpisodeFileID=k — 2
//     rows. Single pass converges to 1.
//   - Type 3 (SeriesImage), Consumer "Kodi", SeriesID=keep — 3 rows.
//     NOT swept (Type 3 isn't in the housekeeper's list).
//   - Type 4 (SeasonImage), Consumer "Kodi", SeriesID=keep — 2 rows.
//     NOT swept.
//   - Type 2 with different Consumer ("Roksbox") — single row, not a
//     dup; survives.
func runDuplicateMetadataSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	keep, _, keepFile := seedSeriesAndFile(t, db)
	now := time.Now().UTC()

	mfRepo, err := metadatafiles.NewRepo(db)
	if err != nil {
		t.Fatalf("metadatafiles.NewRepo: %v", err)
	}
	mk := func(mtype int, consumer, name string, episodeFileID int) *metadatafiles.MetadataFile {
		efid := episodeFileID
		return &metadatafiles.MetadataFile{
			SeriesID:      keep.ID,
			Consumer:      consumer,
			Type:          mtype,
			RelativePath:  name,
			LastUpdated:   now,
			Extension:     ".nfo",
			EpisodeFileID: &efid,
		}
	}

	// Type 1 — 3 duplicates (will need 2 passes to converge).
	type1a := mk(1, "Kodi", "tvshow-a.nfo", 0)
	type1b := mk(1, "Kodi", "tvshow-b.nfo", 0)
	type1c := mk(1, "Kodi", "tvshow-c.nfo", 0)
	// Type 2 — 2 duplicates.
	type2a := mk(2, "Kodi", "ep-a.nfo", keepFile.ID)
	type2b := mk(2, "Kodi", "ep-b.nfo", keepFile.ID)
	// Type 5 — 2 duplicates.
	type5a := mk(5, "Kodi", "ep-img-a.jpg", keepFile.ID)
	type5b := mk(5, "Kodi", "ep-img-b.jpg", keepFile.ID)
	// Type 3 — duplicates but NOT swept.
	type3a := mk(3, "Kodi", "series-img-a.jpg", 0)
	type3b := mk(3, "Kodi", "series-img-b.jpg", 0)
	type3c := mk(3, "Kodi", "series-img-c.jpg", 0)
	// Type 4 — duplicates but NOT swept.
	type4a := mk(4, "Kodi", "season-img-a.jpg", 0)
	type4b := mk(4, "Kodi", "season-img-b.jpg", 0)
	// Type 2 with different Consumer — no dup partner; survives.
	type2roksbox := mk(2, "Roksbox", "ep-roksbox.xml", keepFile.ID)

	all := []*metadatafiles.MetadataFile{
		type1a, type1b, type1c,
		type2a, type2b,
		type5a, type5b,
		type3a, type3b, type3c,
		type4a, type4b,
		type2roksbox,
	}
	for i, row := range all {
		if _, err := mfRepo.Insert(t.Context(), row); err != nil {
			t.Fatalf("metadatafiles Insert idx=%d: %v", i, err)
		}
	}

	task, err := housekeeping.NewCleanupDuplicateMetadataFiles(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := task.Name(); got != "CleanupDuplicateMetadataFiles" {
		t.Errorf("Name() = %q", got)
	}
	// Run twice to converge the 3-row Type-1 group (first pass: 3→2;
	// second pass: 2→1). Type 2 / Type 5 converge on the first pass.
	for i := 0; i < 2; i++ {
		if err := task.Clean(t.Context()); err != nil {
			t.Fatalf("Clean iter=%d: %v", i, err)
		}
	}

	// Count survivors per Type+Consumer group via a custom query.
	count := func(predicate string, args ...any) int {
		t.Helper()
		row := db.SQL.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM "MetadataFiles" WHERE `+predicate, args...)
		var n int
		if err := row.Scan(&n); err != nil {
			t.Fatalf("count(%s): %v", predicate, err)
		}
		return n
	}
	if n := count(`"Type" = `+datastore.Placeholder(db.Engine, 1)+` AND "Consumer" = `+datastore.Placeholder(db.Engine, 2), 1, "Kodi"); n != 1 {
		t.Errorf("Type=1/Kodi survivors = %d, want 1", n)
	}
	if n := count(`"Type" = `+datastore.Placeholder(db.Engine, 1)+` AND "Consumer" = `+datastore.Placeholder(db.Engine, 2), 2, "Kodi"); n != 1 {
		t.Errorf("Type=2/Kodi survivors = %d, want 1", n)
	}
	if n := count(`"Type" = `+datastore.Placeholder(db.Engine, 1)+` AND "Consumer" = `+datastore.Placeholder(db.Engine, 2), 5, "Kodi"); n != 1 {
		t.Errorf("Type=5/Kodi survivors = %d, want 1", n)
	}
	// Untouched Types — full count survives.
	if n := count(`"Type" = `+datastore.Placeholder(db.Engine, 1), 3); n != 3 {
		t.Errorf("Type=3 survivors = %d, want 3 (not swept)", n)
	}
	if n := count(`"Type" = `+datastore.Placeholder(db.Engine, 1), 4); n != 2 {
		t.Errorf("Type=4 survivors = %d, want 2 (not swept)", n)
	}
	// Different-Consumer non-dup survives.
	if n := count(`"Type" = `+datastore.Placeholder(db.Engine, 1)+` AND "Consumer" = `+datastore.Placeholder(db.Engine, 2), 2, "Roksbox"); n != 1 {
		t.Errorf("Type=2/Roksbox survivors = %d, want 1 (no dup partner)", n)
	}
}

func TestCleanupDuplicateMetadataFiles_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupDuplicateMetadataFiles(nil, nil); err == nil {
		t.Errorf("expected ErrNilDB")
	}
}

func TestCleanupDuplicateMetadataFiles_EmptyTableIsNoOp(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	task, err := housekeeping.NewCleanupDuplicateMetadataFiles(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean (empty): %v", err)
	}
}
