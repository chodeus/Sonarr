package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
)

// fakeLister implements Lister (alias of apiv3.HealthResultsLister)
// for the v5 ListHandler. Returns a configured slice; tests can
// shuffle the input to verify the handler's sort.
type fakeLister struct {
	results []healthcheck.HealthCheck
}

func (f *fakeLister) Results() []healthcheck.HealthCheck {
	out := make([]healthcheck.HealthCheck, len(f.results))
	copy(out, f.results)
	return out
}

// TestListHandler_EmitsReasonField pins the v5-specific addition over
// v3 — the response shape MUST include a `reason` string per the
// openapi v5 HealthResource schema. Without J254's Reason.String()
// reverse-lookup table this field would emit as int (the Go enum's
// raw value).
func TestListHandler_EmitsReasonField(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{results: []healthcheck.HealthCheck{
		{
			Source:  "IndexerRssCheck",
			Type:    healthcheck.ResultError,
			Reason:  healthcheck.ReasonIndexerRssNoIndexersAvailable,
			Message: "No indexers available with RSS sync enabled",
			WikiURL: "https://wiki.servarr.com/sonarr/system#no-indexers-available-with-rss-sync-enabled",
		},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/health", nil)
	ListHandler(lister).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got []Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("rows: got %d, want 1", len(got))
	}
	if got[0].Reason != "indexerRssNoIndexersAvailable" {
		t.Errorf("reason: got %q, want %q (camelCase per openapi v5)", got[0].Reason, "indexerRssNoIndexersAvailable")
	}
	if got[0].Type != "error" {
		t.Errorf("type: got %q, want %q (camelCase per openapi v5)", got[0].Type, "error")
	}
}

// TestListHandler_DeterministicSort pins the Result-DESC, Source-ASC
// order regardless of input order. The fakeLister shuffles the rows;
// the handler must order them deterministically.
func TestListHandler_DeterministicSort(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{results: []healthcheck.HealthCheck{
		{Source: "BetaCheck", Type: healthcheck.ResultWarning, Reason: healthcheck.ReasonPackage, Message: "B"},
		{Source: "AlphaCheck", Type: healthcheck.ResultError, Reason: healthcheck.ReasonAppDataLocation, Message: "A"},
		{Source: "GammaCheck", Type: healthcheck.ResultWarning, Reason: healthcheck.ReasonProxyFailed, Message: "G"},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/health", nil)
	ListHandler(lister).ServeHTTP(rr, req)
	var got []Resource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 3 {
		t.Fatalf("rows: got %d, want 3", len(got))
	}
	// Expected: Error first (AlphaCheck), then Warnings alphabetically
	// (BetaCheck, GammaCheck).
	wantOrder := []string{"AlphaCheck", "BetaCheck", "GammaCheck"}
	for i, want := range wantOrder {
		if got[i].Source != want {
			t.Errorf("position %d: got source=%q, want %q", i, got[i].Source, want)
		}
	}
}

// TestListHandler_EmptyReturnsBracketsNotNull pins the empty contract:
// `[]` not `null`. Matches the JS-side arr.map() non-crash invariant
// established across v3 + earlier v5 ports.
func TestListHandler_EmptyReturnsBracketsNotNull(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/health", nil)
	ListHandler(&fakeLister{results: nil}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("body: got %q, want \"[]\"", got)
	}
}
