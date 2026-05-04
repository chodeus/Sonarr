package commands_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
)

func runHelpersSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	r, err := commands.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	// Seed: 2 queued, 1 started, 1 completed (old enough to trim).
	seeds := []commands.Command{
		{Name: "RefreshSeries", Body: "{}", Status: int(commands.StatusQueued), QueuedAt: now, Result: int(commands.ResultUnknown)},
		{Name: "RefreshSeries", Body: `{"id":1}`, Status: int(commands.StatusQueued), QueuedAt: now.Add(time.Second), Result: int(commands.ResultUnknown)},
		{Name: "Backup", Body: "{}", Status: int(commands.StatusStarted), QueuedAt: now, StartedAt: ptrTime(now), Result: int(commands.ResultUnknown)},
		{
			Name: "Backup", Body: "{}",
			Status:   int(commands.StatusCompleted),
			QueuedAt: now.Add(-48 * time.Hour),
			EndedAt:  ptrTime(now.Add(-2 * time.Hour)), // older than 1-day cutoff
			Result:   int(commands.ResultSuccessful),
		},
	}
	for i := range seeds {
		if _, err := r.Insert(t.Context(), &seeds[i]); err != nil {
			t.Fatalf("Insert seed %d: %v", i, err)
		}
	}

	// Queued — exactly 2 rows, ordered by QueuedAt asc.
	queued, err := r.Queued(t.Context())
	if err != nil {
		t.Fatalf("Queued: %v", err)
	}
	if len(queued) != 2 {
		t.Fatalf("Queued: got %d, want 2", len(queued))
	}
	if queued[0].Body != "{}" || queued[1].Body != `{"id":1}` {
		t.Errorf("Queued ordering: %+v", queued)
	}

	// Started — exactly 1 row.
	started, err := r.Started(t.Context())
	if err != nil {
		t.Fatalf("Started: %v", err)
	}
	if len(started) != 1 || started[0].Name != "Backup" {
		t.Errorf("Started: %+v", started)
	}

	// FindByName — 2 RefreshSeries rows, ordered DESC.
	hits, err := r.FindByName(t.Context(), "RefreshSeries")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("FindByName: got %d, want 2", len(hits))
	}
	if !hits[0].QueuedAt.After(hits[1].QueuedAt) {
		t.Errorf("FindByName: not ordered DESC by QueuedAt")
	}
	if missing, err := r.FindByName(t.Context(), "NoSuch"); err != nil || len(missing) != 0 {
		t.Errorf("FindByName(missing): %v / %d", err, len(missing))
	}

	// Trim with cutoff = now - 1h drops the old Backup row only.
	cutoff := now.Add(-1 * time.Hour)
	deleted, err := r.Trim(t.Context(), cutoff)
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if deleted != 1 {
		t.Errorf("Trim: rows=%d, want 1", deleted)
	}
	all, err := r.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("after Trim: %d remain, want 3", len(all))
	}

	// OrphanStarted: the Started Backup row flips to Orphaned.
	orphaned, err := r.OrphanStarted(t.Context())
	if err != nil {
		t.Fatalf("OrphanStarted: %v", err)
	}
	if orphaned != 1 {
		t.Errorf("OrphanStarted: rows=%d, want 1", orphaned)
	}
	got, err := r.Get(t.Context(), seeds[2].ID)
	if err != nil {
		t.Fatalf("Get after OrphanStarted: %v", err)
	}
	if commands.Status(got.Status) != commands.StatusOrphaned {
		t.Errorf("after OrphanStarted: Status=%v, want Orphaned", commands.Status(got.Status))
	}
	if got.EndedAt == nil {
		t.Errorf("after OrphanStarted: EndedAt nil, want set")
	}

	// Start: take a Queued row → Started + StartedAt.
	cmd, err := r.Get(t.Context(), seeds[0].ID)
	if err != nil {
		t.Fatalf("Get queued: %v", err)
	}
	cmd.Status = int(commands.StatusStarted)
	startTime := time.Date(2026, 5, 5, 13, 0, 0, 0, time.UTC)
	if err := r.Start(t.Context(), cmd, startTime); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if cmd.StartedAt == nil || !cmd.StartedAt.Equal(startTime) {
		t.Errorf("Start: in-place StartedAt: %v, want %v", cmd.StartedAt, startTime)
	}
	got, err = r.Get(t.Context(), cmd.ID)
	if err != nil {
		t.Fatalf("Get after Start: %v", err)
	}
	if commands.Status(got.Status) != commands.StatusStarted {
		t.Errorf("after Start: Status=%v, want Started", commands.Status(got.Status))
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(startTime) {
		t.Errorf("after Start: persisted StartedAt: %v, want %v", got.StartedAt, startTime)
	}

	// End: same row → Completed + EndedAt + Duration + Exception.
	cmd.Status = int(commands.StatusCompleted)
	dur := 5 * time.Second
	cmd.Duration = &dur
	cmd.Exception = ""
	endTime := startTime.Add(dur)
	if err := r.End(t.Context(), cmd, endTime); err != nil {
		t.Fatalf("End: %v", err)
	}
	got, err = r.Get(t.Context(), cmd.ID)
	if err != nil {
		t.Fatalf("Get after End: %v", err)
	}
	if commands.Status(got.Status) != commands.StatusCompleted {
		t.Errorf("after End: Status=%v, want Completed", commands.Status(got.Status))
	}
	if got.EndedAt == nil || !got.EndedAt.Equal(endTime) {
		t.Errorf("after End: persisted EndedAt: %v, want %v", got.EndedAt, endTime)
	}
	if got.Duration == nil || *got.Duration != dur {
		t.Errorf("after End: persisted Duration: %v, want %v", got.Duration, dur)
	}

	// Start / End on missing rows return ErrNotFound.
	ghost := &commands.Command{ID: 99999}
	if err := r.Start(t.Context(), ghost, time.Now()); err == nil {
		t.Errorf("Start(missing): want error")
	}
	if err := r.End(t.Context(), ghost, time.Now()); err == nil {
		t.Errorf("End(missing): want error")
	}

	// Empty-input edges.
	if _, err := r.Trim(t.Context(), now.Add(-100*time.Hour)); err != nil {
		t.Errorf("Trim with no matches: %v", err)
	}
}

func TestHelpers_SQLite(t *testing.T) {
	t.Parallel()
	runHelpersSuite(t, datastoretest.OpenSQLite(t))
}

func TestHelpers_Postgres(t *testing.T) {
	runHelpersSuite(t, datastoretest.OpenPostgres(t))
}
