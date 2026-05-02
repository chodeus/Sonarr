package commands_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
)

func ptrTime(t time.Time) *time.Time             { return &t }
func ptrDuration(d time.Duration) *time.Duration { return &d }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := commands.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All initial: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("All initial: got %d, want 0", len(all))
	}

	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)

	// Queued — not yet started, no Duration/Exception.
	queued := &commands.Command{
		Name:     "RefreshSeriesCommand",
		Body:     `{"name":"RefreshSeriesCommand","seriesId":42}`,
		Priority: 0, // Normal
		Status:   1, // Queued
		QueuedAt: now,
		Trigger:  0, // Manual
		Result:   1, // Unknown
	}
	if _, err := repo.Insert(t.Context(), queued); err != nil {
		t.Fatalf("Insert queued: %v", err)
	}

	// Completed — full lifecycle, including Duration round-trip.
	startedAt := now.Add(time.Second)
	endedAt := now.Add(2*time.Minute + 30*time.Second + 750*time.Millisecond)
	dur := 2*time.Minute + 29*time.Second + 750*time.Millisecond
	completed := &commands.Command{
		Name:      "RssSyncCommand",
		Body:      `{"name":"RssSyncCommand"}`,
		Priority:  0,
		Status:    3, // Completed
		QueuedAt:  now,
		StartedAt: ptrTime(startedAt),
		EndedAt:   ptrTime(endedAt),
		Duration:  ptrDuration(dur),
		Exception: "",
		Trigger:   1, // Scheduled
		Result:    0, // Successful
	}
	if _, err := repo.Insert(t.Context(), completed); err != nil {
		t.Fatalf("Insert completed: %v", err)
	}

	// Failed — non-empty Exception.
	failed := &commands.Command{
		Name:      "ApplicationUpdateCommand",
		Body:      `{"name":"ApplicationUpdateCommand"}`,
		Priority:  2, // High
		Status:    4, // Failed
		QueuedAt:  now,
		StartedAt: ptrTime(startedAt),
		EndedAt:   ptrTime(endedAt),
		Duration:  ptrDuration(dur),
		Exception: "Update server unreachable: dial tcp: lookup",
		Trigger:   1,
		Result:    2, // Unsuccessful
	}
	if _, err := repo.Insert(t.Context(), failed); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Round-trip: Duration parses back to the same time.Duration.
	got, err := repo.Get(t.Context(), completed.ID)
	if err != nil {
		t.Fatalf("Get completed: %v", err)
	}
	if got.Duration == nil || *got.Duration != dur {
		t.Errorf("Duration round-trip: got %v, want %v", got.Duration, dur)
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(startedAt) {
		t.Errorf("StartedAt: %v", got.StartedAt)
	}
	if got.Body != completed.Body {
		t.Errorf("Body: %s", got.Body)
	}
	if got.QueuedAt.Location() != time.UTC {
		t.Errorf("QueuedAt timezone: %v", got.QueuedAt.Location())
	}

	gotQueued, err := repo.Get(t.Context(), queued.ID)
	if err != nil {
		t.Fatalf("Get queued: %v", err)
	}
	if gotQueued.StartedAt != nil || gotQueued.EndedAt != nil || gotQueued.Duration != nil {
		t.Errorf("queued nullables not nil: %+v", gotQueued)
	}
	if gotQueued.Exception != "" {
		t.Errorf("queued Exception: %q", gotQueued.Exception)
	}

	gotFailed, err := repo.Get(t.Context(), failed.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if gotFailed.Exception == "" {
		t.Errorf("failed Exception not preserved")
	}

	// AllByStatus(1) — just the queued row.
	queuedRows, err := repo.AllByStatus(t.Context(), 1)
	if err != nil {
		t.Fatalf("AllByStatus(1): %v", err)
	}
	if len(queuedRows) != 1 || queuedRows[0].ID != queued.ID {
		t.Errorf("queuedRows: %+v", queuedRows)
	}

	if err := repo.Update(t.Context(), &commands.Command{ID: 99999, Name: "x", Body: "{}", QueuedAt: now}); !errors.Is(err, commands.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), queued.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), queued.ID); !errors.Is(err, commands.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
