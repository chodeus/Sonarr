package v3

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/queue"
)

// fakeQueueWriter implements QueueWriter against an in-memory list.
type fakeQueueWriter struct {
	rows         []queue.Queue
	removedCalls []int // ids passed to Remove, in order
}

func (f *fakeQueueWriter) Find(id int) *queue.Queue {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp
		}
	}
	return nil
}

func (f *fakeQueueWriter) Remove(id int) bool {
	f.removedCalls = append(f.removedCalls, id)
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return true
		}
	}
	return false
}

func TestQueueDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeQueueWriter{rows: []queue.Queue{{ID: 7}}}
	var broadcasts []int
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/queue/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	QueueDeleteHandler(w, func(id int) { broadcasts = append(broadcasts, id) }).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.removedCalls) != 1 || w.removedCalls[0] != 7 {
		t.Errorf("Remove not invoked with id 7: %v", w.removedCalls)
	}
	if len(broadcasts) != 1 || broadcasts[0] != 7 {
		t.Errorf("broadcast not fired with id 7: %v", broadcasts)
	}
}

func TestQueueDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	w := &fakeQueueWriter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/queue/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	QueueDeleteHandler(w, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
	if len(w.removedCalls) != 0 {
		t.Errorf("Remove should NOT fire when Find returns nil: %v", w.removedCalls)
	}
}

func TestQueueDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodDelete, "/api/v3/queue/"+raw, nil)
			req.SetPathValue("id", raw)
			rr := httptest.NewRecorder()
			QueueDeleteHandler(&fakeQueueWriter{}, nil).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: %d (id=%q)", rr.Code, raw)
			}
		})
	}
}

func TestQueueDeleteHandler_QueryParamsParsedButIgnored(t *testing.T) {
	t.Parallel()
	// 5.E2a defers all 4 side-effect query params. Confirm none
	// cause an error and the queue row is still removed.
	w := &fakeQueueWriter{rows: []queue.Queue{{ID: 7}}}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/queue/7?removeFromClient=true&blocklist=true&skipRedownload=true&changeCategory=true", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	QueueDeleteHandler(w, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 0 {
		t.Errorf("row should be removed: %+v", w.rows)
	}
}

func TestQueueDeleteHandler_NilBroadcaster(t *testing.T) {
	t.Parallel()
	// Nil broadcaster (production wiring today; SignalR queue
	// resource broadcaster not yet defined) — should not panic.
	w := &fakeQueueWriter{rows: []queue.Queue{{ID: 7}}}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/queue/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	QueueDeleteHandler(w, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
}

func TestQueueBulkDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeQueueWriter{rows: []queue.Queue{{ID: 1}, {ID: 2}, {ID: 3}}}
	var broadcasts []int
	body := QueueBulkDeleteResource{IDs: []int{1, 3}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	QueueBulkDeleteHandler(w, func(id int) { broadcasts = append(broadcasts, id) }).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/queue/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].ID != 2 {
		t.Errorf("rows after delete: %+v", w.rows)
	}
	if len(broadcasts) != 2 || broadcasts[0] != 1 || broadcasts[1] != 3 {
		t.Errorf("broadcasts: %v", broadcasts)
	}
}

func TestQueueBulkDeleteHandler_MissingIdsSilentlySkipped(t *testing.T) {
	t.Parallel()
	w := &fakeQueueWriter{rows: []queue.Queue{{ID: 1}}}
	var broadcasts []int
	body := QueueBulkDeleteResource{IDs: []int{1, 999}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	QueueBulkDeleteHandler(w, func(id int) { broadcasts = append(broadcasts, id) }).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/queue/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	// Broadcast only fires for ids that actually matched.
	if len(broadcasts) != 1 || broadcasts[0] != 1 {
		t.Errorf("broadcasts should only fire for matched ids: %v", broadcasts)
	}
}

func TestQueueBulkDeleteHandler_EmptyIDs_400(t *testing.T) {
	t.Parallel()
	body := QueueBulkDeleteResource{IDs: nil}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	QueueBulkDeleteHandler(&fakeQueueWriter{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/queue/bulk", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestQueueBulkDeleteHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueBulkDeleteHandler(&fakeQueueWriter{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/queue/bulk", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
