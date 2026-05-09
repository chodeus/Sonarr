package blocklist_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
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
	if s := blocklist.NewService(nil); s != nil {
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
	s := blocklist.NewService(&stubWriter{})
	if _, err := s.Add(context.Background(), nil); err == nil {
		t.Errorf("nil entry: want error")
	}
}

func TestAddRequiresSeriesID(t *testing.T) {
	t.Parallel()
	s := blocklist.NewService(&stubWriter{})
	e := validEntry()
	e.SeriesID = 0
	if _, err := s.Add(context.Background(), e); err == nil {
		t.Errorf("SeriesID=0: want error")
	}
}

func TestAddRequiresSourceTitle(t *testing.T) {
	t.Parallel()
	s := blocklist.NewService(&stubWriter{})
	e := validEntry()
	e.SourceTitle = ""
	if _, err := s.Add(context.Background(), e); err == nil {
		t.Errorf("empty SourceTitle: want error")
	}
}

func TestAddRequiresDate(t *testing.T) {
	t.Parallel()
	s := blocklist.NewService(&stubWriter{})
	e := validEntry()
	e.Date = time.Time{}
	if _, err := s.Add(context.Background(), e); err == nil {
		t.Errorf("zero Date: want error")
	}
}

func TestAddAllowsEmptyEpisodeIDs(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s := blocklist.NewService(w)
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
	s := blocklist.NewService(&stubWriter{insertErr: repoErr})
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
	s := blocklist.NewService(w)
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
	s := blocklist.NewService(w)
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
	s := blocklist.NewService(w)
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
	s := blocklist.NewService(&stubWriter{deleteSeriesErr: repoErr})
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
	svc := blocklist.NewService(repo)
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
