package scheduledtasks_test

import (
	"errors"
	"sort"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

func newService(t *testing.T, t0 time.Time) (*scheduledtasks.Service, *scheduledtasks.Repo) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := scheduledtasks.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := scheduledtasks.NewService(r)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetClock(func() time.Time { return t0 })
	return svc, r
}

func TestService_NewServiceErrors(t *testing.T) {
	t.Parallel()
	if _, err := scheduledtasks.NewService(nil); err == nil {
		t.Errorf("NewService(nil) want error")
	}
}

func TestService_RegisterDefaults_FreshSeed(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc, r := newService(t, t0)
	defaults := []*scheduledtasks.Task{
		{TypeName: "Sonarr.Go.RefreshSeriesCommand", Interval: 720},
		{TypeName: "Sonarr.Go.HousekeepingCommand", Interval: 1440},
	}
	if err := svc.RegisterDefaults(t.Context(), defaults); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	all, err := r.All(t.Context())
	if err != nil {
		t.Fatalf("repo.All: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("repo.All: got %d, want 2", len(all))
	}
	for _, row := range all {
		if !row.LastExecution.Equal(t0) {
			t.Errorf("fresh row %q: LastExecution=%v, want %v", row.TypeName, row.LastExecution, t0)
		}
	}
	cached := svc.GetAll(t.Context())
	if len(cached) != 2 {
		t.Errorf("GetAll: got %d, want 2", len(cached))
	}
}

func TestService_RegisterDefaults_ReconcilesAndPreservesLastExecution(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc, r := newService(t, t0)

	// Pre-seed: one keeper + one orphan.
	priorExec := time.Date(2026, 5, 5, 6, 0, 0, 0, time.UTC)
	priorStart := time.Date(2026, 5, 5, 6, 0, 5, 0, time.UTC)
	keeper := &scheduledtasks.Task{
		TypeName:      "Sonarr.Go.RefreshSeriesCommand",
		Interval:      60,
		LastExecution: priorExec,
		LastStartTime: &priorStart,
	}
	orphan := &scheduledtasks.Task{
		TypeName:      "Sonarr.Go.RetiredCommand",
		Interval:      30,
		LastExecution: priorExec,
	}
	if _, err := r.Insert(t.Context(), keeper); err != nil {
		t.Fatalf("Insert keeper: %v", err)
	}
	if _, err := r.Insert(t.Context(), orphan); err != nil {
		t.Fatalf("Insert orphan: %v", err)
	}

	// Register a new default set: keeper (with a different Interval) + new task.
	defaults := []*scheduledtasks.Task{
		{TypeName: "Sonarr.Go.RefreshSeriesCommand", Interval: 720},
		{TypeName: "Sonarr.Go.NewCommand", Interval: 5},
	}
	if err := svc.RegisterDefaults(t.Context(), defaults); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}

	// Orphan deleted.
	if _, err := r.GetByTypeName(t.Context(), "Sonarr.Go.RetiredCommand"); !errors.Is(err, scheduledtasks.ErrNotFound) {
		t.Errorf("orphan should be deleted: %v", err)
	}

	// Keeper preserves Id + LastExecution + LastStartTime, picks up new Interval.
	post, err := r.GetByTypeName(t.Context(), "Sonarr.Go.RefreshSeriesCommand")
	if err != nil {
		t.Fatalf("GetByTypeName keeper: %v", err)
	}
	if post.ID != keeper.ID {
		t.Errorf("keeper Id: got %d, want %d", post.ID, keeper.ID)
	}
	if !post.LastExecution.Equal(priorExec) {
		t.Errorf("keeper LastExecution: got %v, want %v", post.LastExecution, priorExec)
	}
	if post.LastStartTime == nil || !post.LastStartTime.Equal(priorStart) {
		t.Errorf("keeper LastStartTime: got %v, want %v", post.LastStartTime, priorStart)
	}
	if post.Interval != 720 {
		t.Errorf("keeper Interval: got %d, want 720", post.Interval)
	}

	// New row: LastExecution stamped from clock.
	fresh, err := r.GetByTypeName(t.Context(), "Sonarr.Go.NewCommand")
	if err != nil {
		t.Fatalf("GetByTypeName fresh: %v", err)
	}
	if !fresh.LastExecution.Equal(t0) {
		t.Errorf("fresh LastExecution: got %v, want %v", fresh.LastExecution, t0)
	}
}

func TestService_RegisterDefaults_RejectsBadInput(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, t0)

	if err := svc.RegisterDefaults(t.Context(), []*scheduledtasks.Task{nil}); err == nil {
		t.Errorf("nil default: expected error")
	}
	if err := svc.RegisterDefaults(t.Context(), []*scheduledtasks.Task{{}}); err == nil {
		t.Errorf("empty TypeName: expected error")
	}
	dup := []*scheduledtasks.Task{
		{TypeName: "Sonarr.Go.A", Interval: 5},
		{TypeName: "Sonarr.Go.A", Interval: 10},
	}
	if err := svc.RegisterDefaults(t.Context(), dup); err == nil {
		t.Errorf("duplicate TypeName: expected error")
	}
}

func TestService_GetPending_FiltersByDueness(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc, r := newService(t, t0)

	// Pre-seed three rows: one due, one not yet due, one with Interval=0 (disabled).
	dueLast := t0.Add(-2 * time.Hour) // last ran 2h ago, interval 60m → due
	freshLast := t0.Add(-30 * time.Minute)
	for _, row := range []*scheduledtasks.Task{
		{TypeName: "Sonarr.Go.Due", Interval: 60, LastExecution: dueLast},
		{TypeName: "Sonarr.Go.NotYet", Interval: 60, LastExecution: freshLast},
		{TypeName: "Sonarr.Go.Disabled", Interval: 0, LastExecution: dueLast},
	} {
		if _, err := r.Insert(t.Context(), row); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	defaults := []*scheduledtasks.Task{
		{TypeName: "Sonarr.Go.Due", Interval: 60},
		{TypeName: "Sonarr.Go.NotYet", Interval: 60},
		{TypeName: "Sonarr.Go.Disabled", Interval: 0},
	}
	if err := svc.RegisterDefaults(t.Context(), defaults); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}

	pending := svc.GetPending(t.Context())
	if len(pending) != 1 {
		names := make([]string, 0, len(pending))
		for _, p := range pending {
			names = append(names, p.TypeName)
		}
		sort.Strings(names)
		t.Fatalf("GetPending: got %d (%v), want 1", len(pending), names)
	}
	if pending[0].TypeName != "Sonarr.Go.Due" {
		t.Errorf("GetPending: got %q, want Sonarr.Go.Due", pending[0].TypeName)
	}
}

func TestService_GetNextExecution(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc, r := newService(t, t0)
	last := time.Date(2026, 5, 5, 11, 0, 0, 0, time.UTC)
	if _, err := r.Insert(t.Context(), &scheduledtasks.Task{
		TypeName:      "Sonarr.Go.RefreshSeriesCommand",
		Interval:      60,
		LastExecution: last,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := svc.RegisterDefaults(t.Context(), []*scheduledtasks.Task{
		{TypeName: "Sonarr.Go.RefreshSeriesCommand", Interval: 60},
	}); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}

	next, err := svc.GetNextExecution(t.Context(), "Sonarr.Go.RefreshSeriesCommand")
	if err != nil {
		t.Fatalf("GetNextExecution: %v", err)
	}
	want := last.Add(60 * time.Minute)
	if !next.Equal(want) {
		t.Errorf("GetNextExecution: got %v, want %v", next, want)
	}

	if _, err := svc.GetNextExecution(t.Context(), "Sonarr.Go.Missing"); !errors.Is(err, scheduledtasks.ErrNotFound) {
		t.Errorf("GetNextExecution(missing): %v", err)
	}
}

func TestService_HandleCommandExecuted_UpdatesPersistedAndCache(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc, r := newService(t, t0)
	if err := svc.RegisterDefaults(t.Context(), []*scheduledtasks.Task{
		{TypeName: "Sonarr.Go.RefreshSeriesCommand", Interval: 60},
	}); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}

	// Move the clock forward and fire an executed event.
	t1 := t0.Add(45 * time.Minute)
	svc.SetClock(func() time.Time { return t1 })
	startedAt := t0.Add(40 * time.Minute)
	cmd := &commands.Command{
		Name:      "Sonarr.Go.RefreshSeriesCommand",
		StartedAt: &startedAt,
	}
	if err := svc.HandleCommandExecuted(t.Context(), commands.CommandExecutedEvent{Command: cmd}); err != nil {
		t.Fatalf("HandleCommandExecuted: %v", err)
	}

	// DB row updated.
	post, err := r.GetByTypeName(t.Context(), "Sonarr.Go.RefreshSeriesCommand")
	if err != nil {
		t.Fatalf("GetByTypeName: %v", err)
	}
	if !post.LastExecution.Equal(t1) {
		t.Errorf("DB LastExecution: got %v, want %v", post.LastExecution, t1)
	}
	if post.LastStartTime == nil || !post.LastStartTime.Equal(startedAt) {
		t.Errorf("DB LastStartTime: got %v, want %v", post.LastStartTime, startedAt)
	}

	// Cache also updated — GetNextExecution sees the new LastExecution.
	next, err := svc.GetNextExecution(t.Context(), "Sonarr.Go.RefreshSeriesCommand")
	if err != nil {
		t.Fatalf("GetNextExecution: %v", err)
	}
	want := t1.Add(60 * time.Minute)
	if !next.Equal(want) {
		t.Errorf("cache LastExecution: got next=%v, want %v", next, want)
	}
}

func TestService_HandleCommandExecuted_SkipsUnregistered(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, t0)
	startedAt := t0.Add(-time.Minute)
	cmd := &commands.Command{Name: "Sonarr.Go.NotScheduled", StartedAt: &startedAt}
	if err := svc.HandleCommandExecuted(t.Context(), commands.CommandExecutedEvent{Command: cmd}); err != nil {
		t.Errorf("HandleCommandExecuted unregistered: %v", err)
	}
}

func TestService_HandleCommandExecuted_PredicateGate(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc, r := newService(t, t0)
	priorExec := time.Date(2026, 5, 5, 6, 0, 0, 0, time.UTC)
	if _, err := r.Insert(t.Context(), &scheduledtasks.Task{
		TypeName:      "Sonarr.Go.RefreshSeriesCommand",
		Interval:      60,
		LastExecution: priorExec,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := svc.RegisterDefaults(t.Context(), []*scheduledtasks.Task{
		{TypeName: "Sonarr.Go.RefreshSeriesCommand", Interval: 60},
	}); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}

	// Predicate that always rejects — execution should leave LastExecution untouched.
	svc.SetShouldUpdatePredicate(func(typeName string) bool { return false })

	startedAt := t0.Add(40 * time.Minute)
	cmd := &commands.Command{Name: "Sonarr.Go.RefreshSeriesCommand", StartedAt: &startedAt}
	if err := svc.HandleCommandExecuted(t.Context(), commands.CommandExecutedEvent{Command: cmd}); err != nil {
		t.Fatalf("HandleCommandExecuted: %v", err)
	}
	post, err := r.GetByTypeName(t.Context(), "Sonarr.Go.RefreshSeriesCommand")
	if err != nil {
		t.Fatalf("GetByTypeName: %v", err)
	}
	if !post.LastExecution.Equal(priorExec) {
		t.Errorf("predicate rejected, but LastExecution moved: got %v, want %v", post.LastExecution, priorExec)
	}
}

func TestService_HandleCommandExecuted_NilSafety(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, t0)
	if err := svc.HandleCommandExecuted(t.Context(), commands.CommandExecutedEvent{}); err != nil {
		t.Errorf("nil command: %v", err)
	}
	cmd := &commands.Command{Name: "x"} // StartedAt nil
	if err := svc.HandleCommandExecuted(t.Context(), commands.CommandExecutedEvent{Command: cmd}); err != nil {
		t.Errorf("nil StartedAt: %v", err)
	}
}
