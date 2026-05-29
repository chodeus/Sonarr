package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeLogLister struct {
	records   []LogResource
	total     int
	err       error
	gotLevels []string
	gotSort   string
	gotDesc   bool
}

func (f *fakeLogLister) PagedLogs(_ context.Context, _, _ int, sortKey string, desc bool, levels []string) ([]LogResource, int, error) {
	f.gotSort = sortKey
	f.gotDesc = desc
	f.gotLevels = levels
	return f.records, f.total, f.err
}

// A nil lister (log DB disabled) returns a 200 empty page with SortKey
// rewritten to "time".
func TestLogHandler_NilLister_EmptyPage(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	LogHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/log", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
	var resp PagingResource[LogResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.TotalRecords != 0 || len(resp.Records) != 0 {
		t.Errorf("want empty page, got total=%d records=%d", resp.TotalRecords, len(resp.Records))
	}
	if resp.SortKey != "time" {
		t.Errorf("SortKey = %q, want time", resp.SortKey)
	}
}

// A populated lister's rows + total flow into the paged envelope.
func TestLogHandler_ReturnsRows(t *testing.T) {
	t.Parallel()
	lister := &fakeLogLister{
		records: []LogResource{{ID: 7, Level: "Warn", Message: "hi"}},
		total:   1,
	}
	rr := httptest.NewRecorder()
	LogHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/log", nil))
	var resp PagingResource[LogResource]
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.TotalRecords != 1 || len(resp.Records) != 1 || resp.Records[0].ID != 7 {
		t.Fatalf("resp = %+v, want 1 record id=7", resp)
	}
	// C# sorts by id internally, descending by default.
	if lister.gotSort != "id" {
		t.Errorf("internal sort = %q, want id", lister.gotSort)
	}
	if !lister.gotDesc {
		t.Errorf("desc = false, want true (default descending)")
	}
}

// The `level` query param expands to a cumulative filter set.
func TestLogHandler_LevelFilterExpands(t *testing.T) {
	t.Parallel()
	lister := &fakeLogLister{}
	rr := httptest.NewRecorder()
	LogHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/log?level=warn", nil))
	if got := strings.Join(lister.gotLevels, ","); got != "Fatal,Error,Warn" {
		t.Errorf("levels = %q, want Fatal,Error,Warn", got)
	}
}

// A lister error surfaces as 500.
func TestLogHandler_ListerError_500(t *testing.T) {
	t.Parallel()
	lister := &fakeLogLister{err: context.DeadlineExceeded}
	rr := httptest.NewRecorder()
	LogHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/log", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rr.Code)
	}
}

// expandLogLevelFilter cascade table.
func TestExpandLogLevelFilter(t *testing.T) {
	t.Parallel()
	cases := map[string]int{"": 0, "unknown": 0, "fatal": 1, "error": 2, "warn": 3, "info": 4, "debug": 5, "trace": 6}
	for in, want := range cases {
		if got := len(expandLogLevelFilter(in)); got != want {
			t.Errorf("expandLogLevelFilter(%q) = %d levels, want %d", in, got, want)
		}
	}
}
