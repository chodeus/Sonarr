package housekeeping_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

func TestNewFixFutureRunScheduledTasks_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewFixFutureRunScheduledTasks(nil, nil); !errors.Is(err, housekeeping.ErrNilDB) {
		t.Errorf("NewFixFuture(nil): %v, want ErrNilDB", err)
	}
}

func TestFixFutureRunScheduledTasks_ClampsFutureRows(t *testing.T) {
	t.Parallel()
	runFixFutureSuite(t, datastoretest.OpenSQLite(t))
}

func TestFixFutureRunScheduledTasks_ClampsFutureRows_Postgres(t *testing.T) {
	runFixFutureSuite(t, datastoretest.OpenPostgres(t))
}

func runFixFutureSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	repo, err := scheduledtasks.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// Seed: one row in the future (-> should clamp), one in the past
	// (-> untouched).
	future := &scheduledtasks.Task{
		TypeName:      "FutureTask",
		Interval:      60,
		LastExecution: now.Add(2 * time.Hour),
	}
	past := &scheduledtasks.Task{
		TypeName:      "PastTask",
		Interval:      60,
		LastExecution: now.Add(-2 * time.Hour),
	}
	if err := repo.Upsert(t.Context(), future); err != nil {
		t.Fatalf("Upsert future: %v", err)
	}
	if err := repo.Upsert(t.Context(), past); err != nil {
		t.Fatalf("Upsert past: %v", err)
	}

	task, err := housekeeping.NewFixFutureRunScheduledTasks(db, slog.Default())
	if err != nil {
		t.Fatalf("NewFixFuture: %v", err)
	}
	task.SetClock(func() time.Time { return now })

	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// future should now equal `now`; past should be unchanged.
	gotFuture, err := repo.GetByTypeName(t.Context(), "FutureTask")
	if err != nil {
		t.Fatalf("Get future: %v", err)
	}
	if !gotFuture.LastExecution.Equal(now) {
		t.Errorf("future LastExecution: %v, want %v (clamped)", gotFuture.LastExecution, now)
	}

	gotPast, err := repo.GetByTypeName(t.Context(), "PastTask")
	if err != nil {
		t.Fatalf("Get past: %v", err)
	}
	if !gotPast.LastExecution.Equal(now.Add(-2 * time.Hour)) {
		t.Errorf("past LastExecution: %v, want unchanged", gotPast.LastExecution)
	}

	// Task.Name surfaces the canonical type name.
	if task.Name() != "FixFutureRunScheduledTasks" {
		t.Errorf("Name: %q", task.Name())
	}
}

func TestFixFutureRunScheduledTasks_NoFutureRows_Noop(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	repo, err := scheduledtasks.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	if err := repo.Upsert(t.Context(), &scheduledtasks.Task{
		TypeName:      "OnlyPast",
		Interval:      60,
		LastExecution: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	task, err := housekeeping.NewFixFutureRunScheduledTasks(db, slog.Default())
	if err != nil {
		t.Fatalf("NewFixFuture: %v", err)
	}
	task.SetClock(func() time.Time { return now })

	before, err := repo.GetByTypeName(t.Context(), "OnlyPast")
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	after, err := repo.GetByTypeName(t.Context(), "OnlyPast")
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if !before.LastExecution.Equal(after.LastExecution) {
		t.Errorf("LastExecution drifted: before=%v after=%v", before.LastExecution, after.LastExecution)
	}
}

func TestFixFutureRunScheduledTasks_ContextCancel(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	task, err := housekeeping.NewFixFutureRunScheduledTasks(db, slog.Default())
	if err != nil {
		t.Fatalf("NewFixFuture: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before the call
	if err := task.Clean(ctx); err == nil {
		t.Errorf("Clean(cancelled): want error from ExecContext")
	}
}
