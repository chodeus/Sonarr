package history_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/history"
)

// stubReader is a deterministic [history.EntryReader] for service-
// level tests that don't need a real DB. The repo-backed integration
// suite below covers the SQL-to-Go path.
type stubReader struct {
	out []history.Entry
	err error
}

func (s stubReader) AllByDownloadID(_ context.Context, _ string) ([]history.Entry, error) {
	return s.out, s.err
}

func TestNewServiceNilRepoReturnsNil(t *testing.T) {
	t.Parallel()
	if s := history.NewService(nil); s != nil {
		t.Errorf("NewService(nil) = %+v, want nil", s)
	}
}

func TestEventTypeStringMatchesCSharpNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   history.EventType
		want string
	}{
		{history.EventTypeUnknown, "Unknown"},
		{history.EventTypeGrabbed, "Grabbed"},
		{history.EventTypeSeriesFolderImported, "SeriesFolderImported"},
		{history.EventTypeDownloadFolderImported, "DownloadFolderImported"},
		{history.EventTypeDownloadFailed, "DownloadFailed"},
		{history.EventTypeEpisodeFileDeleted, "EpisodeFileDeleted"},
		{history.EventTypeEpisodeFileRenamed, "EpisodeFileRenamed"},
		{history.EventTypeDownloadIgnored, "DownloadIgnored"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("EventType(%d).String() = %q, want %q", int(tc.in), got, tc.want)
		}
	}
	if got := history.EventType(99).String(); got != "EventType(99)" {
		t.Errorf("out-of-range String = %q, want EventType(99)", got)
	}
}

// --- FindByDownloadID -----------------------------------------------

func TestFindByDownloadIDRepoErrPropagates(t *testing.T) {
	t.Parallel()
	s := history.NewService(stubReader{err: errors.New("EIO")})
	got, err := s.FindByDownloadID(context.Background(), "abc")
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

func TestFindByDownloadIDPassesThroughOrderedRows(t *testing.T) {
	t.Parallel()

	g := func(t history.EventType) *int { v := int(t); return &v }
	rows := []history.Entry{
		{ID: 1, DownloadID: "abc", EventType: g(history.EventTypeDownloadFolderImported)},
		{ID: 2, DownloadID: "abc", EventType: g(history.EventTypeGrabbed)},
		{ID: 3, DownloadID: "abc", EventType: nil},
	}
	s := history.NewService(stubReader{out: rows})

	got, err := s.FindByDownloadID(context.Background(), "abc")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	for i := range got {
		if got[i].ID != rows[i].ID {
			t.Errorf("[%d] ID=%d, want %d (caller relies on date-DESC pass-through)", i, got[i].ID, rows[i].ID)
		}
	}
}

func TestFindByDownloadIDEmptyReturnsEmpty(t *testing.T) {
	t.Parallel()
	s := history.NewService(stubReader{})
	got, err := s.FindByDownloadID(context.Background(), "missing")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d rows, want 0", len(got))
	}
}

// --- Find (filter by event type) -----------------------------------

func TestFindRepoErrPropagates(t *testing.T) {
	t.Parallel()
	s := history.NewService(stubReader{err: errors.New("EIO")})
	got, err := s.Find(context.Background(), "abc", history.EventTypeGrabbed)
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

func TestFindEmptyReturnsNil(t *testing.T) {
	t.Parallel()
	s := history.NewService(stubReader{})
	got, err := s.Find(context.Background(), "missing", history.EventTypeGrabbed)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestFindFiltersByEventType(t *testing.T) {
	t.Parallel()

	g := func(t history.EventType) *int { v := int(t); return &v }
	rows := []history.Entry{
		{ID: 1, EventType: g(history.EventTypeGrabbed), SourceTitle: "g1"},
		{ID: 2, EventType: g(history.EventTypeDownloadFailed), SourceTitle: "f1"},
		{ID: 3, EventType: g(history.EventTypeGrabbed), SourceTitle: "g2"},
		{ID: 4, EventType: nil, SourceTitle: "null-row"},
		{ID: 5, EventType: g(history.EventTypeDownloadFolderImported), SourceTitle: "i1"},
	}
	s := history.NewService(stubReader{out: rows})

	got, err := s.Find(context.Background(), "abc", history.EventTypeGrabbed)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].SourceTitle != "g1" || got[1].SourceTitle != "g2" {
		t.Errorf("filter changed order: got %+v", got)
	}
}

func TestFindNilEventTypeRowsAlwaysSkipped(t *testing.T) {
	t.Parallel()

	// Even when filtering by Unknown=0, rows with nil EventType are
	// not the same as rows with EventType=Unknown (the column predates
	// the schema migration). Confirm the filter only returns rows
	// with an explicit value.
	rows := []history.Entry{
		{ID: 1, EventType: nil, SourceTitle: "nil-row"},
		{ID: 2, EventType: ptr(0), SourceTitle: "unknown-row"},
	}
	s := history.NewService(stubReader{out: rows})

	got, err := s.Find(context.Background(), "abc", history.EventTypeUnknown)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].SourceTitle != "unknown-row" {
		t.Errorf("filter included nil row or skipped explicit-Unknown: got %+v", got)
	}
}

func TestFindUnmatchedEventTypeReturnsEmpty(t *testing.T) {
	t.Parallel()

	g := func(t history.EventType) *int { v := int(t); return &v }
	rows := []history.Entry{
		{EventType: g(history.EventTypeGrabbed)},
	}
	s := history.NewService(stubReader{out: rows})
	got, err := s.Find(context.Background(), "abc", history.EventTypeDownloadIgnored)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("unmatched eventType returned %d rows, want 0", len(got))
	}
}

func ptr(i int) *int { return &i }

// --- repo-backed integration suite ---------------------------------

// runServiceSuite exercises the Service against a real repo on the
// supplied DB engine. Pins the date-DESC ordering the service relies
// on, and the SQL-layer integration so a regression in the repo's
// ORDER BY (or the row scan path) gets caught here rather than at
// production-wiring time.
func runServiceSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := history.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc := history.NewService(repo)

	now := time.Date(2026, 5, 7, 14, 30, 0, 0, time.UTC)

	g := func(t history.EventType) *int { v := int(t); return &v }
	type seed struct {
		t   *int
		at  time.Time
		src string
	}
	seeds := []seed{
		{g(history.EventTypeGrabbed), now.Add(-3 * time.Hour), "older-grab"},
		{g(history.EventTypeDownloadFailed), now.Add(-2 * time.Hour), "fail"},
		{g(history.EventTypeGrabbed), now.Add(-time.Hour), "newer-grab"},
		{g(history.EventTypeDownloadFolderImported), now, "imported"},
		{nil, now.Add(-30 * time.Minute), "null-event"},
	}
	for _, sd := range seeds {
		e := &history.Entry{
			EpisodeID:   100,
			SeriesID:    50,
			SourceTitle: sd.src,
			Date:        sd.at,
			Quality:     `{}`,
			Data:        map[string]string{},
			EventType:   sd.t,
			DownloadID:  "test-svc",
			Languages:   []int{1},
		}
		if _, err := repo.Insert(t.Context(), e); err != nil {
			t.Fatalf("Insert %s: %v", sd.src, err)
		}
	}

	// FindByDownloadID returns ALL 5 rows ordered date-DESC.
	all, err := svc.FindByDownloadID(t.Context(), "test-svc")
	if err != nil {
		t.Fatalf("FindByDownloadID: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("FindByDownloadID got %d rows, want 5", len(all))
	}
	if all[0].SourceTitle != "imported" || all[4].SourceTitle != "older-grab" {
		t.Errorf("date-DESC ordering broken: first=%q last=%q", all[0].SourceTitle, all[4].SourceTitle)
	}

	// Find(EventTypeGrabbed) returns 2 rows (newer-grab + older-grab),
	// in date-DESC order. The Failed / Imported / null rows are
	// skipped.
	grabs, err := svc.Find(t.Context(), "test-svc", history.EventTypeGrabbed)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(grabs) != 2 {
		t.Fatalf("Find Grabbed got %d, want 2", len(grabs))
	}
	if grabs[0].SourceTitle != "newer-grab" || grabs[1].SourceTitle != "older-grab" {
		t.Errorf("Grabbed filter order wrong: %q, %q", grabs[0].SourceTitle, grabs[1].SourceTitle)
	}

	// Find for a never-used EventType returns empty.
	none, err := svc.Find(t.Context(), "test-svc", history.EventTypeEpisodeFileRenamed)
	if err != nil {
		t.Fatalf("Find unmatched: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("Find unmatched got %d, want 0", len(none))
	}

	// Unknown DownloadID — both methods return empty.
	if got, err := svc.FindByDownloadID(t.Context(), "missing"); err != nil || len(got) != 0 {
		t.Errorf("FindByDownloadID(missing): got (%v, %v)", got, err)
	}
	if got, err := svc.Find(t.Context(), "missing", history.EventTypeGrabbed); err != nil || len(got) != 0 {
		t.Errorf("Find(missing): got (%v, %v)", got, err)
	}
}

func TestService_SQLite(t *testing.T) {
	t.Parallel()
	runServiceSuite(t, datastoretest.OpenSQLite(t))
}

func TestService_Postgres(t *testing.T) {
	runServiceSuite(t, datastoretest.OpenPostgres(t))
}
