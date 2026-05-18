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

// ---------- 5.B3: CRUD tests ----------

type fakeCustomFilterWriter struct {
	row       *customfilters.Filter
	getErr    error
	addErr    error
	updateErr error
	deleteErr error

	addCalledWith    *customfilters.Filter
	updateCalledWith *customfilters.Filter
	deleteCalledID   int
}

func (f *fakeCustomFilterWriter) Get(_ context.Context, id int) (*customfilters.Filter, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.row == nil {
		return nil, customfilters.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeCustomFilterWriter) Add(_ context.Context, filter *customfilters.Filter) (*customfilters.Filter, error) {
	f.addCalledWith = filter
	if f.addErr != nil {
		return nil, f.addErr
	}
	return &customfilters.Filter{ID: 11, Type: filter.Type, Label: filter.Label, Filters: filter.Filters}, nil
}

func (f *fakeCustomFilterWriter) Update(_ context.Context, filter *customfilters.Filter) error {
	f.updateCalledWith = filter
	return f.updateErr
}

func (f *fakeCustomFilterWriter) Delete(_ context.Context, id int) error {
	f.deleteCalledID = id
	return f.deleteErr
}

// ----- GET /customfilter/{id} -----

func TestCustomFilterByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFilterWriter{row: &customfilters.Filter{ID: 7, Type: "series", Label: "monitored", Filters: `[{"k":"monitored","v":true}]`}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/customfilter/7", nil)
	req.SetPathValue("id", "7")
	CustomFilterByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"id":7`) || !strings.Contains(body, `"label":"monitored"`) {
		t.Errorf("body: %s", body)
	}
	if !strings.Contains(body, `"filters":[{"k":"monitored","v":true}]`) {
		t.Errorf("filters round-trip: %s", body)
	}
}

func TestCustomFilterByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/customfilter/99", nil)
	req.SetPathValue("id", "99")
	CustomFilterByIDHandler(&fakeCustomFilterWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

// ----- POST /customfilter -----

func TestCustomFilterCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFilterWriter{}
	rr := httptest.NewRecorder()
	body := `{"type":"series","label":"new","filters":[{"key":"monitored","value":true}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customfilter", strings.NewReader(body))
	CustomFilterCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201; body=%s", rr.Code, rr.Body.String())
	}
	if w.addCalledWith == nil {
		t.Fatal("svc.Add not called")
	}
	if w.addCalledWith.Type != "series" || w.addCalledWith.Label != "new" {
		t.Errorf("svc.Add args: %+v", w.addCalledWith)
	}
	if !strings.Contains(w.addCalledWith.Filters, `"monitored"`) {
		t.Errorf("filters serialised round-trip: %q", w.addCalledWith.Filters)
	}
	if !strings.Contains(rr.Body.String(), `"id":11`) {
		t.Errorf("response should carry new id: %s", rr.Body.String())
	}
}

func TestCustomFilterCreateHandler_EmptyFiltersListBecomesEmptyArray(t *testing.T) {
	t.Parallel()
	// {"filters": []} must persist as "[]" not "" — matches C#
	// STJson.ToJson(new List<ExpandoObject>()) emitting "[]".
	w := &fakeCustomFilterWriter{}
	rr := httptest.NewRecorder()
	body := `{"type":"series","label":"new","filters":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customfilter", strings.NewReader(body))
	CustomFilterCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.addCalledWith.Filters != "[]" {
		t.Errorf("Filters persisted form: got %q want []", w.addCalledWith.Filters)
	}
}

func TestCustomFilterCreateHandler_RequiredFields400(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"missingType", `{"type":"","label":"x","filters":[]}`, `"propertyName":"Type"`},
		{"missingLabel", `{"type":"x","label":"","filters":[]}`, `"propertyName":"Label"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v3/customfilter", strings.NewReader(c.body))
			CustomFilterCreateHandler(&fakeCustomFilterWriter{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: got %d", rr.Code)
			}
			if !strings.HasPrefix(rr.Body.String(), "[") {
				t.Errorf("body should be validation array: %s", rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), c.want) {
				t.Errorf("body must contain %q: %s", c.want, rr.Body.String())
			}
		})
	}
}

// ----- PUT /customfilter/{id} -----

func TestCustomFilterUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFilterWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":7,"type":"series","label":"renamed","filters":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customfilter/7", strings.NewReader(body))
	req.SetPathValue("id", "7")
	CustomFilterUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202", rr.Code)
	}
	if w.updateCalledWith.ID != 7 || w.updateCalledWith.Label != "renamed" {
		t.Errorf("svc.Update args: %+v", w.updateCalledWith)
	}
}

func TestCustomFilterUpdateHandler_PathIDOverridesBody(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFilterWriter{}
	rr := httptest.NewRecorder()
	// Body says id=999; path says id=7. C# RestPutById uses URL id.
	body := `{"id":999,"type":"x","label":"y","filters":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customfilter/7", strings.NewReader(body))
	req.SetPathValue("id", "7")
	CustomFilterUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.updateCalledWith.ID != 7 {
		t.Errorf("path id must win: got %d want 7", w.updateCalledWith.ID)
	}
}

func TestCustomFilterUpdateHandler_NotFound404(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFilterWriter{updateErr: customfilters.ErrNotFound}
	rr := httptest.NewRecorder()
	body := `{"type":"x","label":"y","filters":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customfilter/99", strings.NewReader(body))
	req.SetPathValue("id", "99")
	CustomFilterUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

// ----- DELETE /customfilter/{id} -----

func TestCustomFilterDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFilterWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/customfilter/7", nil)
	req.SetPathValue("id", "7")
	CustomFilterDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
	if w.deleteCalledID != 7 {
		t.Errorf("svc.Delete called with %d", w.deleteCalledID)
	}
}

func TestCustomFilterDeleteHandler_NotFound404(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFilterWriter{deleteErr: customfilters.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/customfilter/99", nil)
	req.SetPathValue("id", "99")
	CustomFilterDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d", rr.Code)
	}
}
