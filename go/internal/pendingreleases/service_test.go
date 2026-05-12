package pendingreleases_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/pendingreleases"
)

// makeService builds a Service against the supplied DB. The bus is
// optional — pass nil to verify the event-publish path skips silently.
func makeService(t *testing.T, db *datastore.DB, bus *messaging.Bus) *pendingreleases.Service {
	t.Helper()
	repo, err := pendingreleases.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := pendingreleases.NewService(repo, bus, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// makeInput builds an Input whose three matching fields are set and
// whose ReleaseInfo JSON contains the publishDate + indexer the
// matching predicate reads back. ParsedEpisodeInfo / AdditionalInfo
// stay empty strings (the Repo mapper substitutes `{}` for those).
func makeInput(seriesID int, title, indexer string, pubDate time.Time) pendingreleases.Input {
	relJSON, _ := json.Marshal(map[string]any{
		"title":       title,
		"publishDate": pubDate,
		"indexer":     indexer,
	})
	return pendingreleases.Input{
		SeriesID:        seriesID,
		MatchTitle:      title,
		MatchPubDate:    pubDate,
		MatchIndexer:    indexer,
		ReleaseInfoJSON: string(relJSON),
	}
}

func TestNewService_RejectsNilRepo(t *testing.T) {
	t.Parallel()
	_, err := pendingreleases.NewService(nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil Repo, got nil")
	}
}

func TestAddMany_EmptyBatchIsNoop(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc := makeService(t, db, nil)
	if err := svc.AddMany(t.Context(), nil); err != nil {
		t.Fatalf("AddMany(nil): %v", err)
	}
	if err := svc.AddMany(t.Context(), []pendingreleases.AddItem{}); err != nil {
		t.Fatalf("AddMany([]): %v", err)
	}
}

func TestAddMany_ZeroSeriesIDDropped(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc := makeService(t, db, nil)
	repo, _ := pendingreleases.NewRepo(db)

	in := makeInput(0, "Anything", "Newznab", time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC))
	if err := svc.Add(t.Context(), in, pendingreleases.ReasonDelay); err != nil {
		t.Fatalf("Add: %v", err)
	}
	rows, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0 (zero-SeriesID should be skipped)", len(rows))
	}
}

func runAddManyBehaviorSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	pub := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	repo, _ := pendingreleases.NewRepo(db)
	svc := makeService(t, db, nil)

	in := makeInput(7, "Show.S01E01", "Newznab", pub)

	// First add inserts.
	if err := svc.Add(t.Context(), in, pendingreleases.ReasonDelay); err != nil {
		t.Fatalf("Add 1: %v", err)
	}
	rows, _ := repo.AllBySeriesID(t.Context(), 7)
	if len(rows) != 1 {
		t.Fatalf("after 1 Add: rows = %d, want 1", len(rows))
	}
	insertedID := rows[0].ID
	if rows[0].Reason != int(pendingreleases.ReasonDelay) {
		t.Errorf("reason = %d, want Delay", rows[0].Reason)
	}

	// Second add with same Reason → skipped.
	if err := svc.Add(t.Context(), in, pendingreleases.ReasonDelay); err != nil {
		t.Fatalf("Add 2 (same reason): %v", err)
	}
	rows, _ = repo.AllBySeriesID(t.Context(), 7)
	if len(rows) != 1 || rows[0].ID != insertedID {
		t.Errorf("after 2nd Add same reason: rows = %d (want 1, same id)", len(rows))
	}

	// Third add with DIFFERENT reason (Delay → Fallback) → update.
	if err := svc.Add(t.Context(), in, pendingreleases.ReasonFallback); err != nil {
		t.Fatalf("Add 3 (different reason): %v", err)
	}
	rows, _ = repo.AllBySeriesID(t.Context(), 7)
	if len(rows) != 1 {
		t.Fatalf("after reason transition: rows = %d, want 1", len(rows))
	}
	if rows[0].Reason != int(pendingreleases.ReasonFallback) {
		t.Errorf("reason after transition = %d, want Fallback", rows[0].Reason)
	}

	// Fourth: flip to DownloadClientUnavailable.
	if err := svc.Add(t.Context(), in, pendingreleases.ReasonDownloadClientUnavailable); err != nil {
		t.Fatalf("Add 4: %v", err)
	}
	rows, _ = repo.AllBySeriesID(t.Context(), 7)
	if rows[0].Reason != int(pendingreleases.ReasonDownloadClientUnavailable) {
		t.Errorf("reason after DCU transition = %d", rows[0].Reason)
	}

	// Fifth: trying to downgrade FROM DownloadClientUnavailable → preserved.
	if err := svc.Add(t.Context(), in, pendingreleases.ReasonDelay); err != nil {
		t.Fatalf("Add 5 (DCU sticky): %v", err)
	}
	rows, _ = repo.AllBySeriesID(t.Context(), 7)
	if rows[0].Reason != int(pendingreleases.ReasonDownloadClientUnavailable) {
		t.Errorf("DCU should be sticky, got reason = %d", rows[0].Reason)
	}
}

func TestAddMany_BehaviorSuite_SQLite(t *testing.T) {
	t.Parallel()
	runAddManyBehaviorSuite(t, datastoretest.OpenSQLite(t))
}

func TestAddMany_BehaviorSuite_Postgres(t *testing.T) {
	runAddManyBehaviorSuite(t, datastoretest.OpenPostgres(t))
}

func TestAddMany_DuplicatesCollapsedToFirst(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := pendingreleases.NewRepo(db)
	svc := makeService(t, db, nil)
	pub := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	// Pre-seed two duplicate rows that bypass the Service's dedup
	// (simulates a row from before slice 4.BQ landed, or a concurrent
	// insert race). Both share (SeriesID=7, Title, PublishDate,
	// Indexer).
	pre1 := &pendingreleases.Release{
		SeriesID:    7,
		Title:       "Dup.Title",
		Added:       pub,
		ReleaseInfo: `{"title":"Dup.Title","publishDate":"2026-05-13T12:00:00Z","indexer":"Newznab"}`,
		Reason:      int(pendingreleases.ReasonDelay),
	}
	pre2 := &pendingreleases.Release{
		SeriesID:    7,
		Title:       "Dup.Title",
		Added:       pub,
		ReleaseInfo: `{"title":"Dup.Title","publishDate":"2026-05-13T12:00:00Z","indexer":"Newznab"}`,
		Reason:      int(pendingreleases.ReasonDelay),
	}
	if _, err := repo.Insert(t.Context(), pre1); err != nil {
		t.Fatalf("preseed 1: %v", err)
	}
	if _, err := repo.Insert(t.Context(), pre2); err != nil {
		t.Fatalf("preseed 2: %v", err)
	}

	in := makeInput(7, "Dup.Title", "Newznab", pub)
	if err := svc.Add(t.Context(), in, pendingreleases.ReasonDelay); err != nil {
		t.Fatalf("Add: %v", err)
	}
	rows, _ := repo.AllBySeriesID(t.Context(), 7)
	if len(rows) != 1 {
		t.Errorf("after dedup pass: rows = %d, want 1 (duplicates collapsed)", len(rows))
	}
}

func TestAddMany_GroupsByDifferentSeries(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := pendingreleases.NewRepo(db)
	svc := makeService(t, db, nil)
	pub := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	items := []pendingreleases.AddItem{
		{Input: makeInput(1, "Alpha", "IdxA", pub), Reason: pendingreleases.ReasonDelay},
		{Input: makeInput(2, "Bravo", "IdxA", pub), Reason: pendingreleases.ReasonDelay},
		{Input: makeInput(1, "Charlie", "IdxB", pub.Add(time.Hour)), Reason: pendingreleases.ReasonFallback},
	}
	if err := svc.AddMany(t.Context(), items); err != nil {
		t.Fatalf("AddMany: %v", err)
	}

	all, _ := repo.All(t.Context())
	if len(all) != 3 {
		t.Errorf("rows = %d, want 3 (different match tuples = no dedup)", len(all))
	}
	s1, _ := repo.AllBySeriesID(t.Context(), 1)
	s2, _ := repo.AllBySeriesID(t.Context(), 2)
	if len(s1) != 2 {
		t.Errorf("series 1 rows = %d, want 2", len(s1))
	}
	if len(s2) != 1 {
		t.Errorf("series 2 rows = %d, want 1", len(s2))
	}
}

func TestAddMany_MismatchedReleaseInfoJSONStillInserts(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := pendingreleases.NewRepo(db)
	svc := makeService(t, db, nil)
	pub := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	// Pre-seed a row whose stored ReleaseInfo JSON is malformed —
	// matchingRows silently skips that row, so the new Add inserts.
	pre := &pendingreleases.Release{
		SeriesID:    7,
		Title:       "Mismatched",
		Added:       pub,
		ReleaseInfo: `{NOT JSON`,
		Reason:      int(pendingreleases.ReasonDelay),
	}
	if _, err := repo.Insert(t.Context(), pre); err != nil {
		t.Fatalf("preseed: %v", err)
	}
	if err := svc.Add(t.Context(), makeInput(7, "Mismatched", "Newznab", pub), pendingreleases.ReasonDelay); err != nil {
		t.Fatalf("Add: %v", err)
	}
	rows, _ := repo.AllBySeriesID(t.Context(), 7)
	if len(rows) != 2 {
		t.Errorf("rows = %d, want 2 (malformed pre-row treated as no-match)", len(rows))
	}
}

func TestAddMany_PublishesUpdatedEventWhenChanged(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	bus := messaging.New(nil)

	got := make(chan pendingreleases.PendingReleasesUpdatedEvent, 4)
	messaging.Subscribe(bus, "pendingreleases-test", func(_ context.Context, e pendingreleases.PendingReleasesUpdatedEvent) error {
		got <- e
		return nil
	})

	svc := makeService(t, db, bus)
	in := makeInput(11, "Evt.Show", "IdxE", time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC))

	if err := svc.Add(t.Context(), in, pendingreleases.ReasonDelay); err != nil {
		t.Fatalf("Add 1: %v", err)
	}
	// Same-reason re-add → no DB change → no event.
	if err := svc.Add(t.Context(), in, pendingreleases.ReasonDelay); err != nil {
		t.Fatalf("Add 2: %v", err)
	}

	// We expect EXACTLY one event from the first Add.
	select {
	case <-got:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected first Add to publish PendingReleasesUpdatedEvent")
	}
	select {
	case e := <-got:
		t.Errorf("unexpected second event: %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}
