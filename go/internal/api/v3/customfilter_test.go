package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customfilters"
)

type fakeCustomFilterLister struct {
	rows []customfilters.Filter
	err  error
}

func (f *fakeCustomFilterLister) All(context.Context) ([]customfilters.Filter, error) {
	return f.rows, f.err
}

func TestCustomFilterHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFilterHandler(&fakeCustomFilterLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customfilter", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestCustomFilterHandler_DeserialisesFilters(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFilterHandler(&fakeCustomFilterLister{rows: []customfilters.Filter{
		{ID: 1, Type: "series", Label: "Continuing", Filters: `[{"key":"status","value":["continuing"]}]`},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customfilter", nil))

	body := rr.Body.String()
	// Filters round-trips as a parsed array of objects, NOT a
	// stringified blob.
	if !strings.Contains(body, `"filters":[{"key":"status","value":["continuing"]}]`) {
		t.Errorf("filters not deserialised: %s", body)
	}

	// Parse-back through resource to confirm the round-trip.
	var got []CustomFilterResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Type != "series" || got[0].Label != "Continuing" {
		t.Fatalf("row: %+v", got)
	}
	if len(got[0].Filters) != 1 {
		t.Errorf("filters length: got %d want 1", len(got[0].Filters))
	}
}

func TestCustomFilterHandler_MalformedFiltersFallsBackToEmpty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFilterHandler(&fakeCustomFilterLister{rows: []customfilters.Filter{
		{ID: 1, Type: "x", Label: "y", Filters: "not valid json"},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customfilter", nil))
	body := rr.Body.String()
	if !strings.Contains(body, `"filters":[]`) {
		t.Errorf("malformed filters should yield []: %s", body)
	}
}

func TestCustomFilterHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFilterHandler(&fakeCustomFilterLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customfilter", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
