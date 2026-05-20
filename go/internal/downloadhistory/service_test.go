package downloadhistory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/downloadhistory"
)

// stubReader is a deterministic [downloadhistory.EntryReader] for
// service-level tests that don't need a real DB. The repo-backed
// tests use the real engines via runServiceSuite below.
type stubReader struct {
	out             []downloadhistory.Entry
	err             error
	gotDeleteSeries []int
	deleteCount     int64
	deleteErr       error
	gotInsert       *downloadhistory.Entry
	insertErr       error
	insertedID      int
}

func (s *stubReader) AllByDownloadID(_ context.Context, _ string) ([]downloadhistory.Entry, error) {
	return s.out, s.err
}

func (s *stubReader) DeleteBySeriesIDs(_ context.Context, ids []int) (int64, error) {
	if s.deleteErr != nil {
		return 0, s.deleteErr
	}
	s.gotDeleteSeries = append([]int(nil), ids...)
	return s.deleteCount, nil
}

func (s *stubReader) Insert(_ context.Context, e *downloadhistory.Entry) (*downloadhistory.Entry, error) {
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

func TestNewServiceNilRepoReturnsNil(t *testing.T) {
	t.Parallel()
	if s := downloadhistory.NewService(nil); s != nil {
		t.Errorf("NewService(nil) = %+v, want nil", s)
	}
}

func TestEventTypeStringMatchesCSharpNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   downloadhistory.EventType
		want string
	}{
		{downloadhistory.EventTypeGrabbed, "DownloadGrabbed"},
		{downloadhistory.EventTypeImported, "DownloadImported"},
		{downloadhistory.EventTypeFailed, "DownloadFailed"},
		{downloadhistory.EventTypeIgnored, "DownloadIgnored"},
		{downloadhistory.EventTypeFileImported, "FileImported"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("EventType(%d).String() = %q, want %q", int(tc.in), got, tc.want)
		}
	}
	if got := downloadhistory.EventType(99).String(); got != "EventType(99)" {
		t.Errorf("out-of-range String = %q, want EventType(99)", got)
	}
}

// --- DownloadAlreadyImported -----------------------------------------

func TestDownloadAlreadyImportedRepoErrPropagates(t *testing.T) {
	t.Parallel()
	s := downloadhistory.NewService(&stubReader{err: errors.New("EIO")})
	got, err := s.DownloadAlreadyImported(context.Background(), "abc")
	if err == nil || got {
		t.Errorf("got (%v, %v), want (false, error)", got, err)
	}
}

func TestDownloadAlreadyImportedEmptyReturnsFalse(t *testing.T) {
	t.Parallel()
	s := downloadhistory.NewService(&stubReader{})
	got, err := s.DownloadAlreadyImported(context.Background(), "abc")
	if err != nil || got {
		t.Errorf("empty: got (%v, %v), want (false, nil)", got, err)
	}
}

func TestDownloadAlreadyImportedTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		events []downloadhistory.Entry
		want   bool
	}{
		{
			name: "stable-imported (Imported is the only event)",
			events: []downloadhistory.Entry{
				{EventType: int(downloadhistory.EventTypeImported)},
			},
			want: true,
		},
		{
			name: "re-grabbed (Grabbed newer than Imported, date-DESC list puts Grabbed first)",
			events: []downloadhistory.Entry{
				{EventType: int(downloadhistory.EventTypeGrabbed)},
				{EventType: int(downloadhistory.EventTypeImported)},
			},
			want: false,
		},
		{
			name: "imported-after-grab (Imported newer, returned first)",
			events: []downloadhistory.Entry{
				{EventType: int(downloadhistory.EventTypeImported)},
				{EventType: int(downloadhistory.EventTypeGrabbed)},
			},
			want: true,
		},
		{
			name: "Failed-skipped (Failed before the relevant Imported event must not short-circuit)",
			events: []downloadhistory.Entry{
				{EventType: int(downloadhistory.EventTypeFailed)},
				{EventType: int(downloadhistory.EventTypeImported)},
			},
			want: true,
		},
		{
			name: "FileImported-skipped (FileImported is not the summary; service walks past it)",
			events: []downloadhistory.Entry{
				{EventType: int(downloadhistory.EventTypeFileImported)},
				{EventType: int(downloadhistory.EventTypeFileImported)},
				{EventType: int(downloadhistory.EventTypeGrabbed)},
			},
			want: false,
		},
		{
			name: "only-grab returns false",
			events: []downloadhistory.Entry{
				{EventType: int(downloadhistory.EventTypeGrabbed)},
			},
			want: false,
		},
		{
			name: "only-failed returns false (no Imported, no Grabbed)",
			events: []downloadhistory.Entry{
				{EventType: int(downloadhistory.EventTypeFailed)},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := downloadhistory.NewService(&stubReader{out: tc.events})
			got, err := s.DownloadAlreadyImported(context.Background(), "abc")
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// --- GetLatestDownloadHistoryItem ------------------------------------

func TestGetLatestDownloadHistoryItemRepoErrPropagates(t *testing.T) {
	t.Parallel()
	s := downloadhistory.NewService(&stubReader{err: errors.New("EIO")})
	got, err := s.GetLatestDownloadHistoryItem(context.Background(), "abc")
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

func TestGetLatestDownloadHistoryItemEmptyReturnsNil(t *testing.T) {
	t.Parallel()
	s := downloadhistory.NewService(&stubReader{})
	got, err := s.GetLatestDownloadHistoryItem(context.Background(), "abc")
	if err != nil || got != nil {
		t.Errorf("empty: got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestGetLatestDownloadHistoryItemTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		events  []downloadhistory.Entry
		want    downloadhistory.EventType
		wantNil bool
	}{
		{
			name: "Grabbed first wins",
			events: []downloadhistory.Entry{
				{ID: 1, EventType: int(downloadhistory.EventTypeGrabbed)},
				{ID: 2, EventType: int(downloadhistory.EventTypeImported)},
			},
			want: downloadhistory.EventTypeGrabbed,
		},
		{
			name: "Ignored first wins",
			events: []downloadhistory.Entry{
				{ID: 1, EventType: int(downloadhistory.EventTypeIgnored)},
				{ID: 2, EventType: int(downloadhistory.EventTypeGrabbed)},
			},
			want: downloadhistory.EventTypeIgnored,
		},
		{
			name: "Imported first wins",
			events: []downloadhistory.Entry{
				{ID: 1, EventType: int(downloadhistory.EventTypeImported)},
				{ID: 2, EventType: int(downloadhistory.EventTypeGrabbed)},
			},
			want: downloadhistory.EventTypeImported,
		},
		{
			name: "Failed first wins",
			events: []downloadhistory.Entry{
				{ID: 1, EventType: int(downloadhistory.EventTypeFailed)},
				{ID: 2, EventType: int(downloadhistory.EventTypeGrabbed)},
			},
			want: downloadhistory.EventTypeFailed,
		},
		{
			name: "FileImported skipped — returns later Grabbed",
			events: []downloadhistory.Entry{
				{ID: 1, EventType: int(downloadhistory.EventTypeFileImported)},
				{ID: 2, EventType: int(downloadhistory.EventTypeGrabbed)},
			},
			want: downloadhistory.EventTypeGrabbed,
		},
		{
			name: "all FileImported returns nil",
			events: []downloadhistory.Entry{
				{EventType: int(downloadhistory.EventTypeFileImported)},
				{EventType: int(downloadhistory.EventTypeFileImported)},
			},
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := downloadhistory.NewService(&stubReader{out: tc.events})
			got, err := s.GetLatestDownloadHistoryItem(context.Background(), "abc")
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if tc.wantNil {
				if got != nil {
					t.Errorf("got %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil, want EventType=%v", tc.want)
			}
			if downloadhistory.EventType(got.EventType) != tc.want {
				t.Errorf("got EventType=%v, want %v", downloadhistory.EventType(got.EventType), tc.want)
			}
		})
	}
}

// --- GetLatestGrab ---------------------------------------------------

func TestGetLatestGrabRepoErrPropagates(t *testing.T) {
	t.Parallel()
	s := downloadhistory.NewService(&stubReader{err: errors.New("EIO")})
	got, err := s.GetLatestGrab(context.Background(), "abc")
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

func TestGetLatestGrabEmptyReturnsNil(t *testing.T) {
	t.Parallel()
	s := downloadhistory.NewService(&stubReader{})
	got, err := s.GetLatestGrab(context.Background(), "abc")
	if err != nil || got != nil {
		t.Errorf("empty: got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestGetLatestGrabSkipsNonGrabEvents(t *testing.T) {
	t.Parallel()

	events := []downloadhistory.Entry{
		{ID: 1, EventType: int(downloadhistory.EventTypeImported)},
		{ID: 2, EventType: int(downloadhistory.EventTypeFailed)},
		{ID: 3, EventType: int(downloadhistory.EventTypeGrabbed), SourceTitle: "wanted"},
		{ID: 4, EventType: int(downloadhistory.EventTypeGrabbed), SourceTitle: "older"},
	}
	s := downloadhistory.NewService(&stubReader{out: events})
	got, err := s.GetLatestGrab(context.Background(), "abc")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 3 || got.SourceTitle != "wanted" {
		t.Errorf("got %+v, want ID=3 SourceTitle=wanted", got)
	}
}

func TestGetLatestGrabNoGrabReturnsNil(t *testing.T) {
	t.Parallel()
	events := []downloadhistory.Entry{
		{EventType: int(downloadhistory.EventTypeImported)},
		{EventType: int(downloadhistory.EventTypeFailed)},
	}
	s := downloadhistory.NewService(&stubReader{out: events})
	got, err := s.GetLatestGrab(context.Background(), "abc")
	if err != nil || got != nil {
		t.Errorf("got (%+v, %v), want (nil, nil)", got, err)
	}
}

// --- Service.DeleteForSeries (slice 5.J135) -------------------------

func TestDeleteForSeries_NilReceiverErrors(t *testing.T) {
	t.Parallel()
	var s *downloadhistory.Service
	if _, err := s.DeleteForSeries(context.Background(), []int{1}); err == nil {
		t.Errorf("nil receiver: want error")
	}
}

func TestDeleteForSeries_EmptyIDsIsNoOp(t *testing.T) {
	t.Parallel()
	r := &stubReader{}
	s := downloadhistory.NewService(r)
	n, err := s.DeleteForSeries(context.Background(), nil)
	if err != nil {
		t.Errorf("err: %v", err)
	}
	if n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
	if r.gotDeleteSeries != nil {
		t.Errorf("repo.DeleteBySeriesIDs should not be called for empty input; got %v", r.gotDeleteSeries)
	}
}

func TestDeleteForSeries_PassesIDsThrough(t *testing.T) {
	t.Parallel()
	r := &stubReader{deleteCount: 5}
	s := downloadhistory.NewService(r)
	n, err := s.DeleteForSeries(context.Background(), []int{4, 9, 16})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 5 {
		t.Errorf("rows = %d, want 5 (stub-pinned)", n)
	}
	if got := r.gotDeleteSeries; len(got) != 3 || got[0] != 4 || got[1] != 9 || got[2] != 16 {
		t.Errorf("repo.DeleteBySeriesIDs got %v, want [4 9 16]", got)
	}
}

func TestDeleteForSeries_RepoErrorPropagates(t *testing.T) {
	t.Parallel()
	repoErr := errors.New("EIO")
	s := downloadhistory.NewService(&stubReader{deleteErr: repoErr})
	_, err := s.DeleteForSeries(context.Background(), []int{1})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, repoErr) {
		t.Errorf("error chain missing repo err: %v", err)
	}
}

// --- repo-backed integration suite -----------------------------------

// runServiceSuite exercises the Service against a real repo on the
// supplied DB engine. Verifies the date-DESC ordering the repo
// promises is what the service depends on, and pins the SQL-layer
// integration so a regression in the repo's ORDER BY (or the row
// scan path) gets caught here rather than at production-wiring time.
func runServiceSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := downloadhistory.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc := downloadhistory.NewService(repo)

	now := time.Date(2026, 5, 7, 14, 30, 0, 0, time.UTC)

	// Insert order is: oldest-grab, mid-failed, newest-imported.
	// The repo's date-DESC ordering means the service walks
	// imported → failed → grabbed.
	type seed struct {
		t  downloadhistory.EventType
		at time.Time
	}
	for _, sd := range []seed{
		{downloadhistory.EventTypeGrabbed, now.Add(-2 * time.Hour)},
		{downloadhistory.EventTypeFailed, now.Add(-time.Hour)},
		{downloadhistory.EventTypeImported, now},
	} {
		e := &downloadhistory.Entry{
			EventType:   int(sd.t),
			SeriesID:    50,
			DownloadID:  "test-grab-svc",
			SourceTitle: "Foo.S01E01",
			Date:        sd.at,
		}
		if _, err := repo.Insert(t.Context(), e); err != nil {
			t.Fatalf("Insert %v: %v", sd.t, err)
		}
	}

	// DownloadAlreadyImported: top of the list is Imported → true
	if got, err := svc.DownloadAlreadyImported(t.Context(), "test-grab-svc"); err != nil || !got {
		t.Errorf("DownloadAlreadyImported: got (%v, %v), want (true, nil)", got, err)
	}

	// GetLatestDownloadHistoryItem: walks past nothing — Imported is
	// the first match.
	got, err := svc.GetLatestDownloadHistoryItem(t.Context(), "test-grab-svc")
	if err != nil {
		t.Fatalf("GetLatestDownloadHistoryItem: %v", err)
	}
	if got == nil || downloadhistory.EventType(got.EventType) != downloadhistory.EventTypeImported {
		t.Errorf("GetLatestDownloadHistoryItem: got %+v, want Imported", got)
	}

	// GetLatestGrab: must walk past Imported + Failed to find the
	// Grabbed entry buried at the back of the date-DESC list.
	got, err = svc.GetLatestGrab(t.Context(), "test-grab-svc")
	if err != nil {
		t.Fatalf("GetLatestGrab: %v", err)
	}
	if got == nil || downloadhistory.EventType(got.EventType) != downloadhistory.EventTypeGrabbed {
		t.Errorf("GetLatestGrab: got %+v, want Grabbed", got)
	}

	// Unknown DownloadID — empty everywhere.
	if got, err := svc.DownloadAlreadyImported(t.Context(), "missing"); err != nil || got {
		t.Errorf("DownloadAlreadyImported(missing): got (%v, %v), want (false, nil)", got, err)
	}
	if got, err := svc.GetLatestDownloadHistoryItem(t.Context(), "missing"); err != nil || got != nil {
		t.Errorf("GetLatestDownloadHistoryItem(missing): got (%+v, %v)", got, err)
	}
	if got, err := svc.GetLatestGrab(t.Context(), "missing"); err != nil || got != nil {
		t.Errorf("GetLatestGrab(missing): got (%+v, %v)", got, err)
	}

	// DeleteForSeries — bulk-removes the 3 rows seeded above (all
	// SeriesID=50). Repo-level integration: verifies the IN-list SQL
	// shape works on the live engine and the rows-affected count
	// matches what we seeded.
	n, err := svc.DeleteForSeries(t.Context(), []int{50})
	if err != nil {
		t.Fatalf("DeleteForSeries: %v", err)
	}
	if n != 3 {
		t.Errorf("DeleteForSeries rows = %d, want 3", n)
	}
	// Post-delete: lookups for the same downloadID return empty.
	if rows, err := repo.AllByDownloadID(t.Context(), "test-grab-svc"); err != nil || len(rows) != 0 {
		t.Errorf("AllByDownloadID after DeleteForSeries: got (%v, %v); want empty", rows, err)
	}
	// Re-deleting the same series is a no-op (no rows match).
	if n, err := svc.DeleteForSeries(t.Context(), []int{50}); err != nil || n != 0 {
		t.Errorf("DeleteForSeries(re-run): got (%d, %v); want (0, nil)", n, err)
	}
	// Deleting an unknown series id returns 0 with no error.
	if n, err := svc.DeleteForSeries(t.Context(), []int{999}); err != nil || n != 0 {
		t.Errorf("DeleteForSeries(unknown): got (%d, %v); want (0, nil)", n, err)
	}
}

func TestService_SQLite(t *testing.T) {
	t.Parallel()
	runServiceSuite(t, datastoretest.OpenSQLite(t))
}

func TestService_Postgres(t *testing.T) {
	runServiceSuite(t, datastoretest.OpenPostgres(t))
}
