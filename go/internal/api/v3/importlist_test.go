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

// TestImportListDeleteHandler_FiresHookOnSuccess — J502: the post-delete
// hook (→ ListDeletedEvent publish) fires with the deleted id only on an
// actual delete, never on a missing-id swallow.
func TestImportListDeleteHandler_FiresHookOnSuccess(t *testing.T) {
	t.Parallel()
	w := &fakeImportListWriter{rows: []importlists.ImportList{{ID: 7, Name: "victim"}}}
	em := &fakeImportListEmitter{}
	var fired []int
	hook := func(_ context.Context, id int) { fired = append(fired, id) }
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlist/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	ImportListDeleteHandler(w, em, hook).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(fired) != 1 || fired[0] != 7 {
		t.Errorf("hook fired: %v want [7]", fired)
	}
}

func TestImportListDeleteHandler_NoHookOnMissing(t *testing.T) {
	t.Parallel()
	var fired []int
	hook := func(_ context.Context, id int) { fired = append(fired, id) }
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlist/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	ImportListDeleteHandler(&fakeImportListWriter{}, &fakeImportListEmitter{}, hook).ServeHTTP(rr, req)
	if len(fired) != 0 {
		t.Errorf("hook must not fire on missing-id swallow, got %v", fired)
	}
}

// TestImportListDeleteHandler_Idempotent — C.1.i-follow-4 (J232). 200 + `{}` on missing id.
func TestImportListDeleteHandler_Idempotent(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlist/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	em := &fakeImportListEmitter{}
	ImportListDeleteHandler(&fakeImportListWriter{}, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (idempotent)", rr.Code)
	}
	if len(em.deleted) > 0 {
		t.Errorf("SignalR Deleted broadcast must NOT fire on missing-id swallow")
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

// perRowImportListTester maps row ID → outcome.
type perRowImportListTester struct {
	seen     []int
	byID     map[int][]importlists.TestFailure
	errByID  map[int]error
	defaultR []importlists.TestFailure
}

func (p *perRowImportListTester) Test(_ context.Context, n *importlists.ImportList) ([]importlists.TestFailure, error) {
	p.seen = append(p.seen, n.ID)
	if p.errByID != nil {
		if err, ok := p.errByID[n.ID]; ok {
			return nil, err
		}
	}
	if p.byID != nil {
		if r, ok := p.byID[n.ID]; ok {
			return r, nil
		}
	}
	return p.defaultR, nil
}

func TestImportListTestAllHandler_Empty_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListTestAllHandler(&fakeImportListLister{}, &perRowImportListTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `[]` {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestImportListTestAllHandler_AllPass_Returns200Array(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		{ID: 1, Name: "a", Implementation: "SonarrImport"},
		{ID: 2, Name: "b", Implementation: "PlexImport"},
	}
	tester := &perRowImportListTester{}
	rr := httptest.NewRecorder()
	ImportListTestAllHandler(&fakeImportListLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got []ProviderTestAllResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: %d", len(got))
	}
	for i, r := range got {
		if !r.IsValid {
			t.Errorf("row %d isValid: false; want true", i)
		}
	}
	if len(tester.seen) != 2 || tester.seen[0] != 1 || tester.seen[1] != 2 {
		t.Errorf("ordering: %v", tester.seen)
	}
}

func TestImportListTestAllHandler_MixedResults_Returns400(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		{ID: 1, Name: "ok", Implementation: "SonarrImport"},
		{ID: 2, Name: "bad", Implementation: "PlexImport"},
		{ID: 3, Name: "missing", Implementation: "GhostProvider"},
		{ID: 4, Name: "boom", Implementation: "SonarrImport"},
	}
	tester := &perRowImportListTester{
		byID: map[int][]importlists.TestFailure{
			2: {{PropertyName: "BaseUrl", ErrorMessage: "could not connect"}},
		},
		errByID: map[int]error{
			3: importlists.ErrNotRegistered,
			4: errors.New("transport reset"),
		},
	}
	rr := httptest.NewRecorder()
	ImportListTestAllHandler(&fakeImportListLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/testall", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got []ProviderTestAllResult
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 4 {
		t.Fatalf("len: %d", len(got))
	}
	if !got[0].IsValid {
		t.Errorf("row 0: %+v", got[0])
	}
	if got[1].IsValid || got[1].ValidationFailures[0].PropertyName != "BaseUrl" {
		t.Errorf("row 1: %+v", got[1])
	}
	if got[2].IsValid || got[2].ValidationFailures[0].PropertyName != "Implementation" {
		t.Errorf("row 2: %+v", got[2])
	}
	if got[3].IsValid || got[3].ValidationFailures[0].PropertyName != "" {
		t.Errorf("row 3: %+v", got[3])
	}
}

func TestImportListTestAllHandler_ListError_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListTestAllHandler(&fakeImportListLister{err: errors.New("db down")}, &perRowImportListTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/testall", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListTestAllHandler_ValidationFailuresNonNil(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{{ID: 7, Implementation: "SonarrImport"}}
	rr := httptest.NewRecorder()
	ImportListTestAllHandler(&fakeImportListLister{rows: rows}, &perRowImportListTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/importlist/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"validationFailures":[]`)) {
		t.Errorf("validationFailures should be [] not null: body=%s", rr.Body.String())
	}
}

// fakeImportListActioner implements ImportListActioner.
type fakeImportListActioner struct {
	seenName  string
	seenQuery map[string]string
	seenRow   *importlists.ImportList
	result    any
	err       error
}

func (f *fakeImportListActioner) RequestAction(_ context.Context, n *importlists.ImportList, name string, query map[string]string) (any, error) {
	f.seenName = name
	f.seenQuery = query
	f.seenRow = n
	return f.result, f.err
}

func TestImportListRequestActionHandler_Success(t *testing.T) {
	t.Parallel()
	actioner := &fakeImportListActioner{result: map[string]any{"profiles": []any{"any", "hd"}}}
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlist/action/getQualityProfiles?type=tv", bytes.NewReader(buf))
	req.SetPathValue("name", "getQualityProfiles")
	rr := httptest.NewRecorder()
	ImportListRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body shape: %v body=%s", err, rr.Body.String())
	}
	if _, ok := got["profiles"]; !ok {
		t.Errorf("body: %+v", got)
	}
	if actioner.seenName != "getQualityProfiles" {
		t.Errorf("action name: %q", actioner.seenName)
	}
	if actioner.seenQuery["type"] != "tv" {
		t.Errorf("query map: %+v", actioner.seenQuery)
	}
	if actioner.seenRow == nil || actioner.seenRow.Implementation != "SonarrImport" {
		t.Errorf("row not built: %+v", actioner.seenRow)
	}
}

func TestImportListRequestActionHandler_NilResult_EmitsJSONNull(t *testing.T) {
	t.Parallel()
	actioner := &fakeImportListActioner{result: nil}
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlist/action/noop", bytes.NewReader(buf))
	req.SetPathValue("name", "noop")
	rr := httptest.NewRecorder()
	ImportListRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `null` {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestImportListRequestActionHandler_ErrNotRegistered_400(t *testing.T) {
	t.Parallel()
	actioner := &fakeImportListActioner{err: importlists.ErrNotRegistered}
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlist/action/x", bytes.NewReader(buf))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	ImportListRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	var failures []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &failures)
	if len(failures) != 1 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected Implementation failure: %+v", failures)
	}
}

func TestImportListRequestActionHandler_OtherError_500(t *testing.T) {
	t.Parallel()
	actioner := &fakeImportListActioner{err: errors.New("upstream down")}
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlist/action/x", bytes.NewReader(buf))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	ImportListRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListRequestActionHandler_EmptyName_400(t *testing.T) {
	t.Parallel()
	body := validImportListBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlist/action/", bytes.NewReader(buf))
	req.SetPathValue("name", "")
	rr := httptest.NewRecorder()
	ImportListRequestActionHandler(&fakeImportListActioner{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListRequestActionHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlist/action/x", bytes.NewReader([]byte(`{nope`)))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	ImportListRequestActionHandler(&fakeImportListActioner{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func stringPtr(s string) *string { return &s }

func TestImportListBulkUpdateHandler_TagsAdd(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		{ID: 1, Name: "a", Implementation: "SonarrImport", Tags: []int{10}},
		{ID: 2, Name: "b", Implementation: "SonarrImport", Tags: []int{20}},
	}
	w := &fakeImportListWriter{rows: rows}
	em := &fakeImportListEmitter{}
	body := ImportListBulkResource{IDs: []int{1, 2}, Tags: []int{30}, ApplyTags: applyTagsAdd}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListBulkUpdateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/importlist/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if !equalIntSet(w.rows[0].Tags, []int{10, 30}) || !equalIntSet(w.rows[1].Tags, []int{20, 30}) {
		t.Errorf("tags after Add: row0=%v row1=%v", w.rows[0].Tags, w.rows[1].Tags)
	}
	if len(em.updated) != 2 {
		t.Errorf("emitter Updated count: %d", len(em.updated))
	}
}

func TestImportListBulkUpdateHandler_TagsRemoveAndReplace(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		{ID: 1, Implementation: "SonarrImport", Tags: []int{10, 20, 30}},
	}
	w := &fakeImportListWriter{rows: rows}
	body := ImportListBulkResource{IDs: []int{1}, Tags: []int{20}, ApplyTags: applyTagsRemove}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListBulkUpdateHandler(w, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/importlist/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if !equalIntSet(w.rows[0].Tags, []int{10, 30}) {
		t.Errorf("after Remove: %v", w.rows[0].Tags)
	}
	// Replace overwrites.
	body = ImportListBulkResource{IDs: []int{1}, Tags: []int{99}, ApplyTags: applyTagsReplace}
	buf, _ = json.Marshal(body)
	rr = httptest.NewRecorder()
	ImportListBulkUpdateHandler(w, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/importlist/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if !equalIntSet(w.rows[0].Tags, []int{99}) {
		t.Errorf("after Replace: %v", w.rows[0].Tags)
	}
}

func TestImportListBulkUpdateHandler_PerFieldNilSkip(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{{
		ID:                 1,
		Implementation:     "SonarrImport",
		EnableAutomaticAdd: boolPtr(true),
		RootFolderPath:     "/old",
		QualityProfileID:   10,
	}}
	w := &fakeImportListWriter{rows: rows}
	// Only QualityProfileID is updated; other fields preserved.
	body := ImportListBulkResource{IDs: []int{1}, QualityProfileID: intPtr(42)}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListBulkUpdateHandler(w, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/importlist/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].QualityProfileID != 42 {
		t.Errorf("QualityProfileID not updated: %d", w.rows[0].QualityProfileID)
	}
	if w.rows[0].RootFolderPath != "/old" {
		t.Errorf("RootFolderPath should be preserved: %q", w.rows[0].RootFolderPath)
	}
	if w.rows[0].EnableAutomaticAdd == nil || *w.rows[0].EnableAutomaticAdd != true {
		t.Errorf("EnableAutomaticAdd should be preserved: %v", w.rows[0].EnableAutomaticAdd)
	}
}

func TestImportListBulkUpdateHandler_RootFolderPathStringPtr(t *testing.T) {
	t.Parallel()
	// Explicit RootFolderPath supplied → overwrites; even empty string overwrites.
	rows := []importlists.ImportList{{ID: 1, Implementation: "SonarrImport", RootFolderPath: "/old"}}
	w := &fakeImportListWriter{rows: rows}
	body := ImportListBulkResource{IDs: []int{1}, RootFolderPath: stringPtr("/new")}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListBulkUpdateHandler(w, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/importlist/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].RootFolderPath != "/new" {
		t.Errorf("RootFolderPath: %q", w.rows[0].RootFolderPath)
	}
}

func TestImportListBulkUpdateHandler_ApplyTagsAsString(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{{ID: 1, Implementation: "SonarrImport", Tags: []int{10, 20}}}
	w := &fakeImportListWriter{rows: rows}
	body := []byte(`{"ids":[1],"tags":[10],"applyTags":"Remove"}`)
	rr := httptest.NewRecorder()
	ImportListBulkUpdateHandler(w, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/importlist/bulk", bytes.NewReader(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if !equalIntSet(w.rows[0].Tags, []int{20}) {
		t.Errorf("after string-form Remove: %v", w.rows[0].Tags)
	}
}

func TestImportListBulkUpdateHandler_EmptyIDs_400(t *testing.T) {
	t.Parallel()
	body := ImportListBulkResource{IDs: nil}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListBulkUpdateHandler(&fakeImportListWriter{}, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/importlist/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListBulkUpdateHandler_MissingIdsSilentlySkipped(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{{ID: 1, Implementation: "SonarrImport"}}
	w := &fakeImportListWriter{rows: rows}
	em := &fakeImportListEmitter{}
	body := ImportListBulkResource{IDs: []int{1, 999}, QualityProfileID: intPtr(7)}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListBulkUpdateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/importlist/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].QualityProfileID != 7 {
		t.Errorf("existing row not updated: %+v", w.rows[0])
	}
	if len(em.updated) != 1 {
		t.Errorf("Updated should fire only once: %d", len(em.updated))
	}
}

func TestImportListBulkDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		{ID: 1, Implementation: "SonarrImport"},
		{ID: 2, Implementation: "SonarrImport"},
		{ID: 3, Implementation: "SonarrImport"},
	}
	w := &fakeImportListWriter{rows: rows}
	em := &fakeImportListEmitter{}
	body := ImportListBulkResource{IDs: []int{1, 3}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListBulkDeleteHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/importlist/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].ID != 2 {
		t.Errorf("rows after bulk delete: %+v", w.rows)
	}
	if len(em.deleted) != 2 {
		t.Errorf("Deleted should fire per id: %d", len(em.deleted))
	}
}

func TestImportListBulkDeleteHandler_EmptyIDs_400(t *testing.T) {
	t.Parallel()
	body := ImportListBulkResource{IDs: nil}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	ImportListBulkDeleteHandler(&fakeImportListWriter{}, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/importlist/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListBulkUpdateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListBulkUpdateHandler(&fakeImportListWriter{}, &fakeImportListEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/importlist/bulk", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
