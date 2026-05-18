package v3

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseHandler_EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ReleaseHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestReleaseHandler_AllQueryBranches(t *testing.T) {
	t.Parallel()
	cases := []string{
		"?episodeId=42",
		"?seriesId=1&seasonNumber=2",
		"?seriesId=1",
		"",
	}
	for _, qs := range cases {
		rr := httptest.NewRecorder()
		ReleaseHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release"+qs, nil))
		if rr.Code != http.StatusOK || rr.Body.String() != "[]" {
			t.Errorf("query %q: status=%d body=%q", qs, rr.Code, rr.Body.String())
		}
	}
}

func TestReleaseHandler_InvalidInt_400(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"seriesId", "episodeId", "seasonNumber"} {
		rr := httptest.NewRecorder()
		ReleaseHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?"+key+"=abc", nil))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s=abc: status %d", key, rr.Code)
		}
	}
}
