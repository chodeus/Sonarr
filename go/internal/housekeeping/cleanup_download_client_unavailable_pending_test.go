package housekeeping_test

import (
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/pendingreleases"
)

func TestCleanupDCUPending_Sqlite(t *testing.T) {
	t.Parallel()
	runDCUPendingSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupDCUPending_Postgres(t *testing.T) {
	runDCUPendingSuite(t, datastoretest.OpenPostgres(t))
}

// runDCUPendingSuite seeds the PendingReleases table with one row
// per (age × reason) cell of interest, then runs the housekeeper and
// asserts the matrix:
//
//	                       │ Delay │ DCU │ Fallback │
//	───────────────────────┼───────┼─────┼──────────┤
//	older than 14 days     │ keep  │ DEL │ DEL      │
//	younger than 14 days   │ keep  │keep │ keep     │
//
// Delay rows always stay (DelayProfile timer is the controlling
// mechanism, not housekeeping). DCU/Fallback rows only get swept
// when their Added is older than the 14-day TTL.
func runDCUPendingSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	keep, _, _ := seedSeriesAndFile(t, db)

	prRepo, err := pendingreleases.NewRepo(db)
	if err != nil {
		t.Fatalf("pendingreleases.NewRepo: %v", err)
	}

	now := time.Now().UTC()
	old := now.Add(-15 * 24 * time.Hour)   // > 14 days ago
	young := now.Add(-13 * 24 * time.Hour) // < 14 days ago

	mk := func(added time.Time, reason pendingreleases.Reason, title string) *pendingreleases.Release {
		return &pendingreleases.Release{
			SeriesID:          keep.ID,
			Title:             title,
			Added:             added,
			ParsedEpisodeInfo: "{}",
			ReleaseInfo:       "{}",
			Reason:            int(reason),
		}
	}

	oldDelay := mk(old, pendingreleases.ReasonDelay, "old-delay")
	oldDCU := mk(old, pendingreleases.ReasonDownloadClientUnavailable, "old-dcu")
	oldFallback := mk(old, pendingreleases.ReasonFallback, "old-fallback")
	youngDelay := mk(young, pendingreleases.ReasonDelay, "young-delay")
	youngDCU := mk(young, pendingreleases.ReasonDownloadClientUnavailable, "young-dcu")
	youngFallback := mk(young, pendingreleases.ReasonFallback, "young-fallback")

	all := []*pendingreleases.Release{oldDelay, oldDCU, oldFallback, youngDelay, youngDCU, youngFallback}
	for i, r := range all {
		if _, err := prRepo.Insert(t.Context(), r); err != nil {
			t.Fatalf("PendingReleases Insert idx=%d: %v", i, err)
		}
	}

	task, err := housekeeping.NewCleanupDownloadClientUnavailablePendingReleases(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := task.Name(); got != "CleanupDownloadClientUnavailablePendingReleases" {
		t.Errorf("Name() = %q", got)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// oldDCU + oldFallback should be gone.
	for _, gone := range []*pendingreleases.Release{oldDCU, oldFallback} {
		if _, err := prRepo.Get(t.Context(), gone.ID); err == nil {
			t.Errorf("expected sweep of stale row %q (Reason=%d, Added=%v)",
				gone.Title, gone.Reason, gone.Added)
		}
	}
	// Everyone else stays.
	for _, kept := range []*pendingreleases.Release{oldDelay, youngDelay, youngDCU, youngFallback} {
		if _, err := prRepo.Get(t.Context(), kept.ID); err != nil {
			t.Errorf("expected row %q to survive: %v", kept.Title, err)
		}
	}
}

func TestCleanupDCUPending_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupDownloadClientUnavailablePendingReleases(nil, nil); err == nil {
		t.Errorf("expected ErrNilDB")
	}
}

func TestCleanupDCUPending_EmptyTableIsNoOp(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	task, err := housekeeping.NewCleanupDownloadClientUnavailablePendingReleases(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean (empty): %v", err)
	}
}
