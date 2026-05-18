package v3

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManualImportHandler_EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ManualImportHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/manualimport", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestManualImportHandler_QueryParamsAccepted(t *testing.T) {
	t.Parallel()
	cases := []string{
		"?folder=/tmp/downloads",
		"?downloadId=abc123",
		"?seriesId=1&seasonNumber=2",
		"?seriesId=1&filterExistingFiles=true",
	}
	for _, qs := range cases {
		rr := httptest.NewRecorder()
		ManualImportHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/manualimport"+qs, nil))
		if rr.Code != http.StatusOK || rr.Body.String() != "[]" {
			t.Errorf("query %q: status=%d body=%q", qs, rr.Code, rr.Body.String())
		}
	}
}

func TestManualImportHandler_InvalidInt_400(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"seriesId", "seasonNumber"} {
		rr := httptest.NewRecorder()
		ManualImportHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/manualimport?"+key+"=abc", nil))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s=abc: status %d", key, rr.Code)
		}
	}
}
