package episodefiles_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// Compile-time bridges:
//   - *episodes.Service satisfies episodefiles.EpisodeProvider
//   - *series.Service   satisfies episodefiles.SeriesProvider
//
// Pinned so signature drift compile-fails loudly.
var (
	_ episodefiles.EpisodeProvider = (*episodes.Service)(nil)
	_ episodefiles.SeriesProvider  = (*series.Service)(nil)
)

func newSvcWithBus(t *testing.T) (*episodefiles.Service, *episodefiles.Repo, *messaging.Bus) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, err := episodefiles.NewService(r, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, r, bus
}

func sampleFile(seriesID, season int, rel string) *episodefiles.File {
	return &episodefiles.File{
		SeriesID:     seriesID,
		SeasonNumber: season,
		RelativePath: rel,
		Quality:      "{}",
		Size:         1024,
		DateAdded:    time.Now().UTC(),
		Languages:    []int{1},
	}
}

// fakeEpisodeProvider implements EpisodeProvider for tests without
// dragging in the full episodes service.
type fakeEpisodeProvider struct {
	out []episodes.Episode
	err error
}

func (f fakeEpisodeProvider) GetEpisodesByFileID(_ context.Context, _ int) ([]episodes.Episode, error) {
	return f.out, f.err
}

// fakeSeriesProvider implements SeriesProvider.
type fakeSeriesProvider struct {
	out *series.Series
	err error
}

func (f fakeSeriesProvider) GetSeries(_ context.Context, _ int) (*series.Series, error) {
	return f.out, f.err
}

func TestService_Add_PublishesAddedEvent(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)

	provider := fakeEpisodeProvider{
		out: []episodes.Episode{{ID: 11, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1}},
	}
	svc.SetEpisodeProvider(provider)

	var got atomic.Int32
	var lastEv atomic.Pointer[episodefiles.EpisodeFileAddedEvent]
	messaging.Subscribe(bus, "test.episodefiles.added",
		func(_ context.Context, ev episodefiles.EpisodeFileAddedEvent) error {
			got.Add(1)
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	added, err := svc.Add(t.Context(), sampleFile(1, 1, "ep1.mkv"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.ID == 0 {
		t.Errorf("Add: assigned ID=0")
	}
	if got.Load() != 1 {
		t.Fatalf("expected 1 EpisodeFileAddedEvent, got %d", got.Load())
	}
	ev := lastEv.Load()
	if ev == nil || ev.EpisodeFile == nil {
		t.Fatalf("nil event payload: %+v", ev)
	}
	if ev.EpisodeFile.ID != added.ID {
		t.Errorf("event file ID mismatch: ev=%d added=%d", ev.EpisodeFile.ID, added.ID)
	}
	if len(ev.LinkedEpisodes) != 1 || ev.LinkedEpisodes[0].ID != 11 {
		t.Errorf("linked episodes: %+v", ev.LinkedEpisodes)
	}
}

func TestService_Add_NoProviderLeavesLinkedNil(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)

	var lastEv atomic.Pointer[episodefiles.EpisodeFileAddedEvent]
	messaging.Subscribe(bus, "test.episodefiles.added.noprov",
		func(_ context.Context, ev episodefiles.EpisodeFileAddedEvent) error {
			cp := ev
			lastEv.Store(&cp)
			return nil
		})
	if _, err := svc.Add(t.Context(), sampleFile(1, 1, "ep1.mkv")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	ev := lastEv.Load()
	if ev == nil {
		t.Fatalf("no event")
	}
	if ev.LinkedEpisodes != nil {
		t.Errorf("LinkedEpisodes: got %v, want nil", ev.LinkedEpisodes)
	}
}

func TestService_Delete_PublishesDeletedEvent(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)

	svc.SetEpisodeProvider(fakeEpisodeProvider{
		out: []episodes.Episode{{ID: 22, SeriesID: 1, SeasonNumber: 2, EpisodeNumber: 5}},
	})
	parent := &series.Series{ID: 1, Title: "X", Path: "/data/x"}
	svc.SetSeriesProvider(fakeSeriesProvider{out: parent})

	added, err := svc.Add(t.Context(), sampleFile(1, 2, "ep5.mkv"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	var lastDel atomic.Pointer[episodefiles.EpisodeFileDeletedEvent]
	messaging.Subscribe(bus, "test.episodefiles.deleted",
		func(_ context.Context, ev episodefiles.EpisodeFileDeletedEvent) error {
			cp := ev
			lastDel.Store(&cp)
			return nil
		})

	if err := svc.Delete(t.Context(), added, episodefiles.ReasonManual); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	ev := lastDel.Load()
	if ev == nil || ev.EpisodeFile == nil {
		t.Fatalf("missing delete event: %+v", ev)
	}
	if ev.Reason != episodefiles.ReasonManual {
		t.Errorf("reason: got %v, want %v", ev.Reason, episodefiles.ReasonManual)
	}
	if ev.Series == nil || ev.Series.Title != "X" {
		t.Errorf("series: %+v", ev.Series)
	}
	if len(ev.LinkedEpisodes) != 1 || ev.LinkedEpisodes[0].ID != 22 {
		t.Errorf("linked: %+v", ev.LinkedEpisodes)
	}

	// Row is gone.
	if _, err := svc.GetFile(t.Context(), added.ID); !errors.Is(err, episodefiles.ErrNotFound) {
		t.Errorf("Get after delete: %v", err)
	}
}

func TestService_NilBusSkipsPublish(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	r, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := episodefiles.NewService(r, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	added, err := svc.Add(t.Context(), sampleFile(1, 1, "x.mkv"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := svc.Delete(t.Context(), added, episodefiles.ReasonManual); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestService_Update_PublishesUpdatedEvent(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)
	parent := &series.Series{ID: 1, Title: "Show A", Path: "/data/a"}
	svc.SetSeriesProvider(fakeSeriesProvider{out: parent})

	added, err := svc.Add(t.Context(), sampleFile(1, 1, "ep1.mkv"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	var got atomic.Int32
	var lastEv atomic.Pointer[episodefiles.EpisodeFileUpdatedEvent]
	messaging.Subscribe(bus, "test.episodefiles.updated",
		func(_ context.Context, ev episodefiles.EpisodeFileUpdatedEvent) error {
			got.Add(1)
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	added.SceneName = "Show.A.S01E01.WEBDL"
	if err := svc.Update(t.Context(), added); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Load() != 1 {
		t.Fatalf("expected 1 EpisodeFileUpdatedEvent, got %d", got.Load())
	}
	ev := lastEv.Load()
	if ev == nil || ev.EpisodeFile == nil {
		t.Fatalf("nil event payload: %+v", ev)
	}
	if ev.EpisodeFile.ID != added.ID {
		t.Errorf("event file ID mismatch: ev=%d added=%d", ev.EpisodeFile.ID, added.ID)
	}
	if ev.EpisodeFile.SceneName != "Show.A.S01E01.WEBDL" {
		t.Errorf("event scene name: %q", ev.EpisodeFile.SceneName)
	}
	if ev.Series == nil || ev.Series.Path != "/data/a" {
		t.Errorf("series resolution: %+v (want Path=/data/a)", ev.Series)
	}
}

func TestService_UpdateMany_PublishesPerRow(t *testing.T) {
	t.Parallel()
	svc, _, bus := newSvcWithBus(t)

	a, err := svc.Add(t.Context(), sampleFile(1, 1, "a.mkv"))
	if err != nil {
		t.Fatalf("Add a: %v", err)
	}
	b, err := svc.Add(t.Context(), sampleFile(1, 1, "b.mkv"))
	if err != nil {
		t.Fatalf("Add b: %v", err)
	}

	var count atomic.Int32
	messaging.Subscribe(bus, "test.episodefiles.updated.bulk",
		func(_ context.Context, _ episodefiles.EpisodeFileUpdatedEvent) error {
			count.Add(1)
			return nil
		})

	a.SceneName = "A"
	b.SceneName = "B"
	if err := svc.UpdateMany(t.Context(), []*episodefiles.File{a, b}); err != nil {
		t.Fatalf("UpdateMany: %v", err)
	}
	if count.Load() != 2 {
		t.Errorf("UpdateMany publishes per row: got %d, want 2", count.Load())
	}
}

func TestService_Update_NilBusSkipsPublish(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	r, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := episodefiles.NewService(r, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	added, err := svc.Add(t.Context(), sampleFile(1, 1, "x.mkv"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	added.SceneName = "Y"
	if err := svc.Update(t.Context(), added); err != nil {
		t.Fatalf("Update with nil bus must not error: %v", err)
	}
}

func TestService_FilterExistingFiles(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)
	parent := &series.Series{ID: 1, Path: "/data/x"}

	if _, err := svc.Add(t.Context(), sampleFile(1, 1, "ep1.mkv")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := svc.Add(t.Context(), sampleFile(1, 1, "subdir/ep2.mkv")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	candidates := []string{
		"/data/x/ep1.mkv",        // existing
		"/data/x/subdir/ep2.mkv", // existing
		"/data/x/ep3.mkv",        // new
		"/data/x/subdir/ep4.mkv", // new
	}
	out, err := svc.FilterExistingFiles(t.Context(), candidates, parent)
	if err != nil {
		t.Fatalf("FilterExistingFiles: %v", err)
	}
	if len(out) != 2 || out[0] != "/data/x/ep3.mkv" || out[1] != "/data/x/subdir/ep4.mkv" {
		t.Errorf("FilterExistingFiles: got %v, want [ep3, subdir/ep4]", out)
	}

	// Nil series → error.
	if _, err := svc.FilterExistingFiles(t.Context(), candidates, nil); err == nil {
		t.Errorf("FilterExistingFiles(nil): want error")
	}

	// Empty existing-files set → returns input unchanged.
	emptyParent := &series.Series{ID: 99, Path: "/empty"}
	out, err = svc.FilterExistingFiles(t.Context(), candidates, emptyParent)
	if err != nil {
		t.Fatalf("FilterExistingFiles empty: %v", err)
	}
	if len(out) != len(candidates) {
		t.Errorf("FilterExistingFiles empty: got %d, want %d", len(out), len(candidates))
	}
}

func TestService_HandleSeriesDeleted(t *testing.T) {
	t.Parallel()
	svc, r, _ := newSvcWithBus(t)

	if _, err := svc.Add(t.Context(), sampleFile(1, 1, "a.mkv")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := svc.Add(t.Context(), sampleFile(2, 1, "b.mkv")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ev := series.SeriesDeletedEvent{Series: []series.Series{{ID: 1}}}
	if err := svc.HandleSeriesDeleted(t.Context(), ev); err != nil {
		t.Fatalf("HandleSeriesDeleted: %v", err)
	}
	rem, err := r.AllBySeriesIDs(t.Context(), []int{1, 2})
	if err != nil {
		t.Fatalf("AllBySeriesIDs: %v", err)
	}
	if len(rem) != 1 || rem[0].SeriesID != 2 {
		t.Errorf("after delete: %+v", rem)
	}

	// Empty event is a no-op.
	if err := svc.HandleSeriesDeleted(t.Context(), series.SeriesDeletedEvent{}); err != nil {
		t.Errorf("HandleSeriesDeleted empty: %v", err)
	}
}

func TestService_GettersAndUpdate(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBus(t)

	a, _ := svc.Add(t.Context(), sampleFile(1, 1, "a.mkv"))
	b, _ := svc.Add(t.Context(), sampleFile(1, 2, "b.mkv"))
	if _, err := svc.Add(t.Context(), sampleFile(2, 1, "c.mkv")); err != nil {
		t.Fatalf("Add c: %v", err)
	}

	if got, err := svc.GetFile(t.Context(), a.ID); err != nil || got.RelativePath != "a.mkv" {
		t.Errorf("GetFile: %v / %+v", err, got)
	}
	if got, err := svc.GetFiles(t.Context(), []int{a.ID, b.ID}); err != nil || len(got) != 2 {
		t.Errorf("GetFiles: len=%d err=%v", len(got), err)
	}
	if got, err := svc.GetFilesBySeries(t.Context(), 1); err != nil || len(got) != 2 {
		t.Errorf("GetFilesBySeries: len=%d err=%v", len(got), err)
	}
	if got, err := svc.GetFilesBySeriesIDs(t.Context(), []int{1, 2}); err != nil || len(got) != 3 {
		t.Errorf("GetFilesBySeriesIDs: len=%d err=%v", len(got), err)
	}
	if got, err := svc.GetFilesBySeason(t.Context(), 1, 1); err != nil || len(got) != 1 {
		t.Errorf("GetFilesBySeason: len=%d err=%v", len(got), err)
	}
	if got, err := svc.GetFilesWithoutMediaInfo(t.Context()); err != nil || len(got) != 3 {
		t.Errorf("GetFilesWithoutMediaInfo: len=%d err=%v", len(got), err)
	}
	if got, err := svc.GetFilesWithRelativePath(t.Context(), 1, "a.mkv"); err != nil || len(got) != 1 {
		t.Errorf("GetFilesWithRelativePath: len=%d err=%v", len(got), err)
	}

	// Update + UpdateMany.
	a.SceneName = "renamed"
	if err := svc.Update(t.Context(), a); err != nil {
		t.Fatalf("Update: %v", err)
	}
	b.SceneName = "renamed-too"
	if err := svc.UpdateMany(t.Context(), []*episodefiles.File{a, b}); err != nil {
		t.Fatalf("UpdateMany: %v", err)
	}
	if got, err := svc.GetFile(t.Context(), a.ID); err != nil || got.SceneName != "renamed" {
		t.Errorf("after Update: %+v", got)
	}
}

func TestService_NewServiceErrors(t *testing.T) {
	t.Parallel()
	if _, err := episodefiles.NewService(nil, nil); err == nil {
		t.Errorf("NewService(nil) want error")
	}
}

func TestService_DeleteBySeriesID(t *testing.T) {
	t.Parallel()
	svc, r, _ := newSvcWithBus(t)

	// Insert 3 files for series 50 + 1 for series 51.
	for _, f := range []*episodefiles.File{
		sampleFile(50, 1, "S01E01.mkv"),
		sampleFile(50, 1, "S01E02.mkv"),
		sampleFile(50, 2, "S02E01.mkv"),
		sampleFile(51, 1, "Other.mkv"),
	} {
		if _, err := r.Insert(t.Context(), f); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	deleted, err := svc.DeleteBySeriesID(t.Context(), 50)
	if err != nil {
		t.Fatalf("DeleteBySeriesID: %v", err)
	}
	if deleted != 3 {
		t.Errorf("rows: got %d, want 3", deleted)
	}

	// Series 50 has zero files; series 51 still has 1.
	left, _ := r.AllBySeriesID(t.Context(), 50)
	if len(left) != 0 {
		t.Errorf("series 50: %d, want 0", len(left))
	}
	left51, _ := r.AllBySeriesID(t.Context(), 51)
	if len(left51) != 1 {
		t.Errorf("series 51: %d, want 1", len(left51))
	}

	// Idempotent.
	deleted2, err := svc.DeleteBySeriesID(t.Context(), 50)
	if err != nil {
		t.Errorf("DeleteBySeriesID idempotent: %v", err)
	}
	if deleted2 != 0 {
		t.Errorf("idempotent rows: got %d, want 0", deleted2)
	}
}
