package v3

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePagingRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		url    string
		defKey string
		want   PagingRequest
	}{
		{
			name:   "all_defaults",
			url:    "/api/v3/blocklist",
			defKey: "date",
			want:   PagingRequest{Page: 1, PageSize: 10, SortKey: "date", SortDirection: "descending"},
		},
		{
			name:   "explicit_page",
			url:    "/api/v3/blocklist?page=3&pageSize=25",
			defKey: "date",
			want:   PagingRequest{Page: 3, PageSize: 25, SortKey: "date", SortDirection: "descending"},
		},
		{
			name:   "sort_overrides",
			url:    "/api/v3/blocklist?sortKey=title&sortDirection=Ascending",
			defKey: "date",
			want:   PagingRequest{Page: 1, PageSize: 10, SortKey: "title", SortDirection: "ascending"},
		},
		{
			name:   "invalid_page_falls_back",
			url:    "/api/v3/blocklist?page=abc&pageSize=-5",
			defKey: "date",
			want:   PagingRequest{Page: 1, PageSize: 10, SortKey: "date", SortDirection: "descending"},
		},
		{
			name:   "empty_sortkey_keeps_default",
			url:    "/api/v3/blocklist?sortKey=",
			defKey: "timeleft",
			want:   PagingRequest{Page: 1, PageSize: 10, SortKey: "timeleft", SortDirection: "descending"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			got := parsePagingRequest(req, tc.defKey)
			if got != tc.want {
				t.Errorf("parsePagingRequest: got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestEmptyPagingResponse_RecordsNeverNil(t *testing.T) {
	t.Parallel()
	resp := emptyPagingResponse[BlocklistResource](PagingRequest{Page: 1, PageSize: 10, SortKey: "date", SortDirection: "descending"})
	if resp.Records == nil {
		t.Fatal("Records nil — would marshal as null, breaking byte-eq")
	}
	if len(resp.Records) != 0 {
		t.Errorf("Records should be empty: got %d", len(resp.Records))
	}
}
