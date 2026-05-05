package housekeeping_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/tags"
)

func TestNewCleanupUnusedTags_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupUnusedTags(nil, nil, nil); !errors.Is(err, housekeeping.ErrNilDB) {
		t.Errorf("NewCleanupUnusedTags(nil): %v, want ErrNilDB", err)
	}
}

func TestCleanupUnusedTags_Sqlite(t *testing.T) {
	t.Parallel()
	runCleanupUnusedTagsSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupUnusedTags_Postgres(t *testing.T) {
	runCleanupUnusedTagsSuite(t, datastoretest.OpenPostgres(t))
}

func runCleanupUnusedTagsSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	tagsRepo, err := tags.NewRepo(db)
	if err != nil {
		t.Fatalf("tags.NewRepo: %v", err)
	}

	// Seed 6 tags. Each assigned to a different owner table so we
	// exercise the cross-table union. Two are intentionally unassigned
	// so the housekeeper has rows to clean.
	mkTag := func(label string) int {
		row := &tags.Tag{Label: label}
		if _, err := tagsRepo.Insert(t.Context(), row); err != nil {
			t.Fatalf("tags Insert %q: %v", label, err)
		}
		return row.ID
	}
	tagSeries := mkTag("series-tag")
	tagDelay := mkTag("delay-tag")
	tagAutoTag := mkTag("autotag-tag")   // referenced via AutoTagging.Tags column
	tagAutoSpec := mkTag("autospec-tag") // referenced via AutoTagging.Specifications TagSpec.Value
	tagOrphan1 := mkTag("orphan-1")
	tagOrphan2 := mkTag("orphan-2")
	_ = tagOrphan1
	_ = tagOrphan2

	// Series row references tagSeries.
	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	if _, err := seriesRepo.Insert(t.Context(), &series.Series{
		TvdbID:     1001,
		Title:      "Show",
		CleanTitle: "show",
		TitleSlug:  "show",
		Path:       "/tv/show",
		Tags:       []int{tagSeries},
		Seasons:    "[]",
		Genres:     "[]",
		Images:     "[]",
	}); err != nil {
		t.Fatalf("series Insert: %v", err)
	}

	// DelayProfile row references tagDelay.
	delayRepo, err := delayprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("delayprofiles.NewRepo: %v", err)
	}
	if _, err := delayRepo.Insert(t.Context(), &delayprofiles.Profile{
		PreferredProtocol: 1,
		EnableUsenet:      true,
		EnableTorrent:     true,
		Order:             10,
		Tags:              []int{tagDelay},
	}); err != nil {
		t.Fatalf("delayprofiles Insert: %v", err)
	}

	// AutoTagging row: Tags column references tagAutoTag, Specifications
	// column references tagAutoSpec via a TagSpecification.
	autoRepo, err := autotagging.NewRepo(db)
	if err != nil {
		t.Fatalf("autotagging.NewRepo: %v", err)
	}
	specJSON, err := autotagging.MarshalSpecs([]autotagging.Specification{
		&autotagging.TagSpec{
			BaseSpecification: autotagging.BaseSpecification{Name: "tagspec"},
			Value:             tagAutoSpec,
		},
	})
	if err != nil {
		t.Fatalf("MarshalSpecs: %v", err)
	}
	if _, err := autoRepo.Insert(t.Context(), &autotagging.AutoTag{
		Name:           "auto-tag-row",
		Specifications: string(specJSON),
		Tags:           []int{tagAutoTag},
	}); err != nil {
		t.Fatalf("autotagging Insert: %v", err)
	}

	// Seed empty rows in the remaining 5 owner tables to make sure the
	// housekeeper's Tags='[]' guard works (these contribute zero tags
	// to the used set).
	notifRepo, _ := notifications.NewRepo(db)
	cc := "DiscordSettings"
	_, _ = notifRepo.Insert(t.Context(), &notifications.Notification{
		Name: "n", Implementation: "Discord", ConfigContract: &cc, Settings: "{}", Tags: []int{},
	})
	rpRepo, _ := releaseprofiles.NewRepo(db)
	_, _ = rpRepo.Insert(t.Context(), &releaseprofiles.Profile{
		Required: []string{}, Ignored: []string{}, Tags: []int{}, ExcludedTags: []int{}, IndexerIDs: []int{},
	})
	listsRepo, _ := importlists.NewRepo(db)
	_, _ = listsRepo.Insert(t.Context(), &importlists.ImportList{
		Name: "il", Implementation: "RSS", ConfigContract: ptrString("RssListSettings"),
		Settings: ptrString("{}"), RootFolderPath: "/tv", ShouldMonitor: 1, Tags: []int{}, QualityProfileID: 1,
	})
	idxRepo, _ := indexers.NewRepo(db)
	_, _ = idxRepo.Insert(t.Context(), &indexers.Indexer{
		Name: "i", Implementation: "Newznab", EnableInteractiveSearch: true, Priority: 25, Tags: []int{},
	})
	dcRepo, _ := downloadclients.NewRepo(db)
	_, _ = dcRepo.Insert(t.Context(), &downloadclients.DownloadClient{
		Name: "d", Implementation: "QBittorrent", Priority: 1, Tags: []int{},
	})

	// Run the housekeeper with autotagging wired so TagSpec values
	// participate in the used set.
	task, err := housekeeping.NewCleanupUnusedTags(db, autoRepo, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if task.Name() != "CleanupUnusedTags" {
		t.Errorf("Name: %q", task.Name())
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// Survivors: tagSeries / tagDelay / tagAutoTag / tagAutoSpec.
	for _, id := range []int{tagSeries, tagDelay, tagAutoTag, tagAutoSpec} {
		if _, err := tagsRepo.Get(t.Context(), id); err != nil {
			t.Errorf("tag id=%d should have survived: %v", id, err)
		}
	}
	// Orphans: removed.
	for _, id := range []int{tagOrphan1, tagOrphan2} {
		if _, err := tagsRepo.Get(t.Context(), id); err == nil {
			t.Errorf("tag id=%d should have been swept", id)
		}
	}

	// Idempotency: re-run is a no-op (no remaining unused rows).
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean (re-run): %v", err)
	}
	for _, id := range []int{tagSeries, tagDelay, tagAutoTag, tagAutoSpec} {
		if _, err := tagsRepo.Get(t.Context(), id); err != nil {
			t.Errorf("tag id=%d went missing after re-run: %v", id, err)
		}
	}
}

func TestCleanupUnusedTags_NoAutoTagRepo_StillSweepsOrphans(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	tagsRepo, err := tags.NewRepo(db)
	if err != nil {
		t.Fatalf("tags.NewRepo: %v", err)
	}
	live := &tags.Tag{Label: "live"}
	orphan := &tags.Tag{Label: "orphan"}
	if _, err := tagsRepo.Insert(t.Context(), live); err != nil {
		t.Fatalf("Insert live: %v", err)
	}
	if _, err := tagsRepo.Insert(t.Context(), orphan); err != nil {
		t.Fatalf("Insert orphan: %v", err)
	}

	// Reference `live` via a Series row.
	seriesRepo, _ := series.NewRepo(db)
	if _, err := seriesRepo.Insert(t.Context(), &series.Series{
		TvdbID: 2002, Title: "S", CleanTitle: "s", TitleSlug: "s", Path: "/tv/s",
		Tags: []int{live.ID}, Seasons: "[]", Genres: "[]", Images: "[]",
	}); err != nil {
		t.Fatalf("series Insert: %v", err)
	}

	task, err := housekeeping.NewCleanupUnusedTags(db, nil, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	if _, err := tagsRepo.Get(t.Context(), live.ID); err != nil {
		t.Errorf("live tag dropped: %v", err)
	}
	if _, err := tagsRepo.Get(t.Context(), orphan.ID); err == nil {
		t.Errorf("orphan tag survived")
	}
}

func TestCleanupUnusedTags_NoUsedTags_SweepsEverything(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	tagsRepo, err := tags.NewRepo(db)
	if err != nil {
		t.Fatalf("tags.NewRepo: %v", err)
	}
	for _, label := range []string{"a", "b", "c"} {
		if _, err := tagsRepo.Insert(t.Context(), &tags.Tag{Label: label}); err != nil {
			t.Fatalf("Insert %s: %v", label, err)
		}
	}

	task, err := housekeeping.NewCleanupUnusedTags(db, nil, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	all, err := tagsRepo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("Tags after sweep: %d, want 0", len(all))
	}
}

func TestCleanupUnusedTags_ContextCancel(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	task, err := housekeeping.NewCleanupUnusedTags(db, nil, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := task.Clean(ctx); err == nil {
		t.Errorf("Clean(cancelled): want error from QueryContext")
	}
}
