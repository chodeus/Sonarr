package housekeeping_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

func TestNewCleanupQualityProfileFormatItems_NilArgs(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	qpRepo, err := qualityprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("qualityprofiles.NewRepo: %v", err)
	}
	cfRepo, err := customformats.NewRepo(db)
	if err != nil {
		t.Fatalf("customformats.NewRepo: %v", err)
	}
	if _, err := housekeeping.NewCleanupQualityProfileFormatItems(nil, cfRepo, nil); err == nil {
		t.Errorf("nil qpRepo: want error")
	}
	if _, err := housekeeping.NewCleanupQualityProfileFormatItems(qpRepo, nil, nil); err == nil {
		t.Errorf("nil cfRepo: want error")
	}
}

func TestCleanupQualityProfileFormatItems_Sqlite(t *testing.T) {
	t.Parallel()
	runQPFormatItemsSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupQualityProfileFormatItems_Postgres(t *testing.T) {
	runQPFormatItemsSuite(t, datastoretest.OpenPostgres(t))
}

func runQPFormatItemsSuite(t *testing.T, db *datastore.DB) {
	t.Helper()

	cfRepo, err := customformats.NewRepo(db)
	if err != nil {
		t.Fatalf("customformats.NewRepo: %v", err)
	}
	mkCF := func(name string) int {
		row := &customformats.CustomFormat{Name: name, Specifications: "[]"}
		if _, err := cfRepo.Insert(t.Context(), row); err != nil {
			t.Fatalf("customformats Insert %s: %v", name, err)
		}
		return row.ID
	}
	cf1 := mkCF("cf-one")
	cf2 := mkCF("cf-two")
	cf3 := mkCF("cf-three")

	qpRepo, err := qualityprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("qualityprofiles.NewRepo: %v", err)
	}

	// Profile A: drift in both directions — references cf2 (preserve
	// score) plus a phantom 999 that no longer exists. Missing cf1+cf3
	// should be prepended.
	driftItems, err := qualityprofiles.MarshalFormatItems([]qualityprofiles.FormatItem{
		{FormatID: cf2, Score: 50},
		{FormatID: 999, Score: 100},
	})
	if err != nil {
		t.Fatalf("marshal drift items: %v", err)
	}
	profileA := &qualityprofiles.Profile{
		Name: "drifted", Cutoff: 1, Items: "[]",
		FormatItems:           driftItems,
		MinFormatScore:        10,
		CutoffFormatScore:     20,
		MinUpgradeFormatScore: 5,
	}
	if _, err := qpRepo.Insert(t.Context(), profileA); err != nil {
		t.Fatalf("insert profileA: %v", err)
	}

	// Profile B: in-sync — references all 3 CFs, no phantoms.
	syncItems, err := qualityprofiles.MarshalFormatItems([]qualityprofiles.FormatItem{
		{FormatID: cf1, Score: 5},
		{FormatID: cf2, Score: 10},
		{FormatID: cf3, Score: 15},
	})
	if err != nil {
		t.Fatalf("marshal sync items: %v", err)
	}
	profileB := &qualityprofiles.Profile{
		Name: "in-sync", Cutoff: 1, Items: "[]",
		FormatItems:           syncItems,
		MinFormatScore:        7,
		CutoffFormatScore:     8,
		MinUpgradeFormatScore: 9,
	}
	if _, err := qpRepo.Insert(t.Context(), profileB); err != nil {
		t.Fatalf("insert profileB: %v", err)
	}

	task, err := housekeeping.NewCleanupQualityProfileFormatItems(qpRepo, cfRepo, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if task.Name() != "CleanupQualityProfileFormatItems" {
		t.Errorf("Name: %q", task.Name())
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// Profile A: phantom dropped, missing prepended, cf2 score preserved.
	gotA, err := qpRepo.Get(t.Context(), profileA.ID)
	if err != nil {
		t.Fatalf("Get profileA: %v", err)
	}
	gotItemsA, err := qualityprofiles.ParseFormatItems(gotA.FormatItems)
	if err != nil {
		t.Fatalf("Parse profileA items: %v", err)
	}
	wantSet := map[int]int{cf1: 0, cf2: 50, cf3: 0}
	if len(gotItemsA) != len(wantSet) {
		t.Fatalf("profileA item count: got %d, want %d", len(gotItemsA), len(wantSet))
	}
	for _, item := range gotItemsA {
		if item.FormatID == 999 {
			t.Errorf("phantom FormatID 999 still present")
		}
		want, ok := wantSet[item.FormatID]
		if !ok {
			t.Errorf("unexpected FormatID %d", item.FormatID)
			continue
		}
		if item.Score != want {
			t.Errorf("FormatID %d Score: got %d, want %d", item.FormatID, item.Score, want)
		}
	}
	// Score knobs are NOT reset — list is non-empty.
	if gotA.MinFormatScore != 10 || gotA.CutoffFormatScore != 20 || gotA.MinUpgradeFormatScore != 5 {
		t.Errorf("profileA scores drifted: %+v", gotA)
	}

	// Profile B: byte-for-byte unchanged.
	gotB, err := qpRepo.Get(t.Context(), profileB.ID)
	if err != nil {
		t.Fatalf("Get profileB: %v", err)
	}
	if gotB.FormatItems != syncItems {
		t.Errorf("profileB FormatItems drifted:\n  got=%q\n want=%q", gotB.FormatItems, syncItems)
	}
	if gotB.MinFormatScore != 7 || gotB.CutoffFormatScore != 8 || gotB.MinUpgradeFormatScore != 9 {
		t.Errorf("profileB scores drifted: %+v", gotB)
	}

	// Idempotency: second run is a no-op.
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean re-run: %v", err)
	}
	gotA2, _ := qpRepo.Get(t.Context(), profileA.ID)
	if gotA2.FormatItems != gotA.FormatItems {
		t.Errorf("profileA drifted on re-run:\n  before=%q\n   after=%q", gotA.FormatItems, gotA2.FormatItems)
	}
}

func TestCleanupQualityProfileFormatItems_EmptyResetsScores(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)

	cfRepo, _ := customformats.NewRepo(db)
	// No CustomFormats seeded.

	qpRepo, _ := qualityprofiles.NewRepo(db)
	// Profile references a phantom CF and has non-default scores.
	driftItems, _ := qualityprofiles.MarshalFormatItems([]qualityprofiles.FormatItem{
		{FormatID: 7, Score: 999},
	})
	profile := &qualityprofiles.Profile{
		Name: "all-phantom", Cutoff: 1, Items: "[]",
		FormatItems:           driftItems,
		MinFormatScore:        100,
		CutoffFormatScore:     200,
		MinUpgradeFormatScore: 300,
	}
	if _, err := qpRepo.Insert(t.Context(), profile); err != nil {
		t.Fatalf("insert: %v", err)
	}

	task, err := housekeeping.NewCleanupQualityProfileFormatItems(qpRepo, cfRepo, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	got, err := qpRepo.Get(t.Context(), profile.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FormatItems != "[]" {
		t.Errorf("FormatItems: got %q, want []", got.FormatItems)
	}
	if got.MinFormatScore != 0 || got.CutoffFormatScore != 0 || got.MinUpgradeFormatScore != 1 {
		t.Errorf("score knobs not reset: Min=%d Cutoff=%d Upgrade=%d, want 0/0/1",
			got.MinFormatScore, got.CutoffFormatScore, got.MinUpgradeFormatScore)
	}
}

func TestCleanupQualityProfileFormatItems_ScoreOnlyChange_Untouched(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)

	cfRepo, _ := customformats.NewRepo(db)
	row := &customformats.CustomFormat{Name: "cf", Specifications: "[]"}
	if _, err := cfRepo.Insert(t.Context(), row); err != nil {
		t.Fatalf("Insert cf: %v", err)
	}

	qpRepo, _ := qualityprofiles.NewRepo(db)
	// User-edited score: 42. Set is correct (matches live CFs); the
	// housekeeper must not stomp the score.
	items, _ := qualityprofiles.MarshalFormatItems([]qualityprofiles.FormatItem{
		{FormatID: row.ID, Score: 42},
	})
	profile := &qualityprofiles.Profile{
		Name: "user-score", Cutoff: 1, Items: "[]",
		FormatItems:           items,
		MinFormatScore:        99,
		CutoffFormatScore:     99,
		MinUpgradeFormatScore: 99,
	}
	if _, err := qpRepo.Insert(t.Context(), profile); err != nil {
		t.Fatalf("insert: %v", err)
	}
	task, _ := housekeeping.NewCleanupQualityProfileFormatItems(qpRepo, cfRepo, slog.Default())
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	got, err := qpRepo.Get(t.Context(), profile.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	gotItems, _ := qualityprofiles.ParseFormatItems(got.FormatItems)
	if len(gotItems) != 1 || gotItems[0].FormatID != row.ID || gotItems[0].Score != 42 {
		t.Errorf("FormatItems mutated: %+v", gotItems)
	}
	if got.MinFormatScore != 99 || got.CutoffFormatScore != 99 || got.MinUpgradeFormatScore != 99 {
		t.Errorf("scores stomped: %+v", got)
	}
}

func TestCleanupQualityProfileFormatItems_ContextCancel(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	qpRepo, _ := qualityprofiles.NewRepo(db)
	cfRepo, _ := customformats.NewRepo(db)
	task, _ := housekeeping.NewCleanupQualityProfileFormatItems(qpRepo, cfRepo, slog.Default())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := task.Clean(ctx); err == nil {
		t.Errorf("Clean(cancelled): want error from QueryContext")
	}
}
