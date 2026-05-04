package scheduledtasks_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// runHelpersSuite exercises SetLastExecutionTime and Upsert across
// both engines.
func runHelpersSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	r, err := scheduledtasks.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	exec := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	start := time.Date(2026, 5, 5, 10, 0, 5, 0, time.UTC)
	t1 := &scheduledtasks.Task{
		TypeName:      "Sonarr.Go.RefreshSeriesCommand",
		Interval:      720,
		LastExecution: exec,
		LastStartTime: &start,
	}
	if _, err := r.Insert(t.Context(), t1); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// SetLastExecutionTime — targeted UPDATE on (LastExecution, LastStartTime).
	newExec := time.Date(2026, 5, 5, 13, 0, 0, 0, time.UTC)
	newStart := time.Date(2026, 5, 5, 13, 0, 2, 0, time.UTC)
	if err := r.SetLastExecutionTime(t.Context(), t1.ID, newExec, newStart); err != nil {
		t.Fatalf("SetLastExecutionTime: %v", err)
	}
	got, err := r.Get(t.Context(), t1.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.LastExecution.Equal(newExec) {
		t.Errorf("LastExecution: got %v, want %v", got.LastExecution, newExec)
	}
	if got.LastStartTime == nil || !got.LastStartTime.Equal(newStart) {
		t.Errorf("LastStartTime: got %v, want %v", got.LastStartTime, newStart)
	}
	// Untouched columns stay the same.
	if got.Interval != 720 || got.TypeName != t1.TypeName {
		t.Errorf("untargeted columns drifted: %+v", got)
	}

	// SetLastExecutionTime on missing id returns ErrNotFound.
	if err := r.SetLastExecutionTime(t.Context(), 99999, newExec, newStart); !errors.Is(err, scheduledtasks.ErrNotFound) {
		t.Errorf("SetLastExecutionTime(missing): %v", err)
	}

	// SetLastExecutionTime forces UTC even when caller passes a local time.
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	localExec := time.Date(2026, 5, 5, 6, 0, 0, 0, loc) // 13:00 UTC
	localStart := time.Date(2026, 5, 5, 6, 0, 5, 0, loc)
	if err := r.SetLastExecutionTime(t.Context(), t1.ID, localExec, localStart); err != nil {
		t.Fatalf("SetLastExecutionTime(local): %v", err)
	}
	got, err = r.Get(t.Context(), t1.ID)
	if err != nil {
		t.Fatalf("Get post-local: %v", err)
	}
	if got.LastExecution.Location() != time.UTC {
		t.Errorf("LastExecution timezone: got %v, want UTC", got.LastExecution.Location())
	}
	if !got.LastExecution.Equal(localExec) {
		t.Errorf("local exec round-trip: got %v, want %v", got.LastExecution, localExec.UTC())
	}

	// Upsert — INSERT path: TypeName not yet persisted.
	fresh := &scheduledtasks.Task{
		TypeName:      "Sonarr.Go.HousekeepingCommand",
		Interval:      1440,
		LastExecution: exec,
	}
	if err := r.Upsert(t.Context(), fresh); err != nil {
		t.Fatalf("Upsert insert: %v", err)
	}
	if fresh.ID == 0 {
		t.Errorf("Upsert insert: ID not populated")
	}

	// Upsert — UPDATE path: same TypeName, different Interval.
	updated := &scheduledtasks.Task{
		TypeName:      "Sonarr.Go.HousekeepingCommand",
		Interval:      4320,
		LastExecution: exec,
	}
	if err := r.Upsert(t.Context(), updated); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	if updated.ID != fresh.ID {
		t.Errorf("Upsert update: ID=%d, want %d (existing)", updated.ID, fresh.ID)
	}
	post, err := r.GetByTypeName(t.Context(), "Sonarr.Go.HousekeepingCommand")
	if err != nil {
		t.Fatalf("GetByTypeName: %v", err)
	}
	if post.Interval != 4320 {
		t.Errorf("Upsert update: Interval=%d, want 4320", post.Interval)
	}

	// Upsert validation: nil + empty TypeName.
	if err := r.Upsert(t.Context(), nil); err == nil {
		t.Errorf("Upsert(nil): expected error")
	}
	if err := r.Upsert(t.Context(), &scheduledtasks.Task{}); err == nil {
		t.Errorf("Upsert(empty TypeName): expected error")
	}
}

func TestRepoHelpers_SQLite(t *testing.T) {
	t.Parallel()
	runHelpersSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepoHelpers_Postgres(t *testing.T) {
	runHelpersSuite(t, datastoretest.OpenPostgres(t))
}
