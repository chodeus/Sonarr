package logs_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/logs"
)

func newRepo(t *testing.T) *logs.Repo {
	t.Helper()
	r, err := logs.NewRepo(datastoretest.OpenSQLiteLog(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return r
}

func TestInsertAndPaged(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := t.Context()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, err := r.Insert(ctx, &logs.Log{
			Message: "msg", Time: base.Add(time.Duration(i) * time.Hour),
			Logger: "Test", Level: "Warn",
		}); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	// Newest-first by Time.
	page, err := r.Paged(ctx, logs.PageQuery{Page: 1, PageSize: 2, SortKey: "time", Desc: true})
	if err != nil {
		t.Fatalf("Paged: %v", err)
	}
	if page.TotalRecords != 3 {
		t.Errorf("total = %d, want 3", page.TotalRecords)
	}
	if len(page.Records) != 2 {
		t.Fatalf("records = %d, want 2 (pageSize)", len(page.Records))
	}
	if !page.Records[0].Time.Equal(base.Add(2 * time.Hour)) {
		t.Errorf("first record time = %v, want newest %v", page.Records[0].Time, base.Add(2*time.Hour))
	}

	// Page 2 carries the remaining row.
	page2, err := r.Paged(ctx, logs.PageQuery{Page: 2, PageSize: 2, SortKey: "time", Desc: true})
	if err != nil {
		t.Fatalf("Paged p2: %v", err)
	}
	if len(page2.Records) != 1 {
		t.Errorf("page2 records = %d, want 1", len(page2.Records))
	}
}

func TestPaged_NullableExceptionRoundTrips(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := t.Context()
	if _, err := r.Insert(ctx, &logs.Log{
		Message: "boom", Time: time.Now().UTC(), Logger: "X", Level: "Error",
		Exception: "kaboom", ExceptionType: "System.Exception",
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	// A row with no exception (stored as NULL) must scan back to "".
	if _, err := r.Insert(ctx, &logs.Log{
		Message: "info", Time: time.Now().UTC(), Logger: "X", Level: "Info",
	}); err != nil {
		t.Fatalf("Insert plain: %v", err)
	}
	page, err := r.Paged(ctx, logs.PageQuery{Page: 1, PageSize: 50, SortKey: "level", Desc: false})
	if err != nil {
		t.Fatalf("Paged: %v", err)
	}
	if len(page.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(page.Records))
	}
	var sawException, sawEmpty bool
	for _, l := range page.Records {
		if l.Exception == "kaboom" && l.ExceptionType == "System.Exception" {
			sawException = true
		}
		if l.Level == "Info" && l.Exception == "" && l.ExceptionType == "" {
			sawEmpty = true
		}
	}
	if !sawException || !sawEmpty {
		t.Errorf("exception round-trip: sawException=%v sawEmpty=%v", sawException, sawEmpty)
	}
}

func TestPaged_LevelFilter(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	for _, lv := range []string{"Info", "Warn", "Error", "Error"} {
		if _, err := r.Insert(ctx, &logs.Log{Message: "m", Time: now, Logger: "L", Level: lv}); err != nil {
			t.Fatalf("Insert %s: %v", lv, err)
		}
	}
	// Filter to the {Fatal,Error,Warn} set (the "warn" cascade): excludes Info.
	page, err := r.Paged(ctx, logs.PageQuery{Page: 1, PageSize: 50, SortKey: "time", Desc: true, Levels: []string{"Fatal", "Error", "Warn"}})
	if err != nil {
		t.Fatalf("Paged: %v", err)
	}
	if page.TotalRecords != 3 {
		t.Errorf("filtered total = %d, want 3 (1 Warn + 2 Error)", page.TotalRecords)
	}
	for _, l := range page.Records {
		if l.Level == "Info" {
			t.Errorf("Info row leaked through the warn-cascade filter")
		}
	}
}

func TestPurge(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := t.Context()
	for i := 0; i < 5; i++ {
		if _, err := r.Insert(ctx, &logs.Log{Message: "m", Time: time.Now().UTC(), Logger: "L", Level: "Warn"}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	n, err := r.Purge(ctx)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n != 5 {
		t.Errorf("purged = %d, want 5", n)
	}
	page, err := r.Paged(ctx, logs.PageQuery{Page: 1, PageSize: 50, SortKey: "time", Desc: true})
	if err != nil {
		t.Fatalf("Paged: %v", err)
	}
	if page.TotalRecords != 0 {
		t.Errorf("after purge total = %d, want 0", page.TotalRecords)
	}
}

func TestTrim(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := t.Context()
	now := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	// One old (>7d), one recent.
	if _, err := r.Insert(ctx, &logs.Log{Message: "old", Time: now.AddDate(0, 0, -10), Logger: "L", Level: "Warn"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Insert(ctx, &logs.Log{Message: "new", Time: now.AddDate(0, 0, -1), Logger: "L", Level: "Warn"}); err != nil {
		t.Fatal(err)
	}
	n, err := r.Trim(ctx, now)
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if n != 1 {
		t.Errorf("trimmed = %d, want 1 (the >7d row)", n)
	}
	page, _ := r.Paged(ctx, logs.PageQuery{Page: 1, PageSize: 50, SortKey: "time", Desc: true})
	if page.TotalRecords != 1 || page.Records[0].Message != "new" {
		t.Errorf("after trim: total=%d records=%v, want 1 'new'", page.TotalRecords, page.Records)
	}
}
