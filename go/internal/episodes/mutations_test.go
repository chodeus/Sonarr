package episodes_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodes"
)

// runMutationsSuite exercises the mutation primitives added in Phase
// 2.62. Mirrors runRepoSuite shape — single seeded fixture, both
// engines via the trampoline tests below.
func runMutationsSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// Seed: 4 episodes across two seasons, mixed Monitored.
	seeds := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Monitored: true, EpisodeFileID: ptrInt(50)},
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 2, Monitored: true},
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 3, Monitored: false},
		{SeriesID: 1, SeasonNumber: 2, EpisodeNumber: 1, Monitored: true},
	}
	for i := range seeds {
		if _, err := repo.Insert(t.Context(), &seeds[i]); err != nil {
			t.Fatalf("Insert seed %d: %v", i, err)
		}
	}

	// SetMonitoredFlat: flip seeds[0] off.
	if err := repo.SetMonitoredFlat(t.Context(), &seeds[0], false); err != nil {
		t.Fatalf("SetMonitoredFlat: %v", err)
	}
	if seeds[0].Monitored {
		t.Errorf("SetMonitoredFlat: in-place not updated")
	}
	got, err := repo.Get(t.Context(), seeds[0].ID)
	if err != nil {
		t.Fatalf("Get after flat: %v", err)
	}
	if got.Monitored {
		t.Errorf("SetMonitoredFlat: persisted Monitored=%v, want false", got.Monitored)
	}
	// Missing row: ErrNotFound.
	if err := repo.SetMonitoredFlat(t.Context(), &episodes.Episode{ID: 99999}, true); !errors.Is(err, episodes.ErrNotFound) {
		t.Errorf("SetMonitoredFlat missing: %v", err)
	}

	// SetMonitoredBySeason: turn season 1 OFF — should affect seeds[1]
	// only (seeds[0] already false from flat-test above; seeds[2] already false).
	n, err := repo.SetMonitoredBySeason(t.Context(), 1, 1, false)
	if err != nil {
		t.Fatalf("SetMonitoredBySeason off: %v", err)
	}
	if n != 1 {
		t.Errorf("SetMonitoredBySeason off: rows=%d, want 1", n)
	}
	// Re-running is a no-op (every row already false).
	n, err = repo.SetMonitoredBySeason(t.Context(), 1, 1, false)
	if err != nil {
		t.Fatalf("SetMonitoredBySeason no-op: %v", err)
	}
	if n != 0 {
		t.Errorf("SetMonitoredBySeason no-op: rows=%d, want 0", n)
	}
	// Turn it ON: 3 rows flip.
	n, err = repo.SetMonitoredBySeason(t.Context(), 1, 1, true)
	if err != nil {
		t.Fatalf("SetMonitoredBySeason on: %v", err)
	}
	if n != 3 {
		t.Errorf("SetMonitoredBySeason on: rows=%d, want 3", n)
	}

	// SetMonitoredByIDs: turn 2 specific rows off.
	n, err = repo.SetMonitoredByIDs(t.Context(), []int{seeds[1].ID, seeds[2].ID}, false)
	if err != nil {
		t.Fatalf("SetMonitoredByIDs: %v", err)
	}
	if n != 2 {
		t.Errorf("SetMonitoredByIDs: rows=%d, want 2", n)
	}
	if n, err := repo.SetMonitoredByIDs(t.Context(), nil, false); err != nil || n != 0 {
		t.Errorf("SetMonitoredByIDs nil: rows=%d err=%v", n, err)
	}

	// SetFileID: link seeds[1] to file 99.
	if err := repo.SetFileID(t.Context(), &seeds[1], 99); err != nil {
		t.Fatalf("SetFileID: %v", err)
	}
	if seeds[1].EpisodeFileID == nil || *seeds[1].EpisodeFileID != 99 {
		t.Errorf("SetFileID: in-place EpisodeFileID=%v", seeds[1].EpisodeFileID)
	}
	got, err = repo.Get(t.Context(), seeds[1].ID)
	if err != nil {
		t.Fatalf("Get after file link: %v", err)
	}
	if got.EpisodeFileID == nil || *got.EpisodeFileID != 99 {
		t.Errorf("SetFileID: persisted=%v", got.EpisodeFileID)
	}

	// ClearFileID without unmonitor: file gone, Monitored unchanged.
	// Reset Monitored to true first to verify it's preserved.
	if err := repo.SetMonitoredFlat(t.Context(), &seeds[1], true); err != nil {
		t.Fatalf("reset Monitored: %v", err)
	}
	if err := repo.ClearFileID(t.Context(), &seeds[1], false); err != nil {
		t.Fatalf("ClearFileID: %v", err)
	}
	if seeds[1].EpisodeFileID != nil || !seeds[1].Monitored {
		t.Errorf("ClearFileID: in-place: file=%v mon=%v", seeds[1].EpisodeFileID, seeds[1].Monitored)
	}
	got, err = repo.Get(t.Context(), seeds[1].ID)
	if err != nil {
		t.Fatalf("Get after clear: %v", err)
	}
	if got.EpisodeFileID != nil {
		t.Errorf("ClearFileID: persisted file=%v", got.EpisodeFileID)
	}
	if !got.Monitored {
		t.Errorf("ClearFileID(false): persisted Monitored=%v, want true", got.Monitored)
	}

	// ClearFileID with unmonitor: link first, then clear with unmonitor=true.
	if err := repo.SetFileID(t.Context(), &seeds[1], 77); err != nil {
		t.Fatalf("SetFileID 77: %v", err)
	}
	if err := repo.ClearFileID(t.Context(), &seeds[1], true); err != nil {
		t.Fatalf("ClearFileID unmonitor: %v", err)
	}
	got, err = repo.Get(t.Context(), seeds[1].ID)
	if err != nil {
		t.Fatalf("Get after clear+unmonitor: %v", err)
	}
	if got.EpisodeFileID != nil || got.Monitored {
		t.Errorf("ClearFileID(true): file=%v mon=%v", got.EpisodeFileID, got.Monitored)
	}

	// UpdateLastSearchTime: stamp seeds[0] + seeds[1].
	when := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	stamps := []episodes.Episode{
		{ID: seeds[0].ID, LastSearchTime: ptrTime(when)},
		{ID: seeds[1].ID, LastSearchTime: ptrTime(when)},
	}
	rows, err := repo.UpdateLastSearchTime(t.Context(), stamps)
	if err != nil {
		t.Fatalf("UpdateLastSearchTime: %v", err)
	}
	if rows != 2 {
		t.Errorf("UpdateLastSearchTime: rows=%d, want 2", rows)
	}
	got, err = repo.Get(t.Context(), seeds[0].ID)
	if err != nil {
		t.Fatalf("Get after stamp: %v", err)
	}
	if got.LastSearchTime == nil || !got.LastSearchTime.Equal(when) {
		t.Errorf("UpdateLastSearchTime: persisted=%v, want %v", got.LastSearchTime, when)
	}

	// InsertMany: 2 fresh rows.
	fresh := []*episodes.Episode{
		{SeriesID: 2, SeasonNumber: 1, EpisodeNumber: 1, Title: "Fresh One"},
		{SeriesID: 2, SeasonNumber: 1, EpisodeNumber: 2, Title: "Fresh Two"},
	}
	if err := repo.InsertMany(t.Context(), fresh); err != nil {
		t.Fatalf("InsertMany: %v", err)
	}
	for i, e := range fresh {
		if e.ID == 0 {
			t.Errorf("InsertMany[%d]: ID not assigned", i)
		}
	}

	// UpdateMany: bump both titles.
	fresh[0].Title = "Updated One"
	fresh[1].Title = "Updated Two"
	if err := repo.UpdateMany(t.Context(), fresh); err != nil {
		t.Fatalf("UpdateMany: %v", err)
	}
	got, err = repo.Get(t.Context(), fresh[0].ID)
	if err != nil {
		t.Fatalf("Get after UpdateMany: %v", err)
	}
	if got.Title != "Updated One" {
		t.Errorf("UpdateMany: title=%q want %q", got.Title, "Updated One")
	}

	// DeleteMany: remove the two fresh rows + one missing id (idempotent).
	delList := []episodes.Episode{
		{ID: fresh[0].ID},
		{ID: fresh[1].ID},
		{ID: 999999}, // missing — must not fail
	}
	if err := repo.DeleteMany(t.Context(), delList); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}
	if _, err := repo.Get(t.Context(), fresh[0].ID); !errors.Is(err, episodes.ErrNotFound) {
		t.Errorf("DeleteMany: row[0] still present: %v", err)
	}

	// Empty-list paths.
	if err := repo.InsertMany(t.Context(), nil); err != nil {
		t.Errorf("InsertMany nil: %v", err)
	}
	if err := repo.UpdateMany(t.Context(), nil); err != nil {
		t.Errorf("UpdateMany nil: %v", err)
	}
	if err := repo.DeleteMany(t.Context(), nil); err != nil {
		t.Errorf("DeleteMany nil: %v", err)
	}
}

func TestMutations_SQLite(t *testing.T) {
	t.Parallel()
	runMutationsSuite(t, datastoretest.OpenSQLite(t))
}

func TestMutations_Postgres(t *testing.T) {
	runMutationsSuite(t, datastoretest.OpenPostgres(t))
}
