package v3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
)

type fakeNotifLister struct {
	rows []notifications.Notification
	err  error
}

func (f *fakeNotifLister) All(context.Context) ([]notifications.Notification, error) {
	return f.rows, f.err
}

func TestNotificationHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationHandler(&fakeNotifLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/notification", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestNotificationHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationHandler(&fakeNotifLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/notification", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// fakeNotifWriter implements NotificationWriter against an in-memory
// table. Insert auto-assigns ids monotonically; Update + Delete match
// by id with ErrNotFound on miss.
type fakeNotifWriter struct {
	rows   []notifications.Notification
	nextID int64
	getErr error
}

func (f *fakeNotifWriter) Get(_ context.Context, id int) (*notifications.Notification, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, notifications.ErrNotFound
}
func (f *fakeNotifWriter) All(_ context.Context) ([]notifications.Notification, error) {
	out := make([]notifications.Notification, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
func (f *fakeNotifWriter) Insert(_ context.Context, n *notifications.Notification) (*notifications.Notification, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}
func (f *fakeNotifWriter) Update(_ context.Context, n *notifications.Notification) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return notifications.ErrNotFound
}
func (f *fakeNotifWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return notifications.ErrNotFound
}

type fakeNotifEmitter struct {
	created []NotificationResource
	updated []NotificationResource
	deleted []NotificationResource
}

func (f *fakeNotifEmitter) Created(_ context.Context, r NotificationResource) error {
	f.created = append(f.created, r)
	return nil
}
func (f *fakeNotifEmitter) Updated(_ context.Context, r NotificationResource) error {
	f.updated = append(f.updated, r)
	return nil
}
func (f *fakeNotifEmitter) Deleted(_ context.Context, r NotificationResource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

type fakeNotifRegistry struct{ valid map[string]bool }

func (f *fakeNotifRegistry) Has(impl, cfg string) bool {
	if f.valid == nil {
		return true
	}
	return f.valid[impl+"|"+cfg]
}

func validRegistry() *fakeNotifRegistry {
	return &fakeNotifRegistry{valid: map[string]bool{
		"Webhook|WebhookSettings": true,
		"Discord|DiscordSettings": true,
	}}
}

func validNotificationBody() NotificationResource {
	return NotificationResource{
		providerResourceCore: providerResourceCore{
			Name:               "My Webhook",
			Implementation:     "Webhook",
			ImplementationName: "Webhook",
			ConfigContract:     "WebhookSettings",
			Tags:               []int{1, 2},
		},
		OnGrab:           true,
		OnDownload:       true,
		OnUpgrade:        true,
		OnSeriesDelete:   true,
		OnImportComplete: true,
		Fields: []ProviderField{
			{Name: "url", Value: "https://example.com/hook"},
			{Name: "method", Value: float64(1)}, // JSON numbers round-trip as float64
		},
	}
}

func TestNotificationCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeNotifWriter{}
	em := &fakeNotifEmitter{}
	body := validNotificationBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/notification", bytes.NewReader(buf))
	rr := httptest.NewRecorder()
	NotificationCreateHandler(w, validRegistry(), em).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].Name != "My Webhook" || w.rows[0].ID <= 0 {
		t.Errorf("row not inserted: %+v", w.rows)
	}
	// Settings JSON must be reconstructed from fields[].
	if w.rows[0].Settings == "" || w.rows[0].Settings == "{}" {
		t.Errorf("settings should pack fields[]; got %q", w.rows[0].Settings)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(w.rows[0].Settings), &settings); err != nil {
		t.Fatalf("settings JSON: %v", err)
	}
	if settings["url"] != "https://example.com/hook" {
		t.Errorf("url field: %v", settings["url"])
	}
	if len(em.created) != 1 || em.created[0].Name != "My Webhook" {
		t.Errorf("emitter.Created not fired: %+v", em.created)
	}
}

func TestNotificationCreateHandler_BadImplementation_400Validation(t *testing.T) {
	t.Parallel()
	body := validNotificationBody()
	body.Implementation = "TotallyMadeUp"
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	NotificationCreateHandler(&fakeNotifWriter{}, validRegistry(), &fakeNotifEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var failures []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &failures); err != nil {
		t.Fatalf("body shape: %v body=%s", err, rr.Body.String())
	}
	if len(failures) == 0 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected Implementation failure: %v", failures)
	}
}

func TestNotificationCreateHandler_NameMissing_400Validation(t *testing.T) {
	t.Parallel()
	body := validNotificationBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	NotificationCreateHandler(&fakeNotifWriter{}, validRegistry(), &fakeNotifEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNotificationCreateHandler_DuplicateName_400Validation(t *testing.T) {
	t.Parallel()
	body := validNotificationBody()
	w := &fakeNotifWriter{rows: []notifications.Notification{{ID: 7, Name: "my webhook"}}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	NotificationCreateHandler(w, validRegistry(), &fakeNotifEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNotificationCreateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationCreateHandler(&fakeNotifWriter{}, validRegistry(), &fakeNotifEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
}

func TestNotificationUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	existing := notifications.Notification{ID: 42, Name: "old", Implementation: "Webhook", Settings: `{"url":"old"}`}
	w := &fakeNotifWriter{rows: []notifications.Notification{existing}}
	em := &fakeNotifEmitter{}
	body := validNotificationBody()
	body.ID = 42
	body.Name = "renamed"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/notification/42", bytes.NewReader(buf))
	req.SetPathValue("id", "42")
	rr := httptest.NewRecorder()
	NotificationUpdateHandler(w, validRegistry(), em).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Name != "renamed" {
		t.Errorf("row not updated: %+v", w.rows[0])
	}
	if len(em.updated) != 1 {
		t.Errorf("emitter.Updated not fired: %+v", em.updated)
	}
}

func TestNotificationUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	body := validNotificationBody()
	body.ID = 999
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/notification/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	NotificationUpdateHandler(&fakeNotifWriter{}, validRegistry(), &fakeNotifEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNotificationUpdateHandler_BodyIDFallback(t *testing.T) {
	t.Parallel()
	// C# behavior: when route id misses but body.Id exists, use the body id.
	existing := notifications.Notification{ID: 5, Name: "old", Implementation: "Webhook"}
	w := &fakeNotifWriter{rows: []notifications.Notification{existing}}
	body := validNotificationBody()
	body.ID = 5
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/notification/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	NotificationUpdateHandler(w, validRegistry(), &fakeNotifEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Name != "My Webhook" {
		t.Errorf("body-id fallback didn't update row 5: %+v", w.rows[0])
	}
}

func TestNotificationUpdateHandler_OmittedFields_KeepsExistingSettings(t *testing.T) {
	t.Parallel()
	existing := notifications.Notification{ID: 1, Name: "old", Implementation: "Webhook", Settings: `{"url":"keep-me"}`}
	w := &fakeNotifWriter{rows: []notifications.Notification{existing}}
	body := validNotificationBody()
	body.ID = 1
	body.Fields = nil // omit fields entirely
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/notification/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	NotificationUpdateHandler(w, validRegistry(), &fakeNotifEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Settings != `{"url":"keep-me"}` {
		t.Errorf("settings should be preserved when fields omitted; got %q", w.rows[0].Settings)
	}
}

// fakeNotifTester implements NotificationTester. result and err are
// returned verbatim. seen records the last Notification it was asked
// to test so the handler-side fromResource projection can be verified.
type fakeNotifTester struct {
	seen   *notifications.Notification
	result []notifications.TestFailure
	err    error
}

func (f *fakeNotifTester) Test(_ context.Context, n *notifications.Notification) ([]notifications.TestFailure, error) {
	f.seen = n
	return f.result, f.err
}

func TestNotificationTestHandler_Success_Returns200EmptyObject(t *testing.T) {
	t.Parallel()
	tester := &fakeNotifTester{}
	body := validNotificationBody()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/notification/test", bytes.NewReader(buf))
	rr := httptest.NewRecorder()
	NotificationTestHandler(tester).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q want {}", rr.Body.String())
	}
	if tester.seen == nil || tester.seen.Implementation != "Webhook" {
		t.Errorf("Test should be invoked with built Notification: seen=%+v", tester.seen)
	}
	// Settings JSON must be reconstructed from fields[] before Test runs.
	if tester.seen != nil {
		var settings map[string]any
		if err := json.Unmarshal([]byte(tester.seen.Settings), &settings); err != nil {
			t.Fatalf("Settings JSON: %v", err)
		}
		if settings["url"] != "https://example.com/hook" {
			t.Errorf("settings url not packed: %v", settings["url"])
		}
	}
}

func TestNotificationTestHandler_FailuresProjectTo400Array(t *testing.T) {
	t.Parallel()
	tester := &fakeNotifTester{result: []notifications.TestFailure{
		{PropertyName: "Url", ErrorMessage: "could not connect"},
		{PropertyName: "", ErrorMessage: "Test failed: timeout"},
	}}
	body := validNotificationBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	NotificationTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/test", bytes.NewReader(buf)))
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
	if failures[0]["propertyName"] != "Url" || failures[0]["errorMessage"] != "could not connect" {
		t.Errorf("first failure: %+v", failures[0])
	}
	if failures[0]["severity"] != "error" {
		t.Errorf("severity must default to error: %+v", failures[0])
	}
	if failures[1]["propertyName"] != "" || failures[1]["errorMessage"] != "Test failed: timeout" {
		t.Errorf("second failure: %+v", failures[1])
	}
}

func TestNotificationTestHandler_UnknownImplementation_400Validation(t *testing.T) {
	t.Parallel()
	tester := &fakeNotifTester{err: notifications.ErrNotRegistered}
	body := validNotificationBody()
	body.Implementation = "NoSuchProvider"
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	NotificationTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var failures []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &failures); err != nil {
		t.Fatalf("body shape: %v", err)
	}
	if len(failures) != 1 || failures[0]["propertyName"] != "Implementation" {
		t.Errorf("expected single Implementation failure: %+v", failures)
	}
}

func TestNotificationTestHandler_OtherError_500(t *testing.T) {
	t.Parallel()
	tester := &fakeNotifTester{err: errors.New("transport broke")}
	body := validNotificationBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	NotificationTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNotificationTestHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationTestHandler(&fakeNotifTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/test", bytes.NewReader([]byte(`{not json`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// TestNotificationTestHandler_SkipsSharedValidator confirms the
// SharedValidator name-non-empty + name-unique + implementation-in-
// registry rules are bypassed at this endpoint. C# uses
// [SkipValidation(true,false)] at :220 to disable PostValidator; the
// Go port skips the entire SharedValidator chain because the Tester
// receives the raw resource and decides what to return.
func TestNotificationTestHandler_SkipsSharedValidator(t *testing.T) {
	t.Parallel()
	tester := &fakeNotifTester{}
	body := validNotificationBody()
	body.Name = "" // SharedValidator would 400 this on POST/PUT
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	NotificationTestHandler(tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/test", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Errorf("name-missing should pass through to Test() (skip-validation): status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// perRowTester maps row ID → outcome so a single fake covers
// mixed-result testall scenarios.
type perRowTester struct {
	seen     []int
	byID     map[int][]notifications.TestFailure
	errByID  map[int]error
	defaultR []notifications.TestFailure
}

func (p *perRowTester) Test(_ context.Context, n *notifications.Notification) ([]notifications.TestFailure, error) {
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

func TestNotificationTestAllHandler_Empty_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationTestAllHandler(&fakeNotifLister{}, &perRowTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `[]` {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestNotificationTestAllHandler_AllPass_Returns200Array(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Name: "a", Implementation: "Webhook"},
		{ID: 2, Name: "b", Implementation: "Discord"},
	}
	tester := &perRowTester{} // defaultR nil = passing
	rr := httptest.NewRecorder()
	NotificationTestAllHandler(&fakeNotifLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got []ProviderTestAllResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rr.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("len: %d", len(got))
	}
	for i, r := range got {
		if !r.IsValid {
			t.Errorf("row %d isValid: false; want true", i)
		}
		if len(r.ValidationFailures) != 0 {
			t.Errorf("row %d failures: %+v", i, r.ValidationFailures)
		}
	}
	if len(tester.seen) != 2 || tester.seen[0] != 1 || tester.seen[1] != 2 {
		t.Errorf("Test should be called per-row in order: %v", tester.seen)
	}
}

func TestNotificationTestAllHandler_MixedResults_Returns400(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Name: "ok", Implementation: "Webhook"},
		{ID: 2, Name: "bad", Implementation: "Discord"},
		{ID: 3, Name: "missing", Implementation: "GhostProvider"},
		{ID: 4, Name: "boom", Implementation: "Webhook"},
	}
	tester := &perRowTester{
		byID: map[int][]notifications.TestFailure{
			2: {{PropertyName: "Url", ErrorMessage: "could not connect"}},
		},
		errByID: map[int]error{
			3: notifications.ErrNotRegistered,
			4: errors.New("transport reset"),
		},
	}
	rr := httptest.NewRecorder()
	NotificationTestAllHandler(&fakeNotifLister{rows: rows}, tester).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/testall", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got []ProviderTestAllResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rr.Body.String())
	}
	if len(got) != 4 {
		t.Fatalf("len: %d", len(got))
	}
	// Row 1 passes.
	if !got[0].IsValid || len(got[0].ValidationFailures) != 0 {
		t.Errorf("row 0: %+v", got[0])
	}
	// Row 2 has the per-field failure.
	if got[1].IsValid || len(got[1].ValidationFailures) != 1 || got[1].ValidationFailures[0].PropertyName != "Url" {
		t.Errorf("row 1: %+v", got[1])
	}
	// Row 3 (ErrNotRegistered) reports a synthesised Implementation failure.
	if got[2].IsValid || len(got[2].ValidationFailures) != 1 || got[2].ValidationFailures[0].PropertyName != "Implementation" {
		t.Errorf("row 2: %+v", got[2])
	}
	// Row 4 (transport error) reports a whole-form Test-failed failure.
	if got[3].IsValid || len(got[3].ValidationFailures) != 1 || got[3].ValidationFailures[0].PropertyName != "" {
		t.Errorf("row 3: %+v", got[3])
	}
}

func TestNotificationTestAllHandler_ListError_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationTestAllHandler(&fakeNotifLister{err: errors.New("db down")}, &perRowTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/testall", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestNotificationTestAllHandler_ValidationFailuresNonNil(t *testing.T) {
	t.Parallel()
	// Even on success the per-row validationFailures field must be
	// `[]` (non-null) — matches the C# `new List<ValidationFailure>()`
	// default. Tests the JSON output literally.
	rows := []notifications.Notification{{ID: 7, Implementation: "Webhook"}}
	rr := httptest.NewRecorder()
	NotificationTestAllHandler(&fakeNotifLister{rows: rows}, &perRowTester{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/notification/testall", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"validationFailures":[]`)) {
		t.Errorf("validationFailures should be [] not null: body=%s", rr.Body.String())
	}
}

func TestNotificationDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeNotifWriter{rows: []notifications.Notification{{ID: 7, Name: "victim"}}}
	em := &fakeNotifEmitter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/notification/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	NotificationDeleteHandler(w, em).ServeHTTP(rr, req)
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

func TestNotificationDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/notification/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	NotificationDeleteHandler(&fakeNotifWriter{}, &fakeNotifEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestNotificationDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodDelete, "/api/v3/notification/"+raw, nil)
			req.SetPathValue("id", raw)
			rr := httptest.NewRecorder()
			NotificationDeleteHandler(&fakeNotifWriter{}, &fakeNotifEmitter{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: %d (id=%q)", rr.Code, raw)
			}
		})
	}
	_ = strconv.Itoa(0)
}
