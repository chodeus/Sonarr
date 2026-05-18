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

	"sonarr.tv/sonarr/internal/metadata"
)

// fakeMetadataWriter implements MetadataWriter.
type fakeMetadataWriter struct {
	rows   []metadata.Metadata
	nextID int64
	getErr error
}

func (f *fakeMetadataWriter) Get(_ context.Context, id int) (*metadata.Metadata, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, metadata.ErrNotFound
}
func (f *fakeMetadataWriter) All(_ context.Context) ([]metadata.Metadata, error) {
	out := make([]metadata.Metadata, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
func (f *fakeMetadataWriter) Insert(_ context.Context, n *metadata.Metadata) (*metadata.Metadata, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}
func (f *fakeMetadataWriter) Update(_ context.Context, n *metadata.Metadata) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return metadata.ErrNotFound
}
func (f *fakeMetadataWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return metadata.ErrNotFound
}

type fakeMetadataEmitter struct {
	created []MetadataResource
	updated []MetadataResource
	deleted []MetadataResource
}

func (f *fakeMetadataEmitter) Created(_ context.Context, r MetadataResource) error {
	f.created = append(f.created, r)
	return nil
}
func (f *fakeMetadataEmitter) Updated(_ context.Context, r MetadataResource) error {
	f.updated = append(f.updated, r)
	return nil
}
func (f *fakeMetadataEmitter) Deleted(_ context.Context, r MetadataResource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

func validMetadataBody() MetadataResource {
	return MetadataResource{
		Name:               "Kodi",
		Implementation:     "XbmcMetadata",
		ImplementationName: "Kodi (XBMC) / Emby",
		ConfigContract:     "XbmcMetadataSettings",
		Tags:               []int{},
		Enable:             true,
		Fields: []ProviderField{
			{Name: "seriesMetadata", Value: true},
			{Name: "episodeMetadata", Value: false},
		},
	}
}

func TestMetadataCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeMetadataWriter{}
	em := &fakeMetadataEmitter{}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataCreateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader(buf)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].Name != "Kodi" || w.rows[0].ID <= 0 {
		t.Errorf("row not inserted: %+v", w.rows)
	}
	if w.rows[0].Settings == "" || w.rows[0].Settings == "{}" {
		t.Errorf("settings should pack fields[]; got %q", w.rows[0].Settings)
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(w.rows[0].Settings), &settings)
	if settings["seriesMetadata"] != true {
		t.Errorf("settings packing: %+v", settings)
	}
	if !w.rows[0].Enable {
		t.Errorf("Enable not persisted")
	}
	if len(em.created) != 1 || em.created[0].Name != "Kodi" {
		t.Errorf("emitter.Created: %+v", em.created)
	}
}

func TestMetadataCreateHandler_NameMissing_400(t *testing.T) {
	t.Parallel()
	body := validMetadataBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataCreateHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataCreateHandler_ImplementationMissing_400(t *testing.T) {
	t.Parallel()
	body := validMetadataBody()
	body.Implementation = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataCreateHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataCreateHandler_DuplicateName_400(t *testing.T) {
	t.Parallel()
	w := &fakeMetadataWriter{rows: []metadata.Metadata{{ID: 9, Name: "kodi"}}}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataCreateHandler(w, &fakeMetadataEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMetadataCreateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	MetadataCreateHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	existing := metadata.Metadata{ID: 42, Name: "old", Implementation: "XbmcMetadata", Settings: `{"seriesMetadata":false}`}
	w := &fakeMetadataWriter{rows: []metadata.Metadata{existing}}
	em := &fakeMetadataEmitter{}
	body := validMetadataBody()
	body.ID = 42
	body.Name = "renamed"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/metadata/42", bytes.NewReader(buf))
	req.SetPathValue("id", "42")
	rr := httptest.NewRecorder()
	MetadataUpdateHandler(w, em).ServeHTTP(rr, req)
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

func TestMetadataUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	body := validMetadataBody()
	body.ID = 999
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/metadata/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	MetadataUpdateHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataUpdateHandler_BodyIDFallback(t *testing.T) {
	t.Parallel()
	existing := metadata.Metadata{ID: 5, Name: "old", Implementation: "XbmcMetadata"}
	w := &fakeMetadataWriter{rows: []metadata.Metadata{existing}}
	body := validMetadataBody()
	body.ID = 5
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/metadata/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	MetadataUpdateHandler(w, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMetadataUpdateHandler_OmittedFields_KeepsSettings(t *testing.T) {
	t.Parallel()
	existing := metadata.Metadata{ID: 1, Name: "old", Implementation: "XbmcMetadata", Settings: `{"seriesMetadata":true}`}
	w := &fakeMetadataWriter{rows: []metadata.Metadata{existing}}
	body := validMetadataBody()
	body.ID = 1
	body.Fields = nil
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/metadata/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	MetadataUpdateHandler(w, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Settings != `{"seriesMetadata":true}` {
		t.Errorf("settings should be preserved: %q", w.rows[0].Settings)
	}
}

func TestMetadataDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeMetadataWriter{rows: []metadata.Metadata{{ID: 7, Name: "victim"}}}
	em := &fakeMetadataEmitter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/metadata/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	MetadataDeleteHandler(w, em).ServeHTTP(rr, req)
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

func TestMetadataDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/metadata/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	MetadataDeleteHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodDelete, "/api/v3/metadata/"+raw, nil)
			req.SetPathValue("id", raw)
			rr := httptest.NewRecorder()
			MetadataDeleteHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: %d (id=%q)", rr.Code, raw)
			}
		})
	}
}

// Sanity check: errors.Is wiring for metadata.ErrNotFound — keeps
// the import non-trivial when no tests cover it.
var _ = errors.Is(metadata.ErrNotFound, metadata.ErrNotFound)

// fakeMetadataTester implements MetadataTester.
type fakeMetadataTester struct {
	seen   *metadata.Metadata
	result []metadata.TestFailure
	err    error
}

func (f *fakeMetadataTester) Test(_ context.Context, n *metadata.Metadata) ([]metadata.TestFailure, error) {
	f.seen = n
	return f.result, f.err
}

func TestMetadataTestHandler_Success_Returns200EmptyObject(t *testing.T) {
	t.Parallel()
	tester := &fakeMetadataTester{}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/metadata/test", bytes.NewReader(buf))
	rr := httptest.NewRecorder()
	MetadataTestHandler(tester).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if tester.seen == nil || tester.seen.Implementation != "XbmcMetadata" {
		t.Errorf("Test should be invoked with built Metadata: %+v", tester.seen)
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(tester.seen.Settings), &settings)
	if settings["seriesMetadata"] != true {
		t.Errorf("settings packing: %+v", settings)
	}
}

func TestMetadataTestHandler_FailuresProjectTo400Array(t *testing.T) {
	t.Parallel()
	tester := &fakeMetadataTester{result: []metadata.TestFailure{
		{PropertyName: "OutputFolder", ErrorMessage: "write permission denied"},
		{PropertyName: "", ErrorMessage: "Test failed: permission"},
	}}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var failures []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &failures); err != nil {
		t.Fatalf("body must be FluentValidation array: %v body=%s", err, rr.Body.String())
	}
	if len(failures) != 2 || failures[0]["propertyName"] != "OutputFolder" {
		t.Errorf("failures: %+v", failures)
	}
}

func TestMetadataTestHandler_UnknownImplementation_400(t *testing.T) {
	t.Parallel()
	tester := &fakeMetadataTester{err: metadata.ErrNotRegistered}
	body := validMetadataBody()
	body.Implementation = "GhostMetadata"
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	var failures []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &failures)
	if len(failures) != 1 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected Implementation failure: %+v", failures)
	}
}

func TestMetadataTestHandler_OtherError_500(t *testing.T) {
	t.Parallel()
	tester := &fakeMetadataTester{err: errors.New("disk full")}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataTestHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	MetadataTestHandler(&fakeMetadataTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/test", bytes.NewReader([]byte(`{not json`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataTestHandler_SkipsSharedValidator(t *testing.T) {
	t.Parallel()
	tester := &fakeMetadataTester{}
	body := validMetadataBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Errorf("skip-validation: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// fakeMetadataRowsLister satisfies MetadataRowsLister.
type fakeMetadataRowsLister struct {
	rows []metadata.Metadata
	err  error
}

func (f *fakeMetadataRowsLister) All(_ context.Context) ([]metadata.Metadata, error) {
	return f.rows, f.err
}

// perRowMetadataTester maps row ID → outcome.
type perRowMetadataTester struct {
	seen     []int
	byID     map[int][]metadata.TestFailure
	errByID  map[int]error
	defaultR []metadata.TestFailure
}

func (p *perRowMetadataTester) Test(_ context.Context, n *metadata.Metadata) ([]metadata.TestFailure, error) {
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

func TestMetadataTestAllHandler_Empty_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	MetadataTestAllHandler(&fakeMetadataRowsLister{}, &perRowMetadataTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `[]` {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestMetadataTestAllHandler_AllPass_Returns200Array(t *testing.T) {
	t.Parallel()
	rows := []metadata.Metadata{
		{ID: 1, Name: "a", Implementation: "XbmcMetadata"},
		{ID: 2, Name: "b", Implementation: "PlexMetadata"},
	}
	tester := &perRowMetadataTester{}
	rr := httptest.NewRecorder()
	MetadataTestAllHandler(&fakeMetadataRowsLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/testall", nil))
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

func TestMetadataTestAllHandler_MixedResults_Returns400(t *testing.T) {
	t.Parallel()
	rows := []metadata.Metadata{
		{ID: 1, Name: "ok", Implementation: "XbmcMetadata"},
		{ID: 2, Name: "bad", Implementation: "PlexMetadata"},
		{ID: 3, Name: "missing", Implementation: "GhostMetadata"},
		{ID: 4, Name: "boom", Implementation: "XbmcMetadata"},
	}
	tester := &perRowMetadataTester{
		byID: map[int][]metadata.TestFailure{
			2: {{PropertyName: "OutputFolder", ErrorMessage: "write permission denied"}},
		},
		errByID: map[int]error{
			3: metadata.ErrNotRegistered,
			4: errors.New("disk full"),
		},
	}
	rr := httptest.NewRecorder()
	MetadataTestAllHandler(&fakeMetadataRowsLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/testall", nil))
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
	if got[1].IsValid || got[1].ValidationFailures[0].PropertyName != "OutputFolder" {
		t.Errorf("row 1: %+v", got[1])
	}
	if got[2].IsValid || got[2].ValidationFailures[0].PropertyName != "Implementation" {
		t.Errorf("row 2: %+v", got[2])
	}
	if got[3].IsValid || got[3].ValidationFailures[0].PropertyName != "" {
		t.Errorf("row 3: %+v", got[3])
	}
}

func TestMetadataTestAllHandler_ListError_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	MetadataTestAllHandler(&fakeMetadataRowsLister{err: errors.New("db down")}, &perRowMetadataTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/testall", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataTestAllHandler_ValidationFailuresNonNil(t *testing.T) {
	t.Parallel()
	rows := []metadata.Metadata{{ID: 7, Implementation: "XbmcMetadata"}}
	rr := httptest.NewRecorder()
	MetadataTestAllHandler(&fakeMetadataRowsLister{rows: rows}, &perRowMetadataTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"validationFailures":[]`)) {
		t.Errorf("validationFailures should be [] not null: body=%s", rr.Body.String())
	}
}

// fakeMetadataActioner implements MetadataActioner.
type fakeMetadataActioner struct {
	seenName  string
	seenQuery map[string]string
	seenRow   *metadata.Metadata
	result    any
	err       error
}

func (f *fakeMetadataActioner) RequestAction(_ context.Context, n *metadata.Metadata, name string, query map[string]string) (any, error) {
	f.seenName = name
	f.seenQuery = query
	f.seenRow = n
	return f.result, f.err
}

func TestMetadataRequestActionHandler_NoOpDefault_EmitsJSONNull(t *testing.T) {
	t.Parallel()
	// Mirrors the metadata.Provider doc-comment "none of the 4 writers
	// override RequestAction; all return nil + nil-error". Confirm the
	// no-op default round-trips as JSON `null`.
	actioner := &fakeMetadataActioner{result: nil}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/metadata/action/getCustom?type=tv", bytes.NewReader(buf))
	req.SetPathValue("name", "getCustom")
	rr := httptest.NewRecorder()
	MetadataRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `null` {
		t.Errorf("body should be JSON null; got %q", rr.Body.String())
	}
	if actioner.seenName != "getCustom" {
		t.Errorf("action name: %q", actioner.seenName)
	}
	if actioner.seenQuery["type"] != "tv" {
		t.Errorf("query map: %+v", actioner.seenQuery)
	}
	if actioner.seenRow == nil || actioner.seenRow.Implementation != "XbmcMetadata" {
		t.Errorf("row not built: %+v", actioner.seenRow)
	}
}

func TestMetadataRequestActionHandler_NonNilResult(t *testing.T) {
	t.Parallel()
	// Hypothetical future writer that DOES override RequestAction —
	// verify the non-nil-result path marshals correctly.
	actioner := &fakeMetadataActioner{result: map[string]any{"result": "ok"}}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/metadata/action/x", bytes.NewReader(buf))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	MetadataRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body shape: %v body=%s", err, rr.Body.String())
	}
	if got["result"] != "ok" {
		t.Errorf("body: %+v", got)
	}
}

func TestMetadataRequestActionHandler_ErrNotRegistered_400(t *testing.T) {
	t.Parallel()
	actioner := &fakeMetadataActioner{err: metadata.ErrNotRegistered}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/metadata/action/x", bytes.NewReader(buf))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	MetadataRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	var failures []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &failures)
	if len(failures) != 1 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected Implementation failure: %+v", failures)
	}
}

func TestMetadataRequestActionHandler_OtherError_500(t *testing.T) {
	t.Parallel()
	actioner := &fakeMetadataActioner{err: errors.New("upstream down")}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/metadata/action/x", bytes.NewReader(buf))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	MetadataRequestActionHandler(actioner).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataRequestActionHandler_EmptyName_400(t *testing.T) {
	t.Parallel()
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/metadata/action/", bytes.NewReader(buf))
	req.SetPathValue("name", "")
	rr := httptest.NewRecorder()
	MetadataRequestActionHandler(&fakeMetadataActioner{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataRequestActionHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/metadata/action/x", bytes.NewReader([]byte(`{nope`)))
	req.SetPathValue("name", "x")
	rr := httptest.NewRecorder()
	MetadataRequestActionHandler(&fakeMetadataActioner{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
