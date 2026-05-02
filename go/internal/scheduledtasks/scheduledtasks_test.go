package scheduledtasks_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// runRepoSuite exercises CRUD across both engines, with a focus on
// time.Time round-tripping (UTC, second precision, nullable) and the
// UNIQUE TypeName constraint.
func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := scheduledtasks.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// HEAD-state seed: migrations don't auto-insert ScheduledTasks rows
	// (the C# scheduler creates them at startup as it discovers tasks),
	// so the table is empty.
	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All initial: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("All initial: got %d rows, want 0", len(all))
	}

	// Insert with non-nil LastStartTime. Use a fixed UTC instant so we
	// can assert byte-equivalence on round-trip.
	exec := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	start := time.Date(2026, 5, 2, 14, 31, 5, 0, time.UTC)
	first := &scheduledtasks.Task{
		TypeName:      "NzbDrone.Core.Indexers.RssSyncCommand",
		Interval:      15,
		LastExecution: exec,
		LastStartTime: &start,
	}
	if _, err := repo.Insert(t.Context(), first); err != nil {
		t.Fatalf("Insert first: %v", err)
	}
	if first.ID == 0 {
		t.Fatalf("Insert: ID not populated")
	}

	got, err := repo.Get(t.Context(), first.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TypeName != first.TypeName || got.Interval != 15 {
		t.Errorf("scalar round-trip: %+v", got)
	}
	if !got.LastExecution.Equal(exec) {
		t.Errorf("LastExecution round-trip: got %v, want %v", got.LastExecution, exec)
	}
	if got.LastExecution.Location() != time.UTC {
		t.Errorf("LastExecution timezone: got %v, want UTC", got.LastExecution.Location())
	}
	if got.LastStartTime == nil {
		t.Fatalf("LastStartTime round-trip: got nil, want %v", start)
	}
	if !got.LastStartTime.Equal(start) {
		t.Errorf("LastStartTime round-trip: got %v, want %v", *got.LastStartTime, start)
	}

	// Insert with nil LastStartTime — exercises NULL persistence.
	second := &scheduledtasks.Task{
		TypeName:      "NzbDrone.Core.Update.UpdateCheckCommand",
		Interval:      720,
		LastExecution: exec,
		LastStartTime: nil,
	}
	if _, err := repo.Insert(t.Context(), second); err != nil {
		t.Fatalf("Insert second: %v", err)
	}
	got2, err := repo.Get(t.Context(), second.ID)
	if err != nil {
		t.Fatalf("Get second: %v", err)
	}
	if got2.LastStartTime != nil {
		t.Errorf("nil LastStartTime round-trip: got %v, want nil", *got2.LastStartTime)
	}

	// GetByTypeName resolves the unique key.
	byName, err := repo.GetByTypeName(t.Context(), "NzbDrone.Core.Update.UpdateCheckCommand")
	if err != nil {
		t.Fatalf("GetByTypeName: %v", err)
	}
	if byName.ID != second.ID {
		t.Errorf("GetByTypeName: got id %d, want %d", byName.ID, second.ID)
	}
	if _, err := repo.GetByTypeName(t.Context(), "DoesNotExist"); !errors.Is(err, scheduledtasks.ErrNotFound) {
		t.Errorf("GetByTypeName(missing): %v", err)
	}

	// Duplicate TypeName violates UNIQUE constraint on both engines.
	dup := &scheduledtasks.Task{
		TypeName:      "NzbDrone.Core.Indexers.RssSyncCommand",
		Interval:      30,
		LastExecution: exec,
	}
	if _, err := repo.Insert(t.Context(), dup); err == nil {
		t.Errorf("Insert duplicate TypeName: expected error, got nil")
	}

	// Update changes everything including clearing LastStartTime.
	got.Interval = 60
	got.LastStartTime = nil
	newExec := time.Date(2026, 5, 2, 15, 0, 0, 0, time.UTC)
	got.LastExecution = newExec
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.Interval != 60 || post.LastStartTime != nil || !post.LastExecution.Equal(newExec) {
		t.Errorf("post-Update: %+v", post)
	}

	// Update on missing ID returns ErrNotFound.
	if err := repo.Update(t.Context(), &scheduledtasks.Task{ID: 99999, TypeName: "x", LastExecution: exec}); !errors.Is(err, scheduledtasks.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	// All should now have 2 rows in Id order.
	all, err = repo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 || all[0].ID != first.ID || all[1].ID != second.ID {
		t.Errorf("All ordering: %+v", all)
	}

	// Delete idempotency.
	if err := repo.Delete(t.Context(), first.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), first.ID); !errors.Is(err, scheduledtasks.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}

	// Get on missing returns ErrNotFound.
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, scheduledtasks.ErrNotFound) {
		t.Errorf("Get(missing): %v", err)
	}
}

// TestRepo_TimeUTCInputLocal verifies that supplying a non-UTC time on
// Insert still round-trips as UTC. Important because callers that pass
// time.Now() without explicit .UTC() shouldn't see drift.
func TestRepo_TimeUTCInputLocal(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, err := scheduledtasks.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	local := time.Date(2026, 5, 2, 7, 30, 0, 0, loc) // 14:30 UTC
	task := &scheduledtasks.Task{
		TypeName:      "Local.Test",
		Interval:      5,
		LastExecution: local,
		LastStartTime: &local,
	}
	if _, err := repo.Insert(t.Context(), task); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := local.UTC()
	if !got.LastExecution.Equal(want) {
		t.Errorf("LastExecution: got %v, want %v", got.LastExecution, want)
	}
	if got.LastStartTime == nil || !got.LastStartTime.Equal(want) {
		t.Errorf("LastStartTime: got %v, want %v", got.LastStartTime, want)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
