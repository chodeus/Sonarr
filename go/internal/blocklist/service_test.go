package blocklist_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
)

// stubWriter captures the last Insert call + lets tests inject an
// error / a sentinel auto-increment id.
type stubWriter struct {
	gotInsert            *blocklist.Entry
	insertErr            error
	insertedID           int
	gotDeleteSeries      []int
	deleteSeriesAffected int64
	deleteSeriesErr      error
	gotDeleteID          int
	deleteErr            error
	gotDeleteByIDs       []int
	deleteByIDsAffected  int64
	deleteByIDsErr       error
}

func (s *stubWriter) Insert(_ context.Context, e *blocklist.Entry) (*blocklist.Entry, error) {
	s.gotInsert = e
	if s.insertErr != nil {
		return nil, s.insertErr
	}
	out := *e
	if s.insertedID > 0 {
		out.ID = s.insertedID
	} else {
		out.ID = 1
	}
	return &out, nil
}

func (s *stubWriter) DeleteForSeries(_ context.Context, ids []int) (int64, error) {
	s.gotDeleteSeries = append([]int(nil), ids...)
	if s.deleteSeriesErr != nil {
		return 0, s.deleteSeriesErr
	}
	return s.deleteSeriesAffected, nil
}

func (s *stubWriter) Delete(_ context.Context, id int) error {
	s.gotDeleteID = id
	return s.deleteErr
}

func (s *stubWriter) DeleteByIDs(_ context.Context, ids []int) (int64, error) {
	s.gotDeleteByIDs = append([]int(nil), ids...)
	if s.deleteByIDsErr != nil {
		return 0, s.deleteByIDsErr
	}
	return s.deleteByIDsAffected, nil
}

func validEntry() *blocklist.Entry {
	return &blocklist.Entry{
		SeriesID:    7,
		EpisodeIDs:  []int{11, 12},
		SourceTitle: "Show.S01E01.WEB-DL-GROUP",
		Quality:     `{}`,
		Date:        time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
		Languages:   []int{1},
		Source:      "Test",
	}
}

func TestNewServiceNilWriterReturnsNil(t *testing.T) {
	t.Parallel()
	if s := blocklist.NewService(nil, nil); s != nil {
		t.Errorf("NewService(nil) = %+v, want nil", s)
	}
}

func TestAddNilReceiverErrors(t *testing.T) {
	t.Parallel()
	var s *blocklist.Service
	if _, err := s.Add(context.Background(), validEntry()); err == nil {
		t.Errorf("nil receiver: want error")
	}
}

func TestAddNilEntryErrors(t *testing.T) {
	t.Parallel()
	s := blocklist.NewService(&stubWriter{}, nil)
	if _, err := s.Add(context.Background(), nil); err == nil {
		t.Errorf("nil entry: want error")
	}
}

func TestAddRequiresSeriesID(t *testing.T) {
	t.Parallel()
	s := blocklist.NewService(&stubWriter{}, nil)
	e := validEntry()
	e.SeriesID = 0
	if _, err := s.Add(context.Background(), e); err == nil {
		t.Errorf("SeriesID=0: want error")
	}
}

func TestAddRequiresSourceTitle(t *testing.T) {
	t.Parallel()
	s := blocklist.NewService(&stubWriter{}, nil)
	e := validEntry()
	e.SourceTitle = ""
	if _, err := s.Add(context.Background(), e); err == nil {
		t.Errorf("empty SourceTitle: want error")
	}
}

func TestAddRequiresDate(t *testing.T) {
	t.Parallel()
	s := blocklist.NewService(&stubWriter{}, nil)
	e := validEntry()
	e.Date = time.Time{}
	if _, err := s.Add(context.Background(), e); err == nil {
		t.Errorf("zero Date: want error")
	}
}

func TestAddAllowsEmptyEpisodeIDs(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s := blocklist.NewService(w, nil)
	e := validEntry()
	e.EpisodeIDs = nil
	if _, err := s.Add(context.Background(), e); err != nil {
		t.Errorf("empty EpisodeIDs should be allowed: %v", err)
	}
	if w.gotInsert == nil {
		t.Errorf("expected Insert to be called")
	}
}

func TestAddRepoErrorPropagates(t *testing.T) {
	t.Parallel()
	repoErr := errors.New("EIO")
	s := blocklist.NewService(&stubWriter{insertErr: repoErr}, nil)
	_, err := s.Add(context.Background(), validEntry())
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, repoErr) {
		t.Errorf("error chain missing repo err: %v", err)
	}
}

func TestAddSuccessReturnsRepoResult(t *testing.T) {
	t.Parallel()
	w := &stubWriter{insertedID: 42}
	s := blocklist.NewService(w, nil)
	e := validEntry()
	out, err := s.Add(context.Background(), e)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out == nil || out.ID != 42 {
		t.Errorf("returned entry ID = %v, want 42", out)
	}
	if out.SeriesID != e.SeriesID || out.SourceTitle != e.SourceTitle {
		t.Errorf("returned entry mismatched input: %+v", out)
	}
}

// --- Service.DeleteForSeries (slice 3.BB) ---------------------------

func TestDeleteForSeriesNilReceiverErrors(t *testing.T) {
	t.Parallel()
	var s *blocklist.Service
	if _, err := s.DeleteForSeries(context.Background(), []int{1}); err == nil {
		t.Errorf("nil receiver: want error")
	}
}

func TestDeleteForSeriesEmptyIDsIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s := blocklist.NewService(w, nil)
	n, err := s.DeleteForSeries(context.Background(), nil)
	if err != nil || n != 0 {
		t.Errorf("got (%d, %v); want (0, nil)", n, err)
	}
	if w.gotDeleteSeries != nil {
		t.Errorf("repo.DeleteForSeries should not have been called for empty input")
	}
}

func TestDeleteForSeriesPassesIDsThrough(t *testing.T) {
	t.Parallel()
	w := &stubWriter{deleteSeriesAffected: 11}
	s := blocklist.NewService(w, nil)
	n, err := s.DeleteForSeries(context.Background(), []int{4, 9})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 11 {
		t.Errorf("rows = %d, want 11", n)
	}
	if got := w.gotDeleteSeries; len(got) != 2 || got[0] != 4 || got[1] != 9 {
		t.Errorf("ids = %v, want [4 9]", got)
	}
}

func TestDeleteForSeriesRepoErrorPropagates(t *testing.T) {
	t.Parallel()
	repoErr := errors.New("EIO")
	s := blocklist.NewService(&stubWriter{deleteSeriesErr: repoErr}, nil)
	_, err := s.DeleteForSeries(context.Background(), []int{1})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, repoErr) {
		t.Errorf("error chain missing repo err: %v", err)
	}
}

// runServiceSuite exercises the Service against a real repo on the
// supplied DB engine. Pins the auto-increment ID propagation + the
// SQL boundary so a regression in the generic-repo Insert path is
// caught at slice time.
func runServiceSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := blocklist.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc := blocklist.NewService(repo, nil)
	if svc == nil {
		t.Fatal("NewService returned nil for non-nil repo")
	}

	e := validEntry()
	out, err := svc.Add(t.Context(), e)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if out == nil || out.ID == 0 {
		t.Errorf("Add returned %+v; want non-nil with ID>0", out)
	}

	// Round-trip: read the row back via the repo and confirm fields
	// match the input shape.
	got, err := repo.AllBySeriesID(t.Context(), e.SeriesID)
	if err != nil {
		t.Fatalf("AllBySeriesID: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row back, got %d", len(got))
	}
	if got[0].SourceTitle != e.SourceTitle || got[0].SeriesID != e.SeriesID {
		t.Errorf("round-trip mismatch: got %+v", got[0])
	}

	// DeleteForSeries removes every row whose SeriesId matches.
	deleted, err := svc.DeleteForSeries(t.Context(), []int{e.SeriesID})
	if err != nil {
		t.Fatalf("DeleteForSeries: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteForSeries rows = %d, want 1", deleted)
	}
	if got, err := repo.AllBySeriesID(t.Context(), e.SeriesID); err != nil || len(got) != 0 {
		t.Errorf("AllBySeriesID after DeleteForSeries: got (%v, %v); want empty", got, err)
	}
	// Re-deleting same series → no-op.
	if n, err := svc.DeleteForSeries(t.Context(), []int{e.SeriesID}); err != nil || n != 0 {
		t.Errorf("DeleteForSeries (re-run): got (%d, %v); want (0, nil)", n, err)
	}

	// nil-entry / missing-required validation arms are pure-Go
	// branches; the repo-backed suite only needs the happy path
	// + the SQL-layer round-trip. The unit tests above cover the
	// rest.
}

func TestService_SQLite(t *testing.T) {
	t.Parallel()
	runServiceSuite(t, datastoretest.OpenSQLite(t))
}

func TestService_Postgres(t *testing.T) {
	runServiceSuite(t, datastoretest.OpenPostgres(t))
}

// Slice 5.J28 (audit B.2 second wave). Delete paths publish
// BlocklistChangedEvent for the SignalR `blocklist` channel.
func TestService_DeletePublishesChangedEvent(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var got atomic.Int32
	var lastIDs atomic.Pointer[[]int]
	var lastCount atomic.Int64
	messaging.Subscribe(bus, "test.blocklist.changed",
		func(_ context.Context, ev blocklist.BlocklistChangedEvent) error {
			got.Add(1)
			ids := append([]int(nil), ev.IDs...)
			lastIDs.Store(&ids)
			lastCount.Store(ev.Count)
			return nil
		})

	w := &stubWriter{deleteByIDsAffected: 2}
	svc := blocklist.NewService(w, bus)

	// Delete single → one event with IDs=[7], Count=1.
	if err := svc.Delete(context.Background(), 7); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 event from Delete, got %d", got.Load())
	}
	if ids := lastIDs.Load(); ids == nil || len(*ids) != 1 || (*ids)[0] != 7 {
		t.Errorf("Delete event IDs: %+v, want [7]", ids)
	}
	if lastCount.Load() != 1 {
		t.Errorf("Delete event Count: %d, want 1", lastCount.Load())
	}

	// DeleteByIDs → one event with input IDs, Count = affected.
	if _, err := svc.DeleteByIDs(context.Background(), []int{4, 5}); err != nil {
		t.Fatalf("DeleteByIDs: %v", err)
	}
	if got.Load() != 2 {
		t.Errorf("expected 2 events after DeleteByIDs, got %d", got.Load())
	}
	if ids := lastIDs.Load(); ids == nil || len(*ids) != 2 {
		t.Errorf("DeleteByIDs event IDs: %+v, want [4 5]", ids)
	}
	if lastCount.Load() != 2 {
		t.Errorf("DeleteByIDs event Count: %d, want 2", lastCount.Load())
	}

	// DeleteByIDs with affected=0 → no event.
	w.deleteByIDsAffected = 0
	if _, err := svc.DeleteByIDs(context.Background(), []int{99}); err != nil {
		t.Fatalf("DeleteByIDs no-affected: %v", err)
	}
	if got.Load() != 2 {
		t.Errorf("no-affected DeleteByIDs should not publish; total=%d", got.Load())
	}
}

func TestService_DeleteForSeriesPublishesWhenAffected(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var got atomic.Int32
	var lastIDs atomic.Pointer[[]int]
	messaging.Subscribe(bus, "test.blocklist.delseries",
		func(_ context.Context, ev blocklist.BlocklistChangedEvent) error {
			got.Add(1)
			ids := append([]int(nil), ev.IDs...)
			lastIDs.Store(&ids)
			return nil
		})

	svc := blocklist.NewService(&stubWriter{deleteSeriesAffected: 5}, bus)
	if _, err := svc.DeleteForSeries(context.Background(), []int{1, 2}); err != nil {
		t.Fatalf("DeleteForSeries: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("DeleteForSeries: events=%d, want 1", got.Load())
	}
	if ids := lastIDs.Load(); ids == nil || len(*ids) != 0 {
		t.Errorf("DeleteForSeries event IDs: %+v, want empty (repo bulk WHERE doesn't return IDs)", ids)
	}
}
