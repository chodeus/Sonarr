package v3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
)

type fakeImportListLister struct {
	rows []importlists.ImportList
	err  error
}

func (f *fakeImportListLister) All(context.Context) ([]importlists.ImportList, error) {
	return f.rows, f.err
}

func TestImportListHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListHandler(&fakeImportListLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlist", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestImportListHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListHandler(&fakeImportListLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlist", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// fakeImportListWriter implements ImportListWriter against an in-memory
// table — same pattern as fakeIndexerWriter.
type fakeImportListWriter struct {
	rows   []importlists.ImportList
	nextID int64
	getErr error
}

func (f *fakeImportListWriter) Get(_ context.Context, id int) (*importlists.ImportList, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, importlists.ErrNotFound
}
func (f *fakeImportListWriter) All(_ context.Context) ([]importlists.ImportList, error) {
	out := make([]importlists.ImportList, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
func (f *fakeImportListWriter) Insert(_ context.Context, n *importlists.ImportList) (*importlists.ImportList, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}
func (f *fakeImportListWriter) Update(_ context.Context, n *importlists.ImportList) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return importlists.ErrNotFound
}
func (f *fakeImportListWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return importlists.ErrNotFound
}

type fakeImportListEmitter struct {
	created []ImportListResource
	updated []ImportListResource
	deleted []ImportListResource
}

func (f *fakeImportListEmitter) Created(_ context.Context, r ImportListResource) error {
	f.created = append(f.created, r)
	return nil
}
func (f *fakeImportListEmitter) Updated(_ context.Context, r ImportListResource) error {
	f.updated = append(f.updated, r)
	return nil
}
func (f *fakeImportListEmitter) Deleted(_ context.Context, r ImportListResource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

func validImportListBody() ImportListResource {
	return ImportListResource{
		providerResourceCore: providerResourceCore{
			Name:               "MyList",
			Implementation:     "SonarrImport",
			ImplementationName: "SonarrImport",
			ConfigContract:     "SonarrSettings",
			Tags:               []int{},
		},
		EnableAutomaticAdd:       true,
		SearchForMissingEpisodes: false,
		ShouldMonitor:            1,
		MonitorNewItems:          1,
		RootFolderPath:           "/tv",
		QualityProfileID:         1,
		SeriesType:               0,
		SeasonFolder:             true,
		Fields: []ProviderField{
			{Name: "baseUrl", Value: "http://upstream:8989"},
			{Name: "apiKey", Value: "abc123"},
		},
	}
}

func TestImportListCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeImportListWriter{}
	em := &fakeImportListEmitter{}
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListCreateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist", bytes.NewReader(buf)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].Name != "MyList" || w.rows[0].ID <= 0 {
		t.Errorf("row not inserted: %+v", w.rows)
	}
	if w.rows[0].Settings == nil {
		t.Fatalf("settings nil")
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(*w.rows[0].Settings), &settings)
	if settings["baseUrl"] != "http://upstream:8989" {
		t.Errorf("settings packing: %+v", settings)
	}
	if len(em.created) != 1 {
		t.Errorf("emitter.Created not fired: %+v", em.created)
	}
}

func TestImportListCreateHandler_NameMissing_400(t *testing.T) {
	t.Parallel()
	body := validImportListBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListCreateHandler(&fakeImportListWriter{}, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListCreateHandler_DuplicateName_400(t *testing.T) {
	t.Parallel()
	w := &fakeImportListWriter{rows: []importlists.ImportList{{ID: 9, Name: "mylist"}}}
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListCreateHandler(w, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestImportListCreateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListCreateHandler(&fakeImportListWriter{}, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	existingSettings := `{"baseUrl":"old"}`
	existing := importlists.ImportList{ID: 42, Name: "old", Implementation: "SonarrImport", Settings: &existingSettings}
	w := &fakeImportListWriter{rows: []importlists.ImportList{existing}}
	em := &fakeImportListEmitter{}
	body := validImportListBody()
	body.ID = 42
	body.Name = "renamed"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/importlist/42", bytes.NewReader(buf))
	req.SetPathValue("id", "42")
	rr := httptest.NewRecorder()
	ImportListUpdateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Name != "renamed" {
		t.Errorf("row not renamed: %+v", w.rows[0])
	}
	if len(em.updated) != 1 {
		t.Errorf("emitter.Updated not fired")
	}
}

func TestImportListUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	body := validImportListBody()
	body.ID = 999
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/importlist/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	ImportListUpdateHandler(&fakeImportListWriter{}, &fakeImportListEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListUpdateHandler_BodyIDFallback(t *testing.T) {
	t.Parallel()
	existing := importlists.ImportList{ID: 5, Name: "old", Implementation: "SonarrImport"}
	w := &fakeImportListWriter{rows: []importlists.ImportList{existing}}
	body := validImportListBody()
	body.ID = 5
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/importlist/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	ImportListUpdateHandler(w, &fakeImportListEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestImportListUpdateHandler_OmittedFields_KeepsSettings(t *testing.T) {
	t.Parallel()
	existingSettings := `{"baseUrl":"keep"}`
	existing := importlists.ImportList{ID: 1, Name: "old", Implementation: "SonarrImport", Settings: &existingSettings}
	w := &fakeImportListWriter{rows: []importlists.ImportList{existing}}
	body := validImportListBody()
	body.ID = 1
	body.Fields = nil
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/importlist/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	ImportListUpdateHandler(w, &fakeImportListEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Settings == nil || *w.rows[0].Settings != `{"baseUrl":"keep"}` {
		t.Errorf("settings should be preserved: %v", w.rows[0].Settings)
	}
}

func TestImportListDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeImportListWriter{rows: []importlists.ImportList{{ID: 7, Name: "victim"}}}
	em := &fakeImportListEmitter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlist/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	ImportListDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(em.deleted) != 1 || em.deleted[0].ID != 7 {
		t.Errorf("emitter.Deleted: %+v", em.deleted)
	}
}

func TestImportListDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlist/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	ImportListDeleteHandler(&fakeImportListWriter{}, &fakeImportListEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlist/"+raw, nil)
			req.SetPathValue("id", raw)
			rr := httptest.NewRecorder()
			ImportListDeleteHandler(&fakeImportListWriter{}, &fakeImportListEmitter{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: %d (id=%q)", rr.Code, raw)
			}
		})
	}
}

// fakeImportListTester implements ImportListTester.
type fakeImportListTester struct {
	seen   *importlists.ImportList
	result []importlists.TestFailure
	err    error
}

func (f *fakeImportListTester) Test(_ context.Context, n *importlists.ImportList) ([]importlists.TestFailure, error) {
	f.seen = n
	return f.result, f.err
}

func TestImportListTestHandler_Success_Returns200EmptyObject(t *testing.T) {
	t.Parallel()
	tester := &fakeImportListTester{}
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlist/test", bytes.NewReader(buf))
	rr := httptest.NewRecorder()
	ImportListTestHandler(tester).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if tester.seen == nil || tester.seen.Implementation != "SonarrImport" {
		t.Errorf("Test should be invoked with built ImportList: %+v", tester.seen)
	}
	if tester.seen.Settings == nil {
		t.Fatalf("Settings nil after fromResource")
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(*tester.seen.Settings), &settings)
	if settings["baseUrl"] != "http://upstream:8989" {
		t.Errorf("settings packing: %+v", settings)
	}
}

func TestImportListTestHandler_FailuresProjectTo400Array(t *testing.T) {
	t.Parallel()
	tester := &fakeImportListTester{result: []importlists.TestFailure{
		{PropertyName: "BaseUrl", ErrorMessage: "could not connect"},
		{PropertyName: "", ErrorMessage: "Test failed: timeout"},
	}}
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var failures []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &failures); err != nil {
		t.Fatalf("body must be FluentValidation array: %v body=%s", err, rr.Body.String())
	}
	if len(failures) != 2 || failures[0]["propertyName"] != "BaseUrl" {
		t.Errorf("failures: %+v", failures)
	}
}

func TestImportListTestHandler_UnknownImplementation_400(t *testing.T) {
	t.Parallel()
	tester := &fakeImportListTester{err: importlists.ErrNotRegistered}
	body := validImportListBody()
	body.Implementation = "GhostProvider"
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	var failures []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &failures)
	if len(failures) != 1 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected single Implementation failure: %+v", failures)
	}
}

func TestImportListTestHandler_OtherError_500(t *testing.T) {
	t.Parallel()
	tester := &fakeImportListTester{err: errors.New("upstream timeout")}
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListTestHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListTestHandler(&fakeImportListTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/test", bytes.NewReader([]byte(`{not json`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListTestHandler_SkipsSharedValidator(t *testing.T) {
	t.Parallel()
	tester := &fakeImportListTester{}
	body := validImportListBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Errorf("skip-validation: status=%d body=%s", rr.Code, rr.Body.String())
	}
}
