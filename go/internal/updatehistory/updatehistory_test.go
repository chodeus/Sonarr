package updatehistory_test

import (
	"context"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/lifecycle"
	"sonarr.tv/sonarr/internal/updatehistory"
)

func newRepo(t *testing.T) *updatehistory.Repo {
	t.Helper()
	r, err := updatehistory.NewRepo(datastoretest.OpenSQLiteLog(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return r
}

func TestNewRepoNilDB(t *testing.T) {
	t.Parallel()
	if _, err := updatehistory.NewRepo(nil); err == nil {
		t.Fatal("NewRepo(nil) = nil err, want ErrNilDB")
	}
}

func TestInsertAndLastInstalled(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	versions := []string{"5.0.0.1", "5.0.0.2", "5.0.0.3"}
	for i, v := range versions {
		if err := r.Insert(ctx, &updatehistory.History{
			Date:      base.Add(time.Duration(i) * time.Hour),
			Version:   v,
			EventType: updatehistory.EventTypeInstalled,
		}); err != nil {
			t.Fatalf("Insert %s: %v", v, err)
		}
	}

	last, err := r.LastInstalled(ctx)
	if err != nil {
		t.Fatalf("LastInstalled: %v", err)
	}
	if last == nil || last.Version != "5.0.0.3" {
		t.Fatalf("LastInstalled = %+v, want 5.0.0.3", last)
	}

	prev, err := r.PreviouslyInstalled(ctx)
	if err != nil {
		t.Fatalf("PreviouslyInstalled: %v", err)
	}
	if prev == nil || prev.Version != "5.0.0.2" {
		t.Fatalf("PreviouslyInstalled = %+v, want 5.0.0.2", prev)
	}
}

func TestLastInstalledEmpty(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	last, err := r.LastInstalled(context.Background())
	if err != nil {
		t.Fatalf("LastInstalled: %v", err)
	}
	if last != nil {
		t.Fatalf("LastInstalled on empty = %+v, want nil", last)
	}
}

func TestInstalledSinceFiltersAndOrders(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// Two installed rows + one non-installed (Initiated) that must be excluded.
	if err := r.Insert(ctx, &updatehistory.History{Date: base, Version: "5.0.0.1", EventType: updatehistory.EventTypeInstalled}); err != nil {
		t.Fatal(err)
	}
	if err := r.Insert(ctx, &updatehistory.History{Date: base.Add(48 * time.Hour), Version: "5.0.0.2", EventType: updatehistory.EventTypeInstalled}); err != nil {
		t.Fatal(err)
	}
	if err := r.Insert(ctx, &updatehistory.History{Date: base.Add(72 * time.Hour), Version: "5.0.0.9", EventType: updatehistory.EventTypeInitiated}); err != nil {
		t.Fatal(err)
	}

	since, err := r.InstalledSince(ctx, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("InstalledSince: %v", err)
	}
	if len(since) != 1 || since[0].Version != "5.0.0.2" {
		t.Fatalf("InstalledSince = %+v, want only 5.0.0.2", since)
	}
}

func TestPurge(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	if err := r.Insert(ctx, &updatehistory.History{Date: time.Now(), Version: "5.0.0.1", EventType: updatehistory.EventTypeInstalled}); err != nil {
		t.Fatal(err)
	}
	if err := r.Purge(ctx); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	last, err := r.LastInstalled(ctx)
	if err != nil {
		t.Fatalf("LastInstalled: %v", err)
	}
	if last != nil {
		t.Fatalf("after Purge LastInstalled = %+v, want nil", last)
	}
}

// --- Service ---

func TestServiceRecordsOnFreshInstall(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	svc := updatehistory.NewService(r, "5.0.0.5", nil)

	if err := svc.HandleApplicationStarted(ctx, lifecycle.ApplicationStartedEvent{}); err != nil {
		t.Fatalf("HandleApplicationStarted: %v", err)
	}
	// Fresh install: a row recorded, no previous version.
	if pv := svc.PrevVersion(); pv != "" {
		t.Fatalf("PrevVersion = %q, want empty on fresh install", pv)
	}
	last, _ := r.LastInstalled(ctx)
	if last == nil || last.Version != "5.0.0.5" {
		t.Fatalf("recorded = %+v, want 5.0.0.5", last)
	}
}

func TestServiceRecordsPrevVersionOnUpgrade(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	// Seed a prior install.
	if err := r.Insert(ctx, &updatehistory.History{Date: time.Now().Add(-time.Hour), Version: "5.0.0.4", EventType: updatehistory.EventTypeInstalled}); err != nil {
		t.Fatal(err)
	}
	svc := updatehistory.NewService(r, "5.0.0.5", nil)
	if err := svc.HandleApplicationStarted(ctx, lifecycle.ApplicationStartedEvent{}); err != nil {
		t.Fatalf("HandleApplicationStarted: %v", err)
	}
	if pv := svc.PrevVersion(); pv != "5.0.0.4" {
		t.Fatalf("PrevVersion = %q, want 5.0.0.4", pv)
	}
	last, _ := r.LastInstalled(ctx)
	if last == nil || last.Version != "5.0.0.5" {
		t.Fatalf("recorded = %+v, want 5.0.0.5", last)
	}
}

func TestServiceSkipsWhenVersionUnchanged(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	if err := r.Insert(ctx, &updatehistory.History{Date: time.Now().Add(-time.Hour), Version: "5.0.0.5", EventType: updatehistory.EventTypeInstalled}); err != nil {
		t.Fatal(err)
	}
	svc := updatehistory.NewService(r, "5.0.0.5", nil)
	if err := svc.HandleApplicationStarted(ctx, lifecycle.ApplicationStartedEvent{}); err != nil {
		t.Fatal(err)
	}
	// No new row, no prevVersion.
	if pv := svc.PrevVersion(); pv != "" {
		t.Fatalf("PrevVersion = %q, want empty", pv)
	}
	all, _ := r.InstalledSince(ctx, time.Now().Add(-24*time.Hour))
	if len(all) != 1 {
		t.Fatalf("rows = %d, want 1 (no duplicate)", len(all))
	}
}

func TestServiceSkipsDevBuild(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	for _, v := range []string{"dev", "10.0.0.1"} {
		svc := updatehistory.NewService(r, v, nil)
		if err := svc.HandleApplicationStarted(ctx, lifecycle.ApplicationStartedEvent{}); err != nil {
			t.Fatalf("HandleApplicationStarted(%s): %v", v, err)
		}
	}
	last, _ := r.LastInstalled(ctx)
	if last != nil {
		t.Fatalf("dev build recorded = %+v, want nothing", last)
	}
}

func TestServicePreviouslyInstalled(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	svc := updatehistory.NewService(r, "5.0.0.5", nil)
	if pv := svc.PreviouslyInstalled(ctx); pv != "" {
		t.Fatalf("PreviouslyInstalled on empty = %q, want empty", pv)
	}
	base := time.Now().Add(-2 * time.Hour)
	_ = r.Insert(ctx, &updatehistory.History{Date: base, Version: "5.0.0.4", EventType: updatehistory.EventTypeInstalled})
	_ = r.Insert(ctx, &updatehistory.History{Date: base.Add(time.Hour), Version: "5.0.0.5", EventType: updatehistory.EventTypeInstalled})
	if pv := svc.PreviouslyInstalled(ctx); pv != "5.0.0.4" {
		t.Fatalf("PreviouslyInstalled = %q, want 5.0.0.4", pv)
	}
}
