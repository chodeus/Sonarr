package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeBlocklistLister struct {
	records []BlocklistResource
	total   int
	err     error
}

func (f *fakeBlocklistLister) PagedBlocklist(context.Context, PagingRequest) ([]BlocklistResource, int, error) {
	return f.records, f.total, f.err
}

func TestBlocklistHandler_EmptyDefaults(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	BlocklistHandler(&fakeBlocklistLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/blocklist", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got PagingResource[BlocklistResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := PagingResource[BlocklistResource]{
		Page: 1, PageSize: 10, Records: []BlocklistResource{},
		SortDirection: "descending", SortKey: "date", TotalRecords: 0,
	}
	if got.Page != want.Page || got.PageSize != want.PageSize ||
		got.SortDirection != want.SortDirection || got.SortKey != want.SortKey ||
		got.TotalRecords != want.TotalRecords {
		t.Errorf("envelope mismatch: got %+v want %+v", got, want)
	}
	// Body must contain literal "[]" (records nil would marshal as null).
	if body := rr.Body.String(); !contains(body, `"records":[]`) {
		t.Errorf(`body missing "records":[]: %s`, body)
	}
}

func TestBlocklistHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	BlocklistHandler(&fakeBlocklistLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/blocklist", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
