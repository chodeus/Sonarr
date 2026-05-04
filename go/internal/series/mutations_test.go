package series_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

func newServiceWithBus(t *testing.T) (*series.Service, *messaging.Bus) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, err := series.NewService(repo, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, bus
}

func sampleSeries(tvdbID int, title string) *series.Series {
	return &series.Series{
		TvdbID:     tvdbID,
		Title:      title,
		CleanTitle: title,
		Status:     1,
		Images:     "[]",
		Path:       "/tv/" + title,
	}
}

func TestService_AddSeries_PublishesSeriesAddedEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	var got atomic.Int32
	var lastID atomic.Int32
	messaging.Subscribe(bus, "test.series.added",
		func(_ context.Context, ev series.SeriesAddedEvent) error {
			got.Add(1)
			if ev.Series != nil {
				lastID.Store(int32(ev.Series.ID))
			}
			return nil
		})

	added, err := svc.AddSeries(t.Context(), sampleSeries(361753, "Mando"))
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if added.ID == 0 {
		t.Errorf("AddSeries: expected non-zero ID")
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 SeriesAddedEvent, got %d", got.Load())
	}
	if int(lastID.Load()) != added.ID {
		t.Errorf("event payload ID=%d want %d", lastID.Load(), added.ID)
	}
}

func TestService_AddSeries_RejectsNil(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	if _, err := svc.AddSeries(t.Context(), nil); err == nil {
		t.Errorf("AddSeries(nil) should error")
	}
}

func TestService_AddSeries_NilBusSkipsPublish(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	_ = svc // just ensure no panic; nil-bus svc constructed in fixture helper
	// Re-build a fresh nil-bus Service.
	repo, err := series.NewRepo(datastoretest.OpenSQLite(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	nilBus, err := series.NewService(repo, nil)
	if err != nil {
		t.Fatalf("NewService(nil bus): %v", err)
	}
	if _, err := nilBus.AddSeries(t.Context(), sampleSeries(123, "X")); err != nil {
		t.Errorf("AddSeries(nil bus): %v", err)
	}
}

func TestService_AddMany_PublishesSeriesImportedEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	var got atomic.Int32
	var ids atomic.Pointer[[]int]
	messaging.Subscribe(bus, "test.series.imported",
		func(_ context.Context, ev series.SeriesImportedEvent) error {
			got.Add(1)
			cp := append([]int(nil), ev.SeriesIDs...)
			ids.Store(&cp)
			return nil
		})

	list := []*series.Series{
		sampleSeries(1, "A"),
		sampleSeries(2, "B"),
		sampleSeries(3, "C"),
	}
	out, err := svc.AddMany(t.Context(), list)
	if err != nil {
		t.Fatalf("AddMany: %v", err)
	}
	if len(out) != 3 {
		t.Errorf("AddMany: returned len=%d", len(out))
	}
	if got.Load() != 1 {
		t.Errorf("expected exactly 1 SeriesImportedEvent, got %d", got.Load())
	}
	got2 := ids.Load()
	if got2 == nil || len(*got2) != 3 {
		t.Errorf("event SeriesIDs: %v", got2)
	}
}

func TestService_UpdateSeries_PublishesEditedEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	added, err := svc.AddSeries(t.Context(), sampleSeries(361753, "Mando"))
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	var got atomic.Int32
	var lastEv atomic.Pointer[series.SeriesEditedEvent]
	messaging.Subscribe(bus, "test.series.edited",
		func(_ context.Context, ev series.SeriesEditedEvent) error {
			got.Add(1)
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	added.Title = "The Mandalorian"
	added.CleanTitle = "themandalorian"
	updated, err := svc.UpdateSeries(t.Context(), added, series.UpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateSeries: %v", err)
	}
	if updated.Title != "The Mandalorian" {
		t.Errorf("Title not persisted: %q", updated.Title)
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 SeriesEditedEvent, got %d", got.Load())
	}
	ev := lastEv.Load()
	if ev == nil {
		t.Fatal("nil event")
	}
	if ev.Series == nil || ev.OldSeries == nil {
		t.Errorf("event missing Series/OldSeries: %+v", ev)
	}
	if ev.Series.Title != "The Mandalorian" || ev.OldSeries.Title != "Mando" {
		t.Errorf("event diff: new=%q old=%q", ev.Series.Title, ev.OldSeries.Title)
	}
	if ev.EpisodesChanged {
		t.Errorf("EpisodesChanged should be false this slice (deferred)")
	}
}

func TestService_UpdateSeries_SkipPublish(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	added, err := svc.AddSeries(t.Context(), sampleSeries(1, "X"))
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	var got atomic.Int32
	messaging.Subscribe(bus, "test.series.edited.skip",
		func(_ context.Context, _ series.SeriesEditedEvent) error {
			got.Add(1)
			return nil
		})

	added.Title = "X2"
	if _, err := svc.UpdateSeries(t.Context(), added, series.UpdateOptions{SkipPublishUpdatedEvent: true}); err != nil {
		t.Fatalf("UpdateSeries: %v", err)
	}
	if got.Load() != 0 {
		t.Errorf("SkipPublishUpdatedEvent didn't suppress event; got %d", got.Load())
	}
}

func TestService_UpdateSeries_PreservesAddOptions(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	row := sampleSeries(1, "X")
	row.AddOptions = `{"searchForMissingEpisodes":true}`
	added, err := svc.AddSeries(t.Context(), row)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	// Caller-supplied AddOptions should be ignored.
	added.AddOptions = `{"searchForMissingEpisodes":false}`
	added.Title = "X2"
	updated, err := svc.UpdateSeries(t.Context(), added, series.UpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateSeries: %v", err)
	}
	if updated.AddOptions != `{"searchForMissingEpisodes":true}` {
		t.Errorf("AddOptions overwritten: got %q want preserved", updated.AddOptions)
	}
}

func TestService_UpdateSeries_RejectsNil(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	if _, err := svc.UpdateSeries(t.Context(), nil, series.UpdateOptions{}); err == nil {
		t.Errorf("UpdateSeries(nil) should error")
	}
}

func TestService_RemoveAddOptions(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	row := sampleSeries(1, "X")
	row.AddOptions = `{"searchForMissingEpisodes":true}`
	added, err := svc.AddSeries(t.Context(), row)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if err := svc.RemoveAddOptions(t.Context(), added.ID); err != nil {
		t.Fatalf("RemoveAddOptions: %v", err)
	}
	got, err := svc.GetSeries(t.Context(), added.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.AddOptions != "" {
		t.Errorf("AddOptions not cleared: %q", got.AddOptions)
	}
}

// fakeTagProvider implements TagChangeProvider with a fixed delta.
type fakeTagProvider struct {
	add    map[int]struct{}
	remove map[int]struct{}
	err    error
}

func (f *fakeTagProvider) GetTagChanges(_ context.Context, _ *series.Series) (map[int]struct{}, map[int]struct{}, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.add, f.remove, nil
}

func TestService_UpdateTags_AppliesDelta(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	provider := &fakeTagProvider{
		add:    map[int]struct{}{2: {}, 5: {}}, // 2 already present; only 5 should be added
		remove: map[int]struct{}{1: {}},        // 1 removed; 7 absent so no-op
	}
	svc.SetTagChangeProvider(provider)

	row := sampleSeries(1, "X")
	row.Tags = []int{1, 2, 7}
	added, err := svc.AddSeries(t.Context(), row)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	changed, err := svc.UpdateTags(t.Context(), added)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}
	if !changed {
		t.Errorf("expected changed=true (1 removed, 5 added)")
	}
	expected := map[int]bool{2: true, 5: true, 7: true}
	for _, tag := range added.Tags {
		if !expected[tag] {
			t.Errorf("unexpected tag %d in result", tag)
		}
		delete(expected, tag)
	}
	for tag := range expected {
		t.Errorf("missing tag %d from result", tag)
	}
}

func TestService_UpdateTags_NoProviderIsNoOp(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	row := sampleSeries(1, "X")
	row.Tags = []int{1, 2, 3}
	added, err := svc.AddSeries(t.Context(), row)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	changed, err := svc.UpdateTags(t.Context(), added)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}
	if changed {
		t.Errorf("expected changed=false when no provider wired")
	}
	if len(added.Tags) != 3 {
		t.Errorf("Tags mutated unexpectedly: %v", added.Tags)
	}
}

func TestService_UpdateTags_NoChange(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	provider := &fakeTagProvider{
		add:    map[int]struct{}{1: {}},  // already present
		remove: map[int]struct{}{99: {}}, // not present
	}
	svc.SetTagChangeProvider(provider)

	row := sampleSeries(1, "X")
	row.Tags = []int{1, 2}
	added, _ := svc.AddSeries(t.Context(), row)

	changed, err := svc.UpdateTags(t.Context(), added)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}
	if changed {
		t.Errorf("expected changed=false (no actual delta)")
	}
}

// UpdateSeries integrates UpdateTags: when the provider supplies a
// delta, the persisted Tags reflect it.
func TestService_UpdateSeries_AppliesAutoTaggingDelta(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	provider := &fakeTagProvider{add: map[int]struct{}{42: {}}}
	svc.SetTagChangeProvider(provider)

	row := sampleSeries(1, "X")
	added, err := svc.AddSeries(t.Context(), row)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	added.Title = "X2"
	updated, err := svc.UpdateSeries(t.Context(), added, series.UpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateSeries: %v", err)
	}
	found := false
	for _, tag := range updated.Tags {
		if tag == 42 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("auto-tagging delta not applied: Tags=%v", updated.Tags)
	}
}

// fakePathBuilder implements SeriesPathBuilder by appending a fixed
// suffix to the RootFolderPath. Lets tests verify the per-row rebuild
// fired without needing the real Organizer port.
type fakePathBuilder struct {
	useExistingFlagSeen bool
}

func (f *fakePathBuilder) BuildPath(_ context.Context, s *series.Series, useExistingRelativeFolder bool) (string, error) {
	f.useExistingFlagSeen = useExistingRelativeFolder
	return s.RootFolderPath + "/built-" + s.Title, nil
}

func TestService_UpdateMany_RebuildsPathsAndPublishesBulkEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	pb := &fakePathBuilder{}
	svc.SetSeriesPathBuilder(pb)

	a, _ := svc.AddSeries(t.Context(), sampleSeries(1, "A"))
	b, _ := svc.AddSeries(t.Context(), sampleSeries(2, "B"))
	a.RootFolderPath = "/tv"
	b.RootFolderPath = "/tv"
	a.Title = "A"
	b.Title = "B"

	var got atomic.Int32
	var lastEv atomic.Pointer[series.SeriesBulkEditedEvent]
	messaging.Subscribe(bus, "test.series.bulk-edited",
		func(_ context.Context, ev series.SeriesBulkEditedEvent) error {
			got.Add(1)
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	out, err := svc.UpdateMany(t.Context(), []*series.Series{a, b}, true)
	if err != nil {
		t.Fatalf("UpdateMany: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("UpdateMany: returned len=%d", len(out))
	}
	if a.Path != "/tv/built-A" {
		t.Errorf("a.Path=%q want /tv/built-A", a.Path)
	}
	if b.Path != "/tv/built-B" {
		t.Errorf("b.Path=%q want /tv/built-B", b.Path)
	}
	if !pb.useExistingFlagSeen {
		t.Errorf("useExistingRelativeFolder=true should propagate to builder")
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 bulk-edited event, got %d", got.Load())
	}
	ev := lastEv.Load()
	if ev == nil || len(ev.Series) != 2 {
		t.Errorf("event payload: %+v", ev)
	}
}

func TestService_UpdateMany_NoBuilderSkipsRebuild(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)

	a, _ := svc.AddSeries(t.Context(), sampleSeries(1, "A"))
	a.RootFolderPath = "/tv"
	originalPath := a.Path

	if _, err := svc.UpdateMany(t.Context(), []*series.Series{a}, false); err != nil {
		t.Fatalf("UpdateMany: %v", err)
	}
	if a.Path != originalPath {
		t.Errorf("Path mutated without builder: %q → %q", originalPath, a.Path)
	}
}

func TestService_UpdateMany_EmptyRootFolderSkipsRebuild(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	svc.SetSeriesPathBuilder(&fakePathBuilder{})

	a, _ := svc.AddSeries(t.Context(), sampleSeries(1, "A"))
	originalPath := a.Path
	// RootFolderPath is empty, so the builder shouldn't run.

	if _, err := svc.UpdateMany(t.Context(), []*series.Series{a}, false); err != nil {
		t.Fatalf("UpdateMany: %v", err)
	}
	if a.Path != originalPath {
		t.Errorf("empty RootFolderPath should skip rebuild: %q → %q", originalPath, a.Path)
	}
}

func TestService_UpdateMany_EmptyListNoOp(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	var fired atomic.Int32
	messaging.Subscribe(bus, "test.series.bulk-edited.empty",
		func(_ context.Context, _ series.SeriesBulkEditedEvent) error {
			fired.Add(1)
			return nil
		})

	out, err := svc.UpdateMany(t.Context(), nil, false)
	if err != nil {
		t.Errorf("UpdateMany(nil): %v", err)
	}
	if out != nil {
		t.Errorf("expected nil out, got %v", out)
	}
	if fired.Load() != 0 {
		t.Errorf("empty UpdateMany shouldn't publish; got %d events", fired.Load())
	}
}

func TestService_DeleteSeries_PublishesEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	a, err := svc.AddSeries(t.Context(), sampleSeries(1, "A"))
	if err != nil {
		t.Fatalf("Add A: %v", err)
	}
	b, err := svc.AddSeries(t.Context(), sampleSeries(2, "B"))
	if err != nil {
		t.Fatalf("Add B: %v", err)
	}

	var got atomic.Int32
	var lastEv atomic.Pointer[series.SeriesDeletedEvent]
	messaging.Subscribe(bus, "test.series.deleted",
		func(_ context.Context, ev series.SeriesDeletedEvent) error {
			got.Add(1)
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	if err := svc.DeleteSeries(t.Context(), []int{a.ID, b.ID, 99999}, true, false); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 SeriesDeletedEvent, got %d", got.Load())
	}
	ev := lastEv.Load()
	if ev == nil || len(ev.Series) != 2 {
		t.Fatalf("event payload: %+v", ev)
	}
	if !ev.DeleteFiles || ev.AddImportListExclusion {
		t.Errorf("flags lost: deleteFiles=%v addExclusion=%v", ev.DeleteFiles, ev.AddImportListExclusion)
	}
	// The rows are gone.
	if _, err := svc.GetSeries(t.Context(), a.ID); err == nil {
		t.Errorf("series A still present after delete")
	}
}

func TestService_DeleteSeries_EmptyListNoOp(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	var got atomic.Int32
	messaging.Subscribe(bus, "test.series.deleted.empty",
		func(_ context.Context, _ series.SeriesDeletedEvent) error {
			got.Add(1)
			return nil
		})
	if err := svc.DeleteSeries(t.Context(), nil, false, false); err != nil {
		t.Errorf("DeleteSeries(nil): %v", err)
	}
	if got.Load() != 0 {
		t.Errorf("empty DeleteSeries should not publish; got %d", got.Load())
	}
}

func TestService_AddMany_EmptyListNoOp(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	var fired atomic.Int32
	messaging.Subscribe(bus, "test.series.imported.empty",
		func(_ context.Context, _ series.SeriesImportedEvent) error {
			fired.Add(1)
			return nil
		})
	out, err := svc.AddMany(t.Context(), nil)
	if err != nil {
		t.Errorf("AddMany(nil): %v", err)
	}
	if out != nil {
		t.Errorf("expected nil out, got %v", out)
	}
	if fired.Load() != 0 {
		t.Errorf("empty AddMany should not publish; got %d events", fired.Load())
	}
}
