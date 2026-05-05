package housekeeping_test

import (
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

func TestUpdateCleanTitleForSeries_Sqlite(t *testing.T) {
	t.Parallel()
	runUpdateCleanTitleSuite(t, datastoretest.OpenSQLite(t))
}

func TestUpdateCleanTitleForSeries_Postgres(t *testing.T) {
	runUpdateCleanTitleSuite(t, datastoretest.OpenPostgres(t))
}

func runUpdateCleanTitleSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	added := time.Now().UTC()

	// Seed three rows:
	//   - drift: stored CleanTitle "wrong"; should be rewritten to
	//     parser.CleanSeriesTitle(title).
	//   - aligned: stored CleanTitle already correct; should be left
	//     alone.
	//   - drift2: another drifting row.
	drift := &series.Series{TvdbID: 400, Title: "Foo: The Best Show!", CleanTitle: "wrong", TitleSlug: "foo-best", Path: "/tv/foo", Added: &added, Status: 1, Tags: []int{}, MalIDs: []int{}, Images: "[]", Seasons: "[]"}
	want := parser.CleanSeriesTitle("Foo: The Best Show!")
	aligned := &series.Series{TvdbID: 401, Title: "Bar", CleanTitle: parser.CleanSeriesTitle("Bar"), TitleSlug: "bar", Path: "/tv/bar", Added: &added, Status: 1, Tags: []int{}, MalIDs: []int{}, Images: "[]", Seasons: "[]"}
	drift2 := &series.Series{TvdbID: 402, Title: "Café Naïve", CleanTitle: "stale", TitleSlug: "cafe-naive", Path: "/tv/cafe", Added: &added, Status: 1, Tags: []int{}, MalIDs: []int{}, Images: "[]", Seasons: "[]"}
	for i, row := range []*series.Series{drift, aligned, drift2} {
		if _, err := repo.Insert(t.Context(), row); err != nil {
			t.Fatalf("Insert idx=%d: %v", i, err)
		}
	}

	task, err := housekeeping.NewUpdateCleanTitleForSeries(repo, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	gotDrift, err := repo.Get(t.Context(), drift.ID)
	if err != nil {
		t.Fatalf("Get drift: %v", err)
	}
	if gotDrift.CleanTitle != want {
		t.Errorf("drift CleanTitle: %q, want %q", gotDrift.CleanTitle, want)
	}

	gotAligned, err := repo.Get(t.Context(), aligned.ID)
	if err != nil {
		t.Fatalf("Get aligned: %v", err)
	}
	if gotAligned.CleanTitle != parser.CleanSeriesTitle("Bar") {
		t.Errorf("aligned CleanTitle drifted: %q", gotAligned.CleanTitle)
	}

	gotDrift2, err := repo.Get(t.Context(), drift2.ID)
	if err != nil {
		t.Fatalf("Get drift2: %v", err)
	}
	if gotDrift2.CleanTitle != parser.CleanSeriesTitle("Café Naïve") {
		t.Errorf("drift2 CleanTitle: %q, want %q", gotDrift2.CleanTitle, parser.CleanSeriesTitle("Café Naïve"))
	}

	// Idempotency: running again is a no-op.
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean(rerun): %v", err)
	}
}

func TestNewUpdateCleanTitleForSeries_NilRepo(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewUpdateCleanTitleForSeries(nil, nil); err == nil {
		t.Errorf("nil repo: want error")
	}
}
