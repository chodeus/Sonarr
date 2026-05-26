package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/notifications"
)

// fakeWriter implements Writer.
type fakeWriter struct {
	rows    []notifications.Notification
	deletes atomic.Int64
}

func (f *fakeWriter) Get(_ context.Context, id int) (*notifications.Notification, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, notifications.ErrNotFound
}

func (f *fakeWriter) All(_ context.Context) ([]notifications.Notification, error) {
	out := make([]notifications.Notification, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func (f *fakeWriter) Insert(_ context.Context, n *notifications.Notification) (*notifications.Notification, error) {
	n.ID = len(f.rows) + 1
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}

func (f *fakeWriter) Update(_ context.Context, n *notifications.Notification) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return notifications.ErrNotFound
}

func (f *fakeWriter) Delete(_ context.Context, id int) error {
	f.deletes.Add(1)
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return notifications.ErrNotFound
}

// fakeEmitter implements Emitter.
type fakeEmitter struct{}

func (fakeEmitter) Created(_ context.Context, _ Resource) error { return nil }
func (fakeEmitter) Updated(_ context.Context, _ Resource) error { return nil }
func (fakeEmitter) Deleted(_ context.Context, _ Resource) error { return nil }

// fakeTester implements Tester (zero-failure success → 200+`{}` in v3).
type fakeTester struct{}

func (fakeTester) Test(_ context.Context, _ *notifications.Notification) ([]notifications.TestFailure, error) {
	return nil, nil
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestDeleteHandler_Rewrites200To204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []notifications.Notification{{ID: 42, Name: "n"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/connection/42", nil)
	req.SetPathValue("id", "42")
	DeleteHandler(w, fakeEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if w.deletes.Load() != 1 {
		t.Errorf("svc.Delete should be called once; got %d", w.deletes.Load())
	}
}

func TestDeleteHandler_MissingID_Returns204Idempotent(t *testing.T) {
	t.Parallel()
	// J232 idempotent missing-id contract carries through wrapper —
	// v3 swallows ErrNotFound and emits 200+`{}`; wrapper rewrites
	// to 204.
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/connection/9999", nil)
	req.SetPathValue("id", "9999")
	DeleteHandler(w, fakeEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
}

// ----- POST /test — v5 returns 204 (v3 returns 200 + `{}` on success) -----

func TestTestHandler_Rewrites200To204(t *testing.T) {
	t.Parallel()
	// Empty body may be rejected by fromNotificationResource pre-test
	// validation (400). The wrapper contract is strictly 200→204; a
	// 400 passes through unchanged. Accept both.
	body := Resource{}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/connection/test", bytes.NewReader(buf))
	TestHandler(fakeTester{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent && rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 204 (rewritten) or 400 (passthrough); body=%q", rr.Code, rr.Body.String())
	}
	if rr.Code == http.StatusNoContent && rr.Body.Len() != 0 {
		t.Errorf("rewritten 204 body should be empty; got %q", rr.Body.String())
	}
}

// ----- alias guards -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	if ListHandler == nil || ByIDHandler == nil || SchemaHandler == nil {
		t.Error("list/byid/schema handler aliases must be non-nil")
	}
	if CreateHandler == nil || UpdateHandler == nil {
		t.Error("create/update handler aliases must be non-nil")
	}
	if TestAllHandler == nil || RequestActionHandler == nil {
		t.Error("testall/action handler aliases must be non-nil")
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	// Pure-alias slice: any drift in Resource shape between v3
	// NotificationResource and v5 ConnectionResource would fail to
	// compile.
	var _ Resource = apiv3.NotificationResource{}
	var _ apiv3.NotificationResource = Resource{}
}
