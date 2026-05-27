package queue

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/queue"
)

// fakeWriter implements Writer (alias of apiv3.QueueWriter) — the
// minimum surface DeleteHandler / BulkDeleteHandler need.
type fakeWriter struct {
	rows []queue.Queue
}

func (f *fakeWriter) Find(id int) *queue.Queue {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp
		}
	}
	return nil
}

func (f *fakeWriter) Remove(id int) bool {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return true
		}
	}
	return false
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestDeleteHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []queue.Queue{{ID: 7}}}
	var broadcasts []int
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/queue/7", nil)
	req.SetPathValue("id", "7")
	DeleteHandler(w, func(id int) { broadcasts = append(broadcasts, id) }).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type should be stripped for 204; got %q", ct)
	}
	if len(broadcasts) != 1 || broadcasts[0] != 7 {
		t.Errorf("SignalR queue/deleted broadcast: got %v want [7]", broadcasts)
	}
}

func TestDeleteHandler_NotFound_PassesThrough404(t *testing.T) {
	t.Parallel()
	// Queue DELETE is NOT in the J232 idempotency-aligned set —
	// v3 returns 404 explicitly (mirrors C# NotFoundException at
	// QueueController.cs:88). The v5 wrapper preserves 404 verbatim.
	w := &fakeWriter{}
	var broadcasts []int
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/queue/999", nil)
	req.SetPathValue("id", "999")
	DeleteHandler(w, func(id int) { broadcasts = append(broadcasts, id) }).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (passthrough — queue DELETE non-idempotent)", rr.Code)
	}
	if len(broadcasts) != 0 {
		t.Errorf("SignalR queue/deleted must not fire on missing id: got %v", broadcasts)
	}
}

func TestDeleteHandler_InvalidID_PassesThrough400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/queue/abc", nil)
	req.SetPathValue("id", "abc")
	DeleteHandler(&fakeWriter{}, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (passthrough)", rr.Code)
	}
}

// ----- DELETE /bulk — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestBulkDeleteHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []queue.Queue{{ID: 1}, {ID: 2}, {ID: 3}}}
	var broadcasts []int
	body := []byte(`{"ids":[1,3]}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/queue/bulk", bytes.NewReader(body))
	BulkDeleteHandler(w, func(id int) { broadcasts = append(broadcasts, id) }).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if len(broadcasts) != 2 || broadcasts[0] != 1 || broadcasts[1] != 3 {
		t.Errorf("SignalR queue/deleted broadcasts: got %v want [1 3]", broadcasts)
	}
}

func TestBulkDeleteHandler_SilentSkipMissingIDs_Returns204(t *testing.T) {
	t.Parallel()
	// Mixed present + missing ids — v3 silently skips missing (matches
	// C# RemoveMany's lookup-returns-null skip at QueueController.cs:107-121).
	// v5 still emits 204 (wrapper rewrites the 200+`{}` happy-path verbatim).
	// SignalR queue/deleted broadcast fires only for the actual remove(s).
	w := &fakeWriter{rows: []queue.Queue{{ID: 1}}}
	var broadcasts []int
	body := []byte(`{"ids":[1,999]}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/queue/bulk", bytes.NewReader(body))
	BulkDeleteHandler(w, func(id int) { broadcasts = append(broadcasts, id) }).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (silent-skip on missing id)", rr.Code)
	}
	if len(broadcasts) != 1 || broadcasts[0] != 1 {
		t.Errorf("SignalR broadcast: got %v want [1] (only actual remove)", broadcasts)
	}
}

func TestBulkDeleteHandler_EmptyIDs_PassesThrough400(t *testing.T) {
	t.Parallel()
	// v3 returns 400 with "ids must be provided" on empty body; v5
	// inherits unchanged (only 200+`{}` triggers the 204 rewrite).
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/queue/bulk", bytes.NewReader([]byte(`{"ids":[]}`)))
	BulkDeleteHandler(w, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (passthrough)", rr.Code)
	}
}

func TestBulkDeleteHandler_MalformedBody_PassesThrough400(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/queue/bulk", bytes.NewReader([]byte(`{not`)))
	BulkDeleteHandler(w, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (passthrough)", rr.Code)
	}
}

// ----- type aliases (compile-time invariant guard) -----

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.QueueResource{}
	var _ apiv3.QueueResource = Resource{}
	var _ PagedLister = (apiv3.QueuePagedLister)(nil)
	var _ Writer = (apiv3.QueueWriter)(nil)
	var _ BulkDeleteResource = apiv3.QueueBulkDeleteResource{}
	var _ apiv3.QueueBulkDeleteResource = BulkDeleteResource{}
}
