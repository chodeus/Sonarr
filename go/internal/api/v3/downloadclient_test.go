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

	"sonarr.tv/sonarr/internal/downloadclients"
)

type fakeDownloadClientLister struct {
	rows []downloadclients.DownloadClient
	err  error
}

func (f *fakeDownloadClientLister) All(context.Context) ([]downloadclients.DownloadClient, error) {
	return f.rows, f.err
}

func TestDownloadClientHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientHandler(&fakeDownloadClientLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestDownloadClientHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientHandler(&fakeDownloadClientLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// fakeDownloadClientWriter implements DownloadClientWriter.
type fakeDownloadClientWriter struct {
	rows   []downloadclients.DownloadClient
	nextID int64
	getErr error
}

func (f *fakeDownloadClientWriter) Get(_ context.Context, id int) (*downloadclients.DownloadClient, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, downloadclients.ErrNotFound
}
func (f *fakeDownloadClientWriter) All(_ context.Context) ([]downloadclients.DownloadClient, error) {
	out := make([]downloadclients.DownloadClient, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
func (f *fakeDownloadClientWriter) Insert(_ context.Context, n *downloadclients.DownloadClient) (*downloadclients.DownloadClient, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}
func (f *fakeDownloadClientWriter) Update(_ context.Context, n *downloadclients.DownloadClient) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return downloadclients.ErrNotFound
}
func (f *fakeDownloadClientWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return downloadclients.ErrNotFound
}

type fakeDownloadClientEmitter struct {
	created []DownloadClientResource
	updated []DownloadClientResource
	deleted []DownloadClientResource
}

func (f *fakeDownloadClientEmitter) Created(_ context.Context, r DownloadClientResource) error {
	f.created = append(f.created, r)
	return nil
}
func (f *fakeDownloadClientEmitter) Updated(_ context.Context, r DownloadClientResource) error {
	f.updated = append(f.updated, r)
	return nil
}
func (f *fakeDownloadClientEmitter) Deleted(_ context.Context, r DownloadClientResource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

func validDownloadClientBody() DownloadClientResource {
	return DownloadClientResource{
		providerResourceCore: providerResourceCore{
			Name:               "MySab",
			Implementation:     "Sabnzbd",
			ImplementationName: "Sabnzbd",
			ConfigContract:     "SabnzbdSettings",
			Tags:               []int{},
		},
		Enable:                   true,
		Priority:                 25,
		RemoveCompletedDownloads: true,
		Fields: []ProviderField{
			{Name: "host", Value: "sab.example"},
			{Name: "apiKey", Value: "deadbeef"},
		},
	}
}

func TestDownloadClientCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeDownloadClientWriter{}
	em := &fakeDownloadClientEmitter{}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientCreateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient", bytes.NewReader(buf)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].Name != "MySab" || w.rows[0].ID <= 0 {
		t.Errorf("row not inserted: %+v", w.rows)
	}
	if w.rows[0].Settings == "" || w.rows[0].Settings == "{}" {
		t.Errorf("settings should pack fields[]; got %q", w.rows[0].Settings)
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(w.rows[0].Settings), &settings)
	if settings["host"] != "sab.example" {
		t.Errorf("settings packing: %+v", settings)
	}
	if len(em.created) != 1 {
		t.Errorf("emitter.Created not fired: %+v", em.created)
	}
}

func TestDownloadClientCreateHandler_NameMissing_400(t *testing.T) {
	t.Parallel()
	body := validDownloadClientBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientCreateHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientCreateHandler_DuplicateName_400(t *testing.T) {
	t.Parallel()
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{{ID: 9, Name: "mysab"}}}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientCreateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDownloadClientCreateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientCreateHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	existing := downloadclients.DownloadClient{ID: 42, Name: "old", Implementation: "Sabnzbd", Settings: `{"host":"old"}`}
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{existing}}
	em := &fakeDownloadClientEmitter{}
	body := validDownloadClientBody()
	body.ID = 42
	body.Name = "renamed"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/42", bytes.NewReader(buf))
	req.SetPathValue("id", "42")
	rr := httptest.NewRecorder()
	DownloadClientUpdateHandler(w, em).ServeHTTP(rr, req)
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

func TestDownloadClientUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	body := validDownloadClientBody()
	body.ID = 999
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	DownloadClientUpdateHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientUpdateHandler_BodyIDFallback(t *testing.T) {
	t.Parallel()
	existing := downloadclients.DownloadClient{ID: 5, Name: "old", Implementation: "Sabnzbd"}
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{existing}}
	body := validDownloadClientBody()
	body.ID = 5
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	DownloadClientUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDownloadClientUpdateHandler_OmittedFields_KeepsSettings(t *testing.T) {
	t.Parallel()
	existing := downloadclients.DownloadClient{ID: 1, Name: "old", Implementation: "Sabnzbd", Settings: `{"host":"keep"}`}
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{existing}}
	body := validDownloadClientBody()
	body.ID = 1
	body.Fields = nil
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	DownloadClientUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Settings != `{"host":"keep"}` {
		t.Errorf("settings should be preserved: %q", w.rows[0].Settings)
	}
}

func TestDownloadClientDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{{ID: 7, Name: "victim"}}}
	em := &fakeDownloadClientEmitter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/downloadclient/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	DownloadClientDeleteHandler(w, em).ServeHTTP(rr, req)
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

// TestDownloadClientDeleteHandler_Idempotent — C.1.i-follow-4 (J232). 200 + `{}` on missing id.
func TestDownloadClientDeleteHandler_Idempotent(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/downloadclient/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	em := &fakeDownloadClientEmitter{}
	DownloadClientDeleteHandler(&fakeDownloadClientWriter{}, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (idempotent)", rr.Code)
	}
	if len(em.deleted) > 0 {
		t.Errorf("SignalR Deleted broadcast must NOT fire on missing-id swallow")
	}
}

func TestDownloadClientDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodDelete, "/api/v3/downloadclient/"+raw, nil)
			req.SetPathValue("id", raw)
			rr := httptest.NewRecorder()
			DownloadClientDeleteHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: %d (id=%q)", rr.Code, raw)
			}
		})
	}
}

// fakeDownloadClientTester implements DownloadClientTester.
type fakeDownloadClientTester struct {
	seen   *downloadclients.DownloadClient
	result []downloadclients.TestFailure
	err    error
}

func (f *fakeDownloadClientTester) Test(_ context.Context, n *downloadclients.DownloadClient) ([]downloadclients.TestFailure, error) {
	f.seen = n
	return f.result, f.err
}

func TestDownloadClientTestHandler_Success_Returns200EmptyObject(t *testing.T) {
	t.Parallel()
	tester := &fakeDownloadClientTester{}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/test", bytes.NewReader(buf))
	rr := httptest.NewRecorder()
	DownloadClientTestHandler(tester).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if tester.seen == nil || tester.seen.Implementation != "Sabnzbd" {
		t.Errorf("Test should be invoked with built DownloadClient: %+v", tester.seen)
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(tester.seen.Settings), &settings)
	if settings["host"] != "sab.example" {
		t.Errorf("settings packing: %+v", settings)
	}
}

func TestDownloadClientTestHandler_FailuresProjectTo400Array(t *testing.T) {
	t.Parallel()
	tester := &fakeDownloadClientTester{result: []downloadclients.TestFailure{
		{PropertyName: "Host", ErrorMessage: "could not connect"},
		{PropertyName: "", ErrorMessage: "Test failed: timeout"},
	}}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var failures []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &failures); err != nil {
		t.Fatalf("body must be FluentValidation array: %v body=%s", err, rr.Body.String())
	}
	if len(failures) != 2 || failures[0]["propertyName"] != "Host" {
		t.Errorf("failures: %+v", failures)
	}
}

func TestDownloadClientTestHandler_UnknownImplementation_400(t *testing.T) {
	t.Parallel()
	tester := &fakeDownloadClientTester{err: downloadclients.ErrNotRegistered}
	body := validDownloadClientBody()
	body.Implementation = "GhostProvider"
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	var failures []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &failures)
	if len(failures) != 1 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected single Implementation failure: %+v", failures)
	}
}

func TestDownloadClientTestHandler_OtherError_500(t *testing.T) {
	t.Parallel()
	tester := &fakeDownloadClientTester{err: errors.New("upstream timeout")}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientTestHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientTestHandler(&fakeDownloadClientTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/test", bytes.NewReader([]byte(`{not json`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientTestHandler_SkipsSharedValidator(t *testing.T) {
	t.Parallel()
	tester := &fakeDownloadClientTester{}
	body := validDownloadClientBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Errorf("skip-validation: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// perRowDownloadClientTester maps row ID → outcome.
type perRowDownloadClientTester struct {
	seen     []int
	byID     map[int][]downloadclients.TestFailure
	errByID  map[int]error
	defaultR []downloadclients.TestFailure
}

func (p *perRowDownloadClientTester) Test(_ context.Context, n *downloadclients.DownloadClient) ([]downloadclients.TestFailure, error) {
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

func TestDownloadClientTestAllHandler_Empty_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientTestAllHandler(&fakeDownloadClientLister{}, &perRowDownloadClientTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `[]` {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestDownloadClientTestAllHandler_AllPass_Returns200Array(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{
		{ID: 1, Name: "a", Implementation: "Sabnzbd"},
		{ID: 2, Name: "b", Implementation: "Transmission"},
	}
	tester := &perRowDownloadClientTester{}
	rr := httptest.NewRecorder()
	DownloadClientTestAllHandler(&fakeDownloadClientLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []ProviderTestAllResult
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("len: %d", len(got))
	}
	for i, r := range got {
		if !r.IsValid {
			t.Errorf("row %d isValid: false", i)
		}
	}
	if len(tester.seen) != 2 || tester.seen[0] != 1 || tester.seen[1] != 2 {
		t.Errorf("ordering: %v", tester.seen)
	}
}

func TestDownloadClientTestAllHandler_MixedResults_Returns400(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{
		{ID: 1, Name: "ok", Implementation: "Sabnzbd"},
		{ID: 2, Name: "bad", Implementation: "Transmission"},
		{ID: 3, Name: "missing", Implementation: "GhostProvider"},
		{ID: 4, Name: "boom", Implementation: "Sabnzbd"},
	}
	tester := &perRowDownloadClientTester{
		byID: map[int][]downloadclients.TestFailure{
			2: {{PropertyName: "Host", ErrorMessage: "could not connect"}},
		},
		errByID: map[int]error{
			3: downloadclients.ErrNotRegistered,
			4: errors.New("transport reset"),
		},
	}
	rr := httptest.NewRecorder()
	DownloadClientTestAllHandler(&fakeDownloadClientLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/testall", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []ProviderTestAllResult
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 4 {
		t.Fatalf("len: %d", len(got))
	}
	if !got[0].IsValid {
		t.Errorf("row 0: %+v", got[0])
	}
	if got[1].IsValid || got[1].ValidationFailures[0].PropertyName != "Host" {
		t.Errorf("row 1: %+v", got[1])
	}
	if got[2].IsValid || got[2].ValidationFailures[0].PropertyName != "Implementation" {
		t.Errorf("row 2: %+v", got[2])
	}
	if got[3].IsValid || got[3].ValidationFailures[0].PropertyName != "" {
		t.Errorf("row 3: %+v", got[3])
	}
}

func TestDownloadClientTestAllHandler_ListError_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientTestAllHandler(&fakeDownloadClientLister{err: errors.New("db down")}, &perRowDownloadClientTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/testall", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientTestAllHandler_ValidationFailuresNonNil(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{{ID: 7, Implementation: "Sabnzbd"}}
	rr := httptest.NewRecorder()
	DownloadClientTestAllHandler(&fakeDownloadClientLister{rows: rows}, &perRowDownloadClientTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"validationFailures":[]`)) {
		t.Errorf("validationFailures should be [] not null: body=%s", rr.Body.String())
	}
}

// fakeDownloadClientActioner implements DownloadClientActioner.
type fakeDownloadClientActioner struct {
	seenName  string
	seenQuery map[string]string
	seenRow   *downloadclients.DownloadClient
	result    any
	err       error
}

func (f *fakeDownloadClientActioner) RequestAction(_ context.Context, n *downloadclients.DownloadClient, name string, query map[string]string) (any, error) {
	f.seenName = name
	f.seenQuery = query
	f.seenRow = n
	return f.result, f.err
}

func TestDownloadClientRequestActionHandler_Success(t *testing.T) {
	t.Parallel()
	actioner := &fakeDownloadClientActioner{result: map[string]any{"categories": []any{"tv", "movies"}}}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/action/getCategories?type=tv", bytes.NewReader(buf))
	req.SetPathValue("name", "getCategories")
	rr := httptest.NewRecorder()
	DownloadClientRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body shape: %v body=%s", err, rr.Body.String())
	}
	if _, ok := got["categories"]; !ok {
		t.Errorf("body: %+v", got)
	}
	if actioner.seenName != "getCategories" {
		t.Errorf("action name: %q", actioner.seenName)
	}
	if actioner.seenQuery["type"] != "tv" {
		t.Errorf("query map: %+v", actioner.seenQuery)
	}
	if actioner.seenRow == nil || actioner.seenRow.Implementation != "Sabnzbd" {
		t.Errorf("row not built: %+v", actioner.seenRow)
	}
}

func TestDownloadClientRequestActionHandler_NilResult_EmitsJSONNull(t *testing.T) {
	t.Parallel()
	actioner := &fakeDownloadClientActioner{result: nil}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/action/noop", bytes.NewReader(buf))
	req.SetPathValue("name", "noop")
	rr := httptest.NewRecorder()
	DownloadClientRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `null` {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestDownloadClientRequestActionHandler_ErrNotRegistered_400(t *testing.T) {
	t.Parallel()
	actioner := &fakeDownloadClientActioner{err: downloadclients.ErrNotRegistered}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/action/x", bytes.NewReader(buf))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	DownloadClientRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	var failures []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &failures)
	if len(failures) != 1 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected Implementation failure: %+v", failures)
	}
}

func TestDownloadClientRequestActionHandler_OtherError_500(t *testing.T) {
	t.Parallel()
	actioner := &fakeDownloadClientActioner{err: errors.New("upstream down")}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/action/x", bytes.NewReader(buf))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	DownloadClientRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientRequestActionHandler_EmptyName_400(t *testing.T) {
	t.Parallel()
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/action/", bytes.NewReader(buf))
	req.SetPathValue("name", "")
	rr := httptest.NewRecorder()
	DownloadClientRequestActionHandler(&fakeDownloadClientActioner{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientRequestActionHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient/action/x", bytes.NewReader([]byte(`{nope`)))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	DownloadClientRequestActionHandler(&fakeDownloadClientActioner{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientBulkUpdateHandler_TagsAddRemoveReplace(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{
		{ID: 1, Implementation: "Sabnzbd", Tags: []int{10, 20}},
	}
	w := &fakeDownloadClientWriter{rows: rows}
	// Add 30
	body := DownloadClientBulkResource{IDs: []int{1}, Tags: []int{30}, ApplyTags: applyTagsAdd}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientBulkUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("Add status: %d", rr.Code)
	}
	if !equalIntSet(w.rows[0].Tags, []int{10, 20, 30}) {
		t.Errorf("after Add: %v", w.rows[0].Tags)
	}
	// Remove 20
	body = DownloadClientBulkResource{IDs: []int{1}, Tags: []int{20}, ApplyTags: applyTagsRemove}
	buf, _ = json.Marshal(body)
	rr = httptest.NewRecorder()
	DownloadClientBulkUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/bulk", bytes.NewReader(buf)))
	if !equalIntSet(w.rows[0].Tags, []int{10, 30}) {
		t.Errorf("after Remove: %v", w.rows[0].Tags)
	}
	// Replace with [99]
	body = DownloadClientBulkResource{IDs: []int{1}, Tags: []int{99}, ApplyTags: applyTagsReplace}
	buf, _ = json.Marshal(body)
	rr = httptest.NewRecorder()
	DownloadClientBulkUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/bulk", bytes.NewReader(buf)))
	if !equalIntSet(w.rows[0].Tags, []int{99}) {
		t.Errorf("after Replace: %v", w.rows[0].Tags)
	}
}

func TestDownloadClientBulkUpdateHandler_PerFieldNilSkip(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{{
		ID:                       1,
		Implementation:           "Sabnzbd",
		Enable:                   true,
		Priority:                 10,
		RemoveCompletedDownloads: true,
		RemoveFailedDownloads:    false,
	}}
	w := &fakeDownloadClientWriter{rows: rows}
	// Only Priority updated; everything else preserved.
	body := DownloadClientBulkResource{IDs: []int{1}, Priority: intPtr(42)}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientBulkUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Priority != 42 {
		t.Errorf("Priority: %d", w.rows[0].Priority)
	}
	if !w.rows[0].Enable {
		t.Errorf("Enable should be preserved")
	}
	if !w.rows[0].RemoveCompletedDownloads {
		t.Errorf("RemoveCompletedDownloads should be preserved")
	}
	if w.rows[0].RemoveFailedDownloads {
		t.Errorf("RemoveFailedDownloads should be preserved")
	}
}

func TestDownloadClientBulkUpdateHandler_AllBoolFieldsToggleable(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{{
		ID:                       1,
		Implementation:           "Sabnzbd",
		Enable:                   true,
		RemoveCompletedDownloads: true,
		RemoveFailedDownloads:    false,
	}}
	w := &fakeDownloadClientWriter{rows: rows}
	enable := false
	removeCompleted := false
	removeFailed := true
	body := DownloadClientBulkResource{
		IDs:                      []int{1},
		Enable:                   &enable,
		RemoveCompletedDownloads: &removeCompleted,
		RemoveFailedDownloads:    &removeFailed,
	}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientBulkUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Enable || w.rows[0].RemoveCompletedDownloads || !w.rows[0].RemoveFailedDownloads {
		t.Errorf("bool fields not toggled: %+v", w.rows[0])
	}
}

func TestDownloadClientBulkUpdateHandler_ApplyTagsAsString(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{{ID: 1, Implementation: "Sabnzbd", Tags: []int{10, 20}}}
	w := &fakeDownloadClientWriter{rows: rows}
	body := []byte(`{"ids":[1],"tags":[10],"applyTags":"Remove"}`)
	rr := httptest.NewRecorder()
	DownloadClientBulkUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/bulk", bytes.NewReader(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if !equalIntSet(w.rows[0].Tags, []int{20}) {
		t.Errorf("after string-form Remove: %v", w.rows[0].Tags)
	}
}

func TestDownloadClientBulkUpdateHandler_EmptyIDs_400(t *testing.T) {
	t.Parallel()
	body := DownloadClientBulkResource{IDs: nil}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientBulkUpdateHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientBulkUpdateHandler_MissingIdsSilentlySkipped(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{{ID: 1, Implementation: "Sabnzbd"}}
	w := &fakeDownloadClientWriter{rows: rows}
	em := &fakeDownloadClientEmitter{}
	body := DownloadClientBulkResource{IDs: []int{1, 999}, Priority: intPtr(7)}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientBulkUpdateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Priority != 7 {
		t.Errorf("existing not updated: %+v", w.rows[0])
	}
	if len(em.updated) != 1 {
		t.Errorf("Updated count: %d", len(em.updated))
	}
}

func TestDownloadClientBulkDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{
		{ID: 1, Implementation: "Sabnzbd"},
		{ID: 2, Implementation: "Sabnzbd"},
		{ID: 3, Implementation: "Sabnzbd"},
	}
	w := &fakeDownloadClientWriter{rows: rows}
	em := &fakeDownloadClientEmitter{}
	body := DownloadClientBulkResource{IDs: []int{1, 3}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientBulkDeleteHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/downloadclient/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].ID != 2 {
		t.Errorf("rows after bulk delete: %+v", w.rows)
	}
	if len(em.deleted) != 2 {
		t.Errorf("Deleted: %d", len(em.deleted))
	}
}

func TestDownloadClientBulkDeleteHandler_EmptyIDs_400(t *testing.T) {
	t.Parallel()
	body := DownloadClientBulkResource{IDs: nil}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientBulkDeleteHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/downloadclient/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientBulkUpdateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientBulkUpdateHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/bulk", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
