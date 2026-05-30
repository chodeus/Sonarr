package importlists_test

import (
	"context"
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/series"
)

// --- clean-library stubs (J501) ---

type stubCleanConfig struct {
	level string
	tag   int
}

func (s stubCleanConfig) ListSyncLevel() string { return s.level }
func (s stubCleanConfig) ListSyncTag() int      { return s.tag }

type stubItemLister struct {
	items []importlists.LibraryItem
}

func (s *stubItemLister) AllItems(context.Context) ([]importlists.LibraryItem, error) {
	return s.items, nil
}

type stubSeriesCleaner struct {
	series       []series.Series
	updated      []series.Series
	updateCalled bool
}

func (s *stubSeriesCleaner) AllSeries(context.Context) ([]series.Series, error) {
	return s.series, nil
}

func (s *stubSeriesCleaner) UpdateSeries(_ context.Context, batch []series.Series) error {
	s.updateCalled = true
	s.updated = append(s.updated, batch...)
	return nil
}

type stubCleanGate struct {
	pending    bool
	gateCalled bool
	markCalled bool
	gotListIDs []int
}

func (s *stubCleanGate) AllListsSuccessfulWithAPendingClean(_ context.Context, listIDs []int) (bool, error) {
	s.gateCalled = true
	s.gotListIDs = listIDs
	return s.pending, nil
}

func (s *stubCleanGate) MarkListsAsCleaned(context.Context) error {
	s.markCalled = true
	return nil
}

// cleanHarness builds a SyncService with a single enabled list (empty
// fetch) plus the clean-library stubs, and runs a full Sync (which ends
// in tryCleanLibrary).
func cleanHarness(t *testing.T, cfg stubCleanConfig, items *stubItemLister, cleaner *stubSeriesCleaner, gate *stubCleanGate) {
	t.Helper()
	rows := []importlists.ImportList{makeRow(1, "L", boolPtr(true))}
	factory := buildSyncFactory(t, rows, nil, nil) // empty fetch → clean, no adds
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, &stubSeriesAdder{}, nil)
	svc.SetCleanLibrary(cfg, items, cleaner, gate)
	if _, err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

func TestCleanLibrary_Disabled_FullNoOp(t *testing.T) {
	t.Parallel()
	items := &stubItemLister{}
	cleaner := &stubSeriesCleaner{series: []series.Series{{ID: 1, TvdbID: 100, Monitored: true}}}
	gate := &stubCleanGate{pending: true}
	cleanHarness(t, stubCleanConfig{level: "Disabled"}, items, cleaner, gate)

	if gate.gateCalled {
		t.Error("Disabled must short-circuit before the pending-clean gate")
	}
	if cleaner.updateCalled || gate.markCalled {
		t.Error("Disabled must not mutate or mark-cleaned")
	}
}

func TestCleanLibrary_GateFalse_NoClean(t *testing.T) {
	t.Parallel()
	items := &stubItemLister{}
	cleaner := &stubSeriesCleaner{series: []series.Series{{ID: 1, TvdbID: 100, Monitored: true}}}
	gate := &stubCleanGate{pending: false}
	cleanHarness(t, stubCleanConfig{level: "KeepAndUnmonitor"}, items, cleaner, gate)

	if !gate.gateCalled {
		t.Error("gate should be consulted when not Disabled")
	}
	if cleaner.updateCalled || gate.markCalled {
		t.Error("no pending clean → must not mutate or mark-cleaned")
	}
}

func TestCleanLibrary_LogOnly_LogsButDoesNotMutate(t *testing.T) {
	t.Parallel()
	items := &stubItemLister{} // no list items → every series is off-list
	cleaner := &stubSeriesCleaner{series: []series.Series{
		{ID: 1, TvdbID: 100, Monitored: true},
		{ID: 2, TvdbID: 200, Monitored: true},
	}}
	gate := &stubCleanGate{pending: true}
	cleanHarness(t, stubCleanConfig{level: "LogOnly"}, items, cleaner, gate)

	// UpdateSeries is still called (C# passes an empty list), but with no
	// mutated rows; MarkListsAsCleaned runs.
	if len(cleaner.updated) != 0 {
		t.Errorf("LogOnly must not mutate any series, got %d", len(cleaner.updated))
	}
	if !gate.markCalled {
		t.Error("LogOnly with a pending clean should mark lists cleaned")
	}
}

func TestCleanLibrary_KeepAndUnmonitor(t *testing.T) {
	t.Parallel()
	// List item 200 keeps series 2 on-list; series 1 (off-list, monitored)
	// is unmonitored; series 3 (off-list, already unmonitored) is skipped.
	// Off-list series carry non-zero distinct TmdbIDs so the faithful C#
	// TmdbId==0 quirk (covered separately) doesn't keep them.
	items := &stubItemLister{items: []importlists.LibraryItem{{TvdbID: 200, TmdbID: 2000}}}
	cleaner := &stubSeriesCleaner{series: []series.Series{
		{ID: 1, TvdbID: 100, TmdbID: 1000, Monitored: true},
		{ID: 2, TvdbID: 200, TmdbID: 2000, Monitored: true},
		{ID: 3, TvdbID: 300, TmdbID: 3000, Monitored: false},
	}}
	gate := &stubCleanGate{pending: true}
	cleanHarness(t, stubCleanConfig{level: "KeepAndUnmonitor"}, items, cleaner, gate)

	if len(cleaner.updated) != 1 {
		t.Fatalf("expected exactly 1 series updated, got %d (%+v)", len(cleaner.updated), cleaner.updated)
	}
	u := cleaner.updated[0]
	if u.ID != 1 || u.Monitored {
		t.Errorf("expected series 1 unmonitored, got %+v", u)
	}
	if !gate.markCalled {
		t.Error("should mark lists cleaned")
	}
	// The gate received the auto-add list id (1).
	if len(gate.gotListIDs) != 1 || gate.gotListIDs[0] != 1 {
		t.Errorf("gate list ids: %v want [1]", gate.gotListIDs)
	}
}

func TestCleanLibrary_KeepAndTag(t *testing.T) {
	t.Parallel()
	items := &stubItemLister{} // all off-list
	cleaner := &stubSeriesCleaner{series: []series.Series{
		{ID: 1, TvdbID: 100, Tags: []int{9}},    // missing tag 7 → tagged
		{ID: 2, TvdbID: 200, Tags: []int{7}},    // already tagged → skip
		{ID: 3, TvdbID: 300, Tags: []int{7, 9}}, // already tagged → skip
	}}
	gate := &stubCleanGate{pending: true}
	cleanHarness(t, stubCleanConfig{level: "KeepAndTag", tag: 7}, items, cleaner, gate)

	if len(cleaner.updated) != 1 {
		t.Fatalf("expected exactly 1 series tagged, got %d (%+v)", len(cleaner.updated), cleaner.updated)
	}
	u := cleaner.updated[0]
	if u.ID != 1 || !containsTag(u.Tags, 7) {
		t.Errorf("expected series 1 to gain tag 7, got %+v", u)
	}
	if !containsTag(u.Tags, 9) {
		t.Errorf("existing tag 9 must be preserved, got %+v", u.Tags)
	}
}

func TestCleanLibrary_MatchesOnEveryIdentifier(t *testing.T) {
	t.Parallel()
	// Each series is matched on a different identifier and must be KEPT.
	// Every list item carries a distinct non-zero TmdbId so the off-list
	// series (TmdbId 5000) isn't falsely kept by the TmdbId==0 quirk.
	items := &stubItemLister{items: []importlists.LibraryItem{
		{TvdbID: 100, TmdbID: 1000},
		{ImdbID: "tt0002", TmdbID: 2000},
		{MalID: 555, TmdbID: 3000},
		{AniListID: 777, TmdbID: 4000},
	}}
	cleaner := &stubSeriesCleaner{series: []series.Series{
		{ID: 1, TvdbID: 100, TmdbID: 1000, Monitored: true},                         // by tvdb
		{ID: 2, TvdbID: 201, ImdbID: "tt0002", TmdbID: 2000, Monitored: true},       // by imdb
		{ID: 3, TvdbID: 202, MalIDs: []int{555}, TmdbID: 3000, Monitored: true},     // by mal
		{ID: 4, TvdbID: 203, AniListIDs: []int{777}, TmdbID: 4000, Monitored: true}, // by anilist
		{ID: 5, TvdbID: 999, TmdbID: 5000, Monitored: true},                         // off-list → unmonitor
	}}
	gate := &stubCleanGate{pending: true}
	cleanHarness(t, stubCleanConfig{level: "KeepAndUnmonitor"}, items, cleaner, gate)

	if len(cleaner.updated) != 1 || cleaner.updated[0].ID != 5 {
		t.Errorf("only the off-list series 5 should be unmonitored, got %+v", cleaner.updated)
	}
}

// TestCleanLibrary_TmdbZeroQuirk pins the faithful C# behaviour: because
// `l.TmdbId == series.TmdbId` has no >0 guard (ImportListSyncService.cs:307),
// a series with TmdbId==0 is treated as on-list whenever ANY list item
// also has TmdbId==0 — so it is NOT unmonitored. This is conservative
// (keeps the series) and matches Sonarr exactly.
func TestCleanLibrary_TmdbZeroQuirk(t *testing.T) {
	t.Parallel()
	// One list item for a different series, with TmdbId left at 0.
	items := &stubItemLister{items: []importlists.LibraryItem{{TvdbID: 200}}}
	cleaner := &stubSeriesCleaner{series: []series.Series{
		// Off-list by TvdbId (100≠200) but TmdbId 0 == the item's TmdbId 0
		// → kept (quirk).
		{ID: 1, TvdbID: 100, TmdbID: 0, Monitored: true},
	}}
	gate := &stubCleanGate{pending: true}
	cleanHarness(t, stubCleanConfig{level: "KeepAndUnmonitor"}, items, cleaner, gate)

	if len(cleaner.updated) != 0 {
		t.Errorf("TmdbId==0 quirk should keep the series, got %+v", cleaner.updated)
	}
}

func containsTag(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
