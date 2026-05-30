package importlistitems_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/importlistitems"
)

func newService(t *testing.T, db *datastore.DB) *importlistitems.Service {
	t.Helper()
	repo, err := importlistitems.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := importlistitems.NewService(repo)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestService_SyncSeriesForList_InsertUpdateDelete(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc := newService(t, db)
	ctx := t.Context()

	// First sync: 3 fresh items on list 1, all inserts.
	round1 := []importlistitems.Item{
		{TvdbID: 100, Title: "Alpha"},
		{TvdbID: 200, Title: "Beta"},
		{ImdbID: ptrString("tt0003"), Title: "Gamma"},
	}
	removed, err := svc.SyncSeriesForList(ctx, round1, 1)
	if err != nil {
		t.Fatalf("Sync round1: %v", err)
	}
	if removed != 0 {
		t.Errorf("round1 removed: got %d, want 0", removed)
	}
	all, err := svc.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("after round1: got %d items, want 3", len(all))
	}

	// Second sync: Alpha kept (title changed → update), Beta dropped,
	// Gamma kept (matched by ImdbId), Delta new (insert). Beta removed.
	round2 := []importlistitems.Item{
		{TvdbID: 100, Title: "Alpha Renamed"},
		{ImdbID: ptrString("tt0003"), Title: "Gamma"},
		{TvdbID: 400, Title: "Delta"},
	}
	removed, err = svc.SyncSeriesForList(ctx, round2, 1)
	if err != nil {
		t.Fatalf("Sync round2: %v", err)
	}
	if removed != 1 {
		t.Errorf("round2 removed: got %d, want 1 (Beta)", removed)
	}
	all, err = svc.All(ctx)
	if err != nil {
		t.Fatalf("All post round2: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("after round2: got %d items, want 3", len(all))
	}
	var sawRename, sawBeta bool
	for _, it := range all {
		if it.TvdbID == 100 && it.Title == "Alpha Renamed" {
			sawRename = true
		}
		if it.TvdbID == 200 {
			sawBeta = true
		}
	}
	if !sawRename {
		t.Error("Alpha was not updated to the renamed title")
	}
	if sawBeta {
		t.Error("Beta should have been deleted")
	}
}

func TestService_SyncSeriesForList_IsolatesByList(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc := newService(t, db)
	ctx := t.Context()

	if _, err := svc.SyncSeriesForList(ctx, []importlistitems.Item{{TvdbID: 1, Title: "L1A"}, {TvdbID: 2, Title: "L1B"}}, 1); err != nil {
		t.Fatalf("sync list 1: %v", err)
	}
	if _, err := svc.SyncSeriesForList(ctx, []importlistitems.Item{{TvdbID: 3, Title: "L2A"}}, 2); err != nil {
		t.Fatalf("sync list 2: %v", err)
	}

	// Re-syncing list 1 with an empty set removes only list 1's rows.
	removed, err := svc.SyncSeriesForList(ctx, nil, 1)
	if err != nil {
		t.Fatalf("sync list 1 empty: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed: got %d, want 2", removed)
	}
	all, err := svc.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 || all[0].ImportListID != 2 {
		t.Errorf("list 2 rows should survive: %+v", all)
	}
}

func TestService_SyncSeriesForList_StampsListID(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc := newService(t, db)
	ctx := t.Context()

	// Pass an item with the wrong/zero ImportListID — Sync must stamp it.
	if _, err := svc.SyncSeriesForList(ctx, []importlistitems.Item{{ImportListID: 99, TvdbID: 7, Title: "X"}}, 5); err != nil {
		t.Fatalf("sync: %v", err)
	}
	all, err := svc.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 || all[0].ImportListID != 5 {
		t.Errorf("ImportListID not stamped to 5: %+v", all)
	}
}

func TestService_DeleteForList(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc := newService(t, db)
	ctx := t.Context()

	if _, err := svc.SyncSeriesForList(ctx, []importlistitems.Item{{TvdbID: 1, Title: "A"}, {TvdbID: 2, Title: "B"}}, 1); err != nil {
		t.Fatalf("sync list 1: %v", err)
	}
	if _, err := svc.SyncSeriesForList(ctx, []importlistitems.Item{{TvdbID: 3, Title: "C"}}, 2); err != nil {
		t.Fatalf("sync list 2: %v", err)
	}

	if err := svc.DeleteForList(ctx, 1); err != nil {
		t.Fatalf("DeleteForList(1): %v", err)
	}
	all, err := svc.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 || all[0].ImportListID != 2 {
		t.Errorf("only list 2 should remain: %+v", all)
	}

	// Idempotent: deleting again (or a never-seen list) is a no-op.
	if err := svc.DeleteForList(ctx, 1); err != nil {
		t.Errorf("DeleteForList(1) idempotent: %v", err)
	}
	if err := svc.DeleteForList(ctx, 888); err != nil {
		t.Errorf("DeleteForList(missing): %v", err)
	}
}

func TestService_SyncSeriesForList_MatchByTmdbAndMal(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc := newService(t, db)
	ctx := t.Context()

	// Seed: one row identified only by TmdbId, one only by MalId.
	if _, err := svc.SyncSeriesForList(ctx, []importlistitems.Item{
		{TmdbID: ptrInt(555), Title: "ByTmdb"},
		{MalID: ptrInt(777), Title: "ByMal"},
	}, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Re-sync with the same external ids but new titles → both update,
	// zero removed.
	removed, err := svc.SyncSeriesForList(ctx, []importlistitems.Item{
		{TmdbID: ptrInt(555), Title: "ByTmdb v2"},
		{MalID: ptrInt(777), Title: "ByMal v2"},
	}, 1)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed: got %d, want 0", removed)
	}
	all, err := svc.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d items, want 2 (both matched, none duplicated)", len(all))
	}
}
