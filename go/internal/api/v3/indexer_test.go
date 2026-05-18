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

	"sonarr.tv/sonarr/internal/indexers"
)

type fakeIndexerLister struct {
	rows []indexers.Indexer
	err  error
}

func (f *fakeIndexerLister) All(context.Context) ([]indexers.Indexer, error) {
	return f.rows, f.err
}

func TestIndexerHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerHandler(&fakeIndexerLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/indexer", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestIndexerHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerHandler(&fakeIndexerLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/indexer", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// fakeIndexerWriter implements IndexerWriter against an in-memory
// table. Same shape as the notification fake — Insert auto-assigns
// ids; Update + Delete match by id with ErrNotFound on miss.
type fakeIndexerWriter struct {
	rows   []indexers.Indexer
	nextID int64
	getErr error
}

func (f *fakeIndexerWriter) Get(_ context.Context, id int) (*indexers.Indexer, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, indexers.ErrNotFound
}
func (f *fakeIndexerWriter) All(_ context.Context) ([]indexers.Indexer, error) {
	out := make([]indexers.Indexer, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
func (f *fakeIndexerWriter) Insert(_ context.Context, n *indexers.Indexer) (*indexers.Indexer, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}
func (f *fakeIndexerWriter) Update(_ context.Context, n *indexers.Indexer) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return indexers.ErrNotFound
}
func (f *fakeIndexerWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return indexers.ErrNotFound
}

type fakeIndexerEmitter struct {
	created []IndexerResource
	updated []IndexerResource
	deleted []IndexerResource
}

func (f *fakeIndexerEmitter) Created(_ context.Context, r IndexerResource) error {
	f.created = append(f.created, r)
	return nil
}
func (f *fakeIndexerEmitter) Updated(_ context.Context, r IndexerResource) error {
	f.updated = append(f.updated, r)
	return nil
}
func (f *fakeIndexerEmitter) Deleted(_ context.Context, r IndexerResource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

func validIndexerBody() IndexerResource {
	return IndexerResource{
		providerResourceCore: providerResourceCore{
			Name:               "Nyaa",
			Implementation:     "Newznab",
			ImplementationName: "Newznab",
			ConfigContract:     "NewznabSettings",
			Tags:               []int{},
		},
		EnableRss:                           true,
		EnableAutomaticSearch:               true,
		EnableInteractiveSearch:             true,
		Priority:                            25,
		DownloadClientID:                    0,
		SeasonSearchMaximumSingleEpisodeAge: 0,
		Fields: []ProviderField{
			{Name: "baseUrl", Value: "https://nyaa.example/api"},
			{Name: "apiKey", Value: "deadbeef"},
		},
	}
}

func TestIndexerCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeIndexerWriter{}
	em := &fakeIndexerEmitter{}
	body := validIndexerBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/indexer", bytes.NewReader(buf))
	rr := httptest.NewRecorder()
	IndexerCreateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].Name != "Nyaa" || w.rows[0].ID <= 0 {
		t.Errorf("row not inserted: %+v", w.rows)
	}
	if w.rows[0].Settings == nil || *w.rows[0].Settings == "" || *w.rows[0].Settings == "{}" {
		t.Errorf("settings should pack fields[]; got %v", w.rows[0].Settings)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(*w.rows[0].Settings), &settings); err != nil {
		t.Fatalf("settings JSON: %v", err)
	}
	if settings["baseUrl"] != "https://nyaa.example/api" || settings["apiKey"] != "deadbeef" {
		t.Errorf("settings packing: %+v", settings)
	}
	if len(em.created) != 1 || em.created[0].Name != "Nyaa" {
		t.Errorf("emitter.Created not fired: %+v", em.created)
	}
}

func TestIndexerCreateHandler_NameMissing_400(t *testing.T) {
	t.Parallel()
	body := validIndexerBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	IndexerCreateHandler(&fakeIndexerWriter{}, &fakeIndexerEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestIndexerCreateHandler_ImplementationMissing_400(t *testing.T) {
	t.Parallel()
	body := validIndexerBody()
	body.Implementation = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	IndexerCreateHandler(&fakeIndexerWriter{}, &fakeIndexerEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerCreateHandler_DuplicateName_400(t *testing.T) {
	t.Parallel()
	body := validIndexerBody()
	w := &fakeIndexerWriter{rows: []indexers.Indexer{{ID: 5, Name: "nyaa"}}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	IndexerCreateHandler(w, &fakeIndexerEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestIndexerCreateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerCreateHandler(&fakeIndexerWriter{}, &fakeIndexerEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	existingSettings := `{"baseUrl":"old"}`
	existing := indexers.Indexer{ID: 42, Name: "old", Implementation: "Newznab", Settings: &existingSettings}
	w := &fakeIndexerWriter{rows: []indexers.Indexer{existing}}
	em := &fakeIndexerEmitter{}
	body := validIndexerBody()
	body.ID = 42
	body.Name = "Renamed"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/indexer/42", bytes.NewReader(buf))
	req.SetPathValue("id", "42")
	rr := httptest.NewRecorder()
	IndexerUpdateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Name != "Renamed" {
		t.Errorf("row not updated: %+v", w.rows[0])
	}
	if len(em.updated) != 1 {
		t.Errorf("emitter.Updated not fired: %+v", em.updated)
	}
}

func TestIndexerUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	body := validIndexerBody()
	body.ID = 999
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/indexer/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	IndexerUpdateHandler(&fakeIndexerWriter{}, &fakeIndexerEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerUpdateHandler_BodyIDFallback(t *testing.T) {
	t.Parallel()
	existing := indexers.Indexer{ID: 5, Name: "old", Implementation: "Newznab"}
	w := &fakeIndexerWriter{rows: []indexers.Indexer{existing}}
	body := validIndexerBody()
	body.ID = 5
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/indexer/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	IndexerUpdateHandler(w, &fakeIndexerEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestIndexerUpdateHandler_OmittedFields_KeepsExistingSettings(t *testing.T) {
	t.Parallel()
	existingSettings := `{"baseUrl":"keep-me"}`
	existing := indexers.Indexer{ID: 1, Name: "old", Implementation: "Newznab", Settings: &existingSettings}
	w := &fakeIndexerWriter{rows: []indexers.Indexer{existing}}
	body := validIndexerBody()
	body.ID = 1
	body.Fields = nil
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/indexer/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	IndexerUpdateHandler(w, &fakeIndexerEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Settings == nil || *w.rows[0].Settings != `{"baseUrl":"keep-me"}` {
		t.Errorf("settings should be preserved when fields omitted; got %v", w.rows[0].Settings)
	}
}

// fakeIndexerTester implements IndexerTester. seen records the last
// Indexer it was asked to test so the handler-side fromResource
// projection can be verified.
type fakeIndexerTester struct {
	seen   *indexers.Indexer
	result []indexers.TestFailure
	err    error
}

func (f *fakeIndexerTester) Test(_ context.Context, n *indexers.Indexer) ([]indexers.TestFailure, error) {
	f.seen = n
	return f.result, f.err
}

func TestIndexerTestHandler_Success_Returns200EmptyObject(t *testing.T) {
	t.Parallel()
	tester := &fakeIndexerTester{}
	body := validIndexerBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/indexer/test", bytes.NewReader(buf))
	rr := httptest.NewRecorder()
	IndexerTestHandler(tester).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q want {}", rr.Body.String())
	}
	if tester.seen == nil || tester.seen.Implementation != "Newznab" {
		t.Errorf("Test should be invoked with built Indexer: seen=%+v", tester.seen)
	}
	if tester.seen.Settings == nil {
		t.Fatalf("Settings nil after fromResource")
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(*tester.seen.Settings), &settings); err != nil {
		t.Fatalf("Settings JSON: %v", err)
	}
	if settings["baseUrl"] != "https://nyaa.example/api" {
		t.Errorf("settings baseUrl not packed: %v", settings["baseUrl"])
	}
}

func TestIndexerTestHandler_FailuresProjectTo400Array(t *testing.T) {
	t.Parallel()
	tester := &fakeIndexerTester{result: []indexers.TestFailure{
		{PropertyName: "BaseUrl", ErrorMessage: "could not connect"},
		{PropertyName: "", ErrorMessage: "Test failed: timeout"},
	}}
	body := validIndexerBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	IndexerTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var failures []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &failures); err != nil {
		t.Fatalf("body must be FluentValidation array: %v body=%s", err, rr.Body.String())
	}
	if len(failures) != 2 {
		t.Fatalf("len: %d", len(failures))
	}
	if failures[0]["propertyName"] != "BaseUrl" || failures[0]["errorMessage"] != "could not connect" {
		t.Errorf("first: %+v", failures[0])
	}
	if failures[0]["severity"] != "error" {
		t.Errorf("severity must default to error: %+v", failures[0])
	}
}

func TestIndexerTestHandler_UnknownImplementation_400(t *testing.T) {
	t.Parallel()
	tester := &fakeIndexerTester{err: indexers.ErrNotRegistered}
	body := validIndexerBody()
	body.Implementation = "GhostProvider"
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	IndexerTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	var failures []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &failures); err != nil {
		t.Fatalf("body shape: %v", err)
	}
	if len(failures) != 1 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected single Implementation failure: %+v", failures)
	}
}

func TestIndexerTestHandler_OtherError_500(t *testing.T) {
	t.Parallel()
	tester := &fakeIndexerTester{err: errors.New("transport broke")}
	body := validIndexerBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	IndexerTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerTestHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerTestHandler(&fakeIndexerTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/test", bytes.NewReader([]byte(`{not json`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerTestHandler_SkipsSharedValidator(t *testing.T) {
	t.Parallel()
	tester := &fakeIndexerTester{}
	body := validIndexerBody()
	body.Name = "" // would be 400 on POST/PUT
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	IndexerTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Errorf("skip-validation: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// fakeIndexerActioner implements IndexerActioner.
type fakeIndexerActioner struct {
	seenName  string
	seenQuery map[string]string
	seenRow   *indexers.Indexer
	result    any
	err       error
}

func (f *fakeIndexerActioner) RequestAction(_ context.Context, n *indexers.Indexer, name string, query map[string]string) (any, error) {
	f.seenName = name
	f.seenQuery = query
	f.seenRow = n
	return f.result, f.err
}

func TestIndexerRequestActionHandler_Success(t *testing.T) {
	t.Parallel()
	actioner := &fakeIndexerActioner{result: map[string]any{"categories": []any{float64(5000), float64(5070)}}}
	body := validIndexerBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/indexer/action/getCategories?type=tv", bytes.NewReader(buf))
	req.SetPathValue("name", "getCategories")
	rr := httptest.NewRecorder()
	IndexerRequestActionHandler(actioner).ServeHTTP(rr, req)
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
	if actioner.seenRow == nil || actioner.seenRow.Implementation != "Newznab" {
		t.Errorf("row not built: %+v", actioner.seenRow)
	}
}

func TestIndexerRequestActionHandler_NilResult_EmitsJSONNull(t *testing.T) {
	t.Parallel()
	actioner := &fakeIndexerActioner{result: nil}
	body := validIndexerBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/indexer/action/noop", bytes.NewReader(buf))
	req.SetPathValue("name", "noop")
	rr := httptest.NewRecorder()
	IndexerRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `null` {
		t.Errorf("body should be JSON null; got %q", rr.Body.String())
	}
}

func TestIndexerRequestActionHandler_ErrNotRegistered_400(t *testing.T) {
	t.Parallel()
	actioner := &fakeIndexerActioner{err: indexers.ErrNotRegistered}
	body := validIndexerBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/indexer/action/x", bytes.NewReader(buf))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	IndexerRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	var failures []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &failures); err != nil {
		t.Fatalf("body shape: %v", err)
	}
	if len(failures) != 1 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected single Implementation failure: %+v", failures)
	}
}

func TestIndexerRequestActionHandler_OtherError_500(t *testing.T) {
	t.Parallel()
	actioner := &fakeIndexerActioner{err: errors.New("upstream down")}
	body := validIndexerBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/indexer/action/x", bytes.NewReader(buf))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	IndexerRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerRequestActionHandler_EmptyName_400(t *testing.T) {
	t.Parallel()
	body := validIndexerBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/indexer/action/", bytes.NewReader(buf))
	req.SetPathValue("name", "")
	rr := httptest.NewRecorder()
	IndexerRequestActionHandler(&fakeIndexerActioner{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerRequestActionHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/indexer/action/x", bytes.NewReader([]byte(`{nope`)))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	IndexerRequestActionHandler(&fakeIndexerActioner{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// perRowIndexerTester maps row ID → outcome.
type perRowIndexerTester struct {
	seen     []int
	byID     map[int][]indexers.TestFailure
	errByID  map[int]error
	defaultR []indexers.TestFailure
}

func (p *perRowIndexerTester) Test(_ context.Context, n *indexers.Indexer) ([]indexers.TestFailure, error) {
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

func TestIndexerTestAllHandler_Empty_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerTestAllHandler(&fakeIndexerLister{}, &perRowIndexerTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `[]` {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestIndexerTestAllHandler_AllPass_Returns200Array(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		{ID: 1, Name: "a", Implementation: "Newznab"},
		{ID: 2, Name: "b", Implementation: "Torznab"},
	}
	tester := &perRowIndexerTester{}
	rr := httptest.NewRecorder()
	IndexerTestAllHandler(&fakeIndexerLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/testall", nil))
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

func TestIndexerTestAllHandler_MixedResults_Returns400(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		{ID: 1, Name: "ok", Implementation: "Newznab"},
		{ID: 2, Name: "bad", Implementation: "Torznab"},
		{ID: 3, Name: "missing", Implementation: "GhostProvider"},
		{ID: 4, Name: "boom", Implementation: "Newznab"},
	}
	tester := &perRowIndexerTester{
		byID: map[int][]indexers.TestFailure{
			2: {{PropertyName: "BaseUrl", ErrorMessage: "could not connect"}},
		},
		errByID: map[int]error{
			3: indexers.ErrNotRegistered,
			4: errors.New("transport reset"),
		},
	}
	rr := httptest.NewRecorder()
	IndexerTestAllHandler(&fakeIndexerLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/testall", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got []ProviderTestAllResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len: %d", len(got))
	}
	if !got[0].IsValid || len(got[0].ValidationFailures) != 0 {
		t.Errorf("row 0: %+v", got[0])
	}
	if got[1].IsValid || len(got[1].ValidationFailures) != 1 || got[1].ValidationFailures[0].PropertyName != "BaseUrl" {
		t.Errorf("row 1: %+v", got[1])
	}
	if got[2].IsValid || got[2].ValidationFailures[0].PropertyName != "Implementation" {
		t.Errorf("row 2: %+v", got[2])
	}
	if got[3].IsValid || got[3].ValidationFailures[0].PropertyName != "" {
		t.Errorf("row 3: %+v", got[3])
	}
}

func TestIndexerTestAllHandler_ListError_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerTestAllHandler(&fakeIndexerLister{err: errors.New("db down")}, &perRowIndexerTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/testall", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerTestAllHandler_ValidationFailuresNonNil(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{{ID: 7, Implementation: "Newznab"}}
	rr := httptest.NewRecorder()
	IndexerTestAllHandler(&fakeIndexerLister{rows: rows}, &perRowIndexerTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/indexer/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"validationFailures":[]`)) {
		t.Errorf("validationFailures should be [] not null: body=%s", rr.Body.String())
	}
}

func TestIndexerDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeIndexerWriter{rows: []indexers.Indexer{{ID: 7, Name: "victim"}}}
	em := &fakeIndexerEmitter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/indexer/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	IndexerDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.rows) != 0 {
		t.Errorf("row should be deleted: %+v", w.rows)
	}
	if len(em.deleted) != 1 || em.deleted[0].ID != 7 {
		t.Errorf("emitter.Deleted not fired with id 7: %+v", em.deleted)
	}
}

func TestIndexerDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/indexer/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	IndexerDeleteHandler(&fakeIndexerWriter{}, &fakeIndexerEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodDelete, "/api/v3/indexer/"+raw, nil)
			req.SetPathValue("id", raw)
			rr := httptest.NewRecorder()
			IndexerDeleteHandler(&fakeIndexerWriter{}, &fakeIndexerEmitter{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: %d (id=%q)", rr.Code, raw)
			}
		})
	}
}
