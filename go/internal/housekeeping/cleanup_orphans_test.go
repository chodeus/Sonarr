package housekeeping_test

import (
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/series"
)

// orphanFixture seeds two pairs in each table:
//
//   - Series 1 + child rows that reference it (survivors)
//   - Series 99 + child rows that reference it, then Series 99 is
//     dropped so the children become orphans
//
// Each housekeeper test reuses the same fixture so the seeded rows
// exercise both the "delete orphan" and "leave survivor" branches in
// one pass.
type orphanFixture struct {
	db              *datastore.DB
	survivor        int // surviving series id
	orphanSeed      int // series id seeded then deleted
	survivorEpisode int
	orphanEpisode   int
	survivorEpFile  int
	orphanEpFile    int
}

func seedOrphanFixture(t *testing.T, db *datastore.DB) *orphanFixture {
	t.Helper()
	now := time.Now().UTC()

	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	added := now
	survivor := &series.Series{TvdbID: 100, Title: "Survivor", CleanTitle: "survivor", TitleSlug: "survivor", Path: "/tv/survivor", Added: &added, Status: 1, Tags: []int{}, MalIDs: []int{}, Images: "[]", Seasons: "[]"}
	dropped := &series.Series{TvdbID: 200, Title: "Dropped", CleanTitle: "dropped", TitleSlug: "dropped", Path: "/tv/dropped", Added: &added, Status: 1, Tags: []int{}, MalIDs: []int{}, Images: "[]", Seasons: "[]"}
	if _, err := seriesRepo.Insert(t.Context(), survivor); err != nil {
		t.Fatalf("series Insert survivor: %v", err)
	}
	if _, err := seriesRepo.Insert(t.Context(), dropped); err != nil {
		t.Fatalf("series Insert dropped: %v", err)
	}

	epRepo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("episodes.NewRepo: %v", err)
	}
	survivorEp := &episodes.Episode{SeriesID: survivor.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "ep1"}
	orphanEp := &episodes.Episode{SeriesID: dropped.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "ep1"}
	if _, err := epRepo.Insert(t.Context(), survivorEp); err != nil {
		t.Fatalf("episodes Insert survivor: %v", err)
	}
	if _, err := epRepo.Insert(t.Context(), orphanEp); err != nil {
		t.Fatalf("episodes Insert orphan: %v", err)
	}

	filesRepo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("episodefiles.NewRepo: %v", err)
	}
	survivorFile := &episodefiles.File{SeriesID: survivor.ID, SeasonNumber: 1, RelativePath: "s.mkv", Quality: "{}", Size: 1, DateAdded: now, Languages: []int{}}
	orphanFile := &episodefiles.File{SeriesID: dropped.ID, SeasonNumber: 1, RelativePath: "o.mkv", Quality: "{}", Size: 1, DateAdded: now, Languages: []int{}}
	if _, err := filesRepo.Insert(t.Context(), survivorFile); err != nil {
		t.Fatalf("episodefiles Insert survivor: %v", err)
	}
	if _, err := filesRepo.Insert(t.Context(), orphanFile); err != nil {
		t.Fatalf("episodefiles Insert orphan: %v", err)
	}

	// Link each Episode to its EpisodeFile so the EpisodeFile-orphan
	// test exercises the LEFT-JOIN-via-FK path.
	survivorEp.EpisodeFileID = ptrInt(survivorFile.ID)
	orphanEp.EpisodeFileID = ptrInt(orphanFile.ID)
	if err := epRepo.Update(t.Context(), survivorEp); err != nil {
		t.Fatalf("episodes Update survivor link: %v", err)
	}
	if err := epRepo.Update(t.Context(), orphanEp); err != nil {
		t.Fatalf("episodes Update orphan link: %v", err)
	}

	// History rows: one points at the survivor pair, one at the
	// orphan pair, one points at a dangling EpisodeID (the orphan
	// episode's id will be valid until we delete it).
	histRepo, err := history.NewRepo(db)
	if err != nil {
		t.Fatalf("history.NewRepo: %v", err)
	}
	survivorHist := &history.Entry{SeriesID: survivor.ID, EpisodeID: survivorEp.ID, SourceTitle: "s", Date: now, Quality: "{}", Data: map[string]string{}, Languages: []int{}}
	orphanHist := &history.Entry{SeriesID: dropped.ID, EpisodeID: orphanEp.ID, SourceTitle: "o", Date: now, Quality: "{}", Data: map[string]string{}, Languages: []int{}}
	if _, err := histRepo.Insert(t.Context(), survivorHist); err != nil {
		t.Fatalf("history Insert survivor: %v", err)
	}
	if _, err := histRepo.Insert(t.Context(), orphanHist); err != nil {
		t.Fatalf("history Insert orphan: %v", err)
	}

	// Blocklist rows: one survivor, one orphan.
	blRepo, err := blocklist.NewRepo(db)
	if err != nil {
		t.Fatalf("blocklist.NewRepo: %v", err)
	}
	survivorBL := &blocklist.Entry{SeriesID: survivor.ID, EpisodeIDs: []int{survivorEp.ID}, SourceTitle: "s", Date: now, Quality: "{}", Languages: []int{}}
	orphanBL := &blocklist.Entry{SeriesID: dropped.ID, EpisodeIDs: []int{orphanEp.ID}, SourceTitle: "o", Date: now, Quality: "{}", Languages: []int{}}
	if _, err := blRepo.Insert(t.Context(), survivorBL); err != nil {
		t.Fatalf("blocklist Insert survivor: %v", err)
	}
	if _, err := blRepo.Insert(t.Context(), orphanBL); err != nil {
		t.Fatalf("blocklist Insert orphan: %v", err)
	}

	// Drop the orphan series so the Episodes / History / Blocklist
	// rows referencing it are now dangling. Episodes must be dropped
	// FIRST otherwise FK-style constraints (or the orphan-episodes
	// housekeeper running first in production) may interfere — but
	// for this test we verify each housekeeper independently.
	if err := seriesRepo.Delete(t.Context(), dropped.ID); err != nil {
		t.Fatalf("series Delete dropped: %v", err)
	}

	return &orphanFixture{
		db:              db,
		survivor:        survivor.ID,
		orphanSeed:      dropped.ID,
		survivorEpisode: survivorEp.ID,
		orphanEpisode:   orphanEp.ID,
		survivorEpFile:  survivorFile.ID,
		orphanEpFile:    orphanFile.ID,
	}
}

func ptrInt(v int) *int { return &v }

func TestCleanupOrphanedEpisodes_Sqlite(t *testing.T) {
	t.Parallel()
	runOrphanedEpisodesSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedEpisodes_Postgres(t *testing.T) {
	runOrphanedEpisodesSuite(t, datastoretest.OpenPostgres(t))
}

func runOrphanedEpisodesSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	fx := seedOrphanFixture(t, db)

	task, err := housekeeping.NewCleanupOrphanedEpisodes(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	epRepo, _ := episodes.NewRepo(db)
	if _, err := epRepo.Get(t.Context(), fx.orphanEpisode); err == nil {
		t.Errorf("orphan episode survived")
	}
	if _, err := epRepo.Get(t.Context(), fx.survivorEpisode); err != nil {
		t.Errorf("survivor episode dropped: %v", err)
	}
}

func TestCleanupOrphanedEpisodeFiles_Sqlite(t *testing.T) {
	t.Parallel()
	runOrphanedEpisodeFilesSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedEpisodeFiles_Postgres(t *testing.T) {
	runOrphanedEpisodeFilesSuite(t, datastoretest.OpenPostgres(t))
}

func runOrphanedEpisodeFilesSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	fx := seedOrphanFixture(t, db)

	// First drop the orphan-episode so the orphan-EpisodeFile is
	// truly dangling (no Episode points at it). The
	// CleanupOrphanedEpisodes housekeeper would normally fire first
	// in production; here we run it explicitly for test clarity.
	episodesTask, _ := housekeeping.NewCleanupOrphanedEpisodes(db, slog.Default())
	if err := episodesTask.Clean(t.Context()); err != nil {
		t.Fatalf("orphan-episodes pre-pass: %v", err)
	}

	task, err := housekeeping.NewCleanupOrphanedEpisodeFiles(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	filesRepo, _ := episodefiles.NewRepo(db)
	if _, err := filesRepo.Get(t.Context(), fx.orphanEpFile); err == nil {
		t.Errorf("orphan EpisodeFile survived")
	}
	if _, err := filesRepo.Get(t.Context(), fx.survivorEpFile); err != nil {
		t.Errorf("survivor EpisodeFile dropped: %v", err)
	}
}

func TestCleanupOrphanedHistoryItems_Sqlite(t *testing.T) {
	t.Parallel()
	runOrphanedHistorySuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedHistoryItems_Postgres(t *testing.T) {
	runOrphanedHistorySuite(t, datastoretest.OpenPostgres(t))
}

func runOrphanedHistorySuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	fx := seedOrphanFixture(t, db)

	task, err := housekeeping.NewCleanupOrphanedHistoryItems(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	histRepo, _ := history.NewRepo(db)
	all, err := histRepo.AllBySeriesID(t.Context(), fx.orphanSeed)
	if err != nil {
		t.Fatalf("AllBySeriesID: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("orphan history rows survived: %d", len(all))
	}
	survivors, err := histRepo.AllBySeriesID(t.Context(), fx.survivor)
	if err != nil {
		t.Fatalf("AllBySeriesID survivor: %v", err)
	}
	if len(survivors) != 1 {
		t.Errorf("survivor history rows: %d, want 1", len(survivors))
	}
}

func TestCleanupOrphanedBlocklist_Sqlite(t *testing.T) {
	t.Parallel()
	runOrphanedBlocklistSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedBlocklist_Postgres(t *testing.T) {
	runOrphanedBlocklistSuite(t, datastoretest.OpenPostgres(t))
}

func runOrphanedBlocklistSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	fx := seedOrphanFixture(t, db)

	task, err := housekeeping.NewCleanupOrphanedBlocklist(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	blRepo, _ := blocklist.NewRepo(db)
	orphan, err := blRepo.AllBySeriesID(t.Context(), fx.orphanSeed)
	if err != nil {
		t.Fatalf("AllBySeriesID orphan: %v", err)
	}
	if len(orphan) != 0 {
		t.Errorf("orphan blocklist rows survived: %d", len(orphan))
	}
	survivors, err := blRepo.AllBySeriesID(t.Context(), fx.survivor)
	if err != nil {
		t.Fatalf("AllBySeriesID survivor: %v", err)
	}
	if len(survivors) != 1 {
		t.Errorf("survivor blocklist rows: %d, want 1", len(survivors))
	}
}

func TestNewCleanupOrphaned_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupOrphanedEpisodes(nil, nil); err == nil {
		t.Errorf("OrphanedEpisodes(nil): want error")
	}
	if _, err := housekeeping.NewCleanupOrphanedEpisodeFiles(nil, nil); err == nil {
		t.Errorf("OrphanedEpisodeFiles(nil): want error")
	}
	if _, err := housekeeping.NewCleanupOrphanedHistoryItems(nil, nil); err == nil {
		t.Errorf("OrphanedHistoryItems(nil): want error")
	}
	if _, err := housekeeping.NewCleanupOrphanedBlocklist(nil, nil); err == nil {
		t.Errorf("OrphanedBlocklist(nil): want error")
	}
}
