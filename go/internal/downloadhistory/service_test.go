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
	out []downloadhistory.Entry
	err error
}

func (s stubReader) AllByDownloadID(_ context.Context, _ string) ([]downloadhistory.Entry, error) {
	return s.out, s.err
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
	s := downloadhistory.NewService(stubReader{err: errors.New("EIO")})
	got, err := s.DownloadAlreadyImported(context.Background(), "abc")
	if err == nil || got {
		t.Errorf("got (%v, %v), want (false, error)", got, err)
	}
}

func TestDownloadAlreadyImportedEmptyReturnsFalse(t *testing.T) {
	t.Parallel()
	s := downloadhistory.NewService(stubReader{})
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
			s := downloadhistory.NewService(stubReader{out: tc.events})
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
	s := downloadhistory.NewService(stubReader{err: errors.New("EIO")})
	got, err := s.GetLatestDownloadHistoryItem(context.Background(), "abc")
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

func TestGetLatestDownloadHistoryItemEmptyReturnsNil(t *testing.T) {
	t.Parallel()
	s := downloadhistory.NewService(stubReader{})
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
			s := downloadhistory.NewService(stubReader{out: tc.events})
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
	s := downloadhistory.NewService(stubReader{err: errors.New("EIO")})
	got, err := s.GetLatestGrab(context.Background(), "abc")
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

func TestGetLatestGrabEmptyReturnsNil(t *testing.T) {
	t.Parallel()
	s := downloadhistory.NewService(stubReader{})
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
	s := downloadhistory.NewService(stubReader{out: events})
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
	s := downloadhistory.NewService(stubReader{out: events})
	got, err := s.GetLatestGrab(context.Background(), "abc")
	if err != nil || got != nil {
		t.Errorf("got (%+v, %v), want (nil, nil)", got, err)
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
}

func TestService_SQLite(t *testing.T) {
	t.Parallel()
	runServiceSuite(t, datastoretest.OpenSQLite(t))
}

func TestService_Postgres(t *testing.T) {
	runServiceSuite(t, datastoretest.OpenPostgres(t))
}
