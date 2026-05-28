package queue

import (
	"bytes"
	"context"
	"encoding/json"
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

// ----- GET /api/v5/queue — includeSubresources[] gating (J352) -----

type fakeV5QueuePagedLister struct {
	records []apiv3.QueueResource
}

func (f fakeV5QueuePagedLister) PagedQueue(_ context.Context, _ apiv3.PagingRequest) ([]apiv3.QueueResource, int, error) {
	// Return a deep-ish copy so per-request stripping doesn't mutate
	// the fixture across sub-tests.
	out := make([]apiv3.QueueResource, len(f.records))
	copy(out, f.records)
	return out, len(out), nil
}

func mkV5Lister() fakeV5QueuePagedLister {
	return fakeV5QueuePagedLister{records: []apiv3.QueueResource{{
		ID:       1,
		Series:   &apiv3.SeriesResource{ID: 7, Title: "Show"},
		Episode:  &apiv3.EpisodeResource{ID: 11}, // v3-singular — v5 must strip
		Episodes: &[]apiv3.EpisodeResource{{ID: 11}, {ID: 12}},
	}}}
}

func TestListHandler_IncludeSubresources_J352(t *testing.T) {
	t.Parallel()
	decode := func(rr *httptest.ResponseRecorder) apiv3.QueueResource {
		var env apiv3.PagingResource[apiv3.QueueResource]
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
		}
		if len(env.Records) != 1 {
			t.Fatalf("records: got %d want 1", len(env.Records))
		}
		return env.Records[0]
	}
	t.Run("no subresources strips all", func(t *testing.T) {
		t.Parallel()
		rr := httptest.NewRecorder()
		ListHandler(mkV5Lister()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue", nil))
		r := decode(rr)
		if r.Series != nil || r.Episodes != nil || r.Episode != nil {
			t.Errorf("no includeSubresources must strip all subresources; got series=%v episodes=%v episode=%v", r.Series, r.Episodes, r.Episode)
		}
	})
	t.Run("series only", func(t *testing.T) {
		t.Parallel()
		rr := httptest.NewRecorder()
		ListHandler(mkV5Lister()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue?includeSubresources=series", nil))
		r := decode(rr)
		if r.Series == nil || r.Series.ID != 7 {
			t.Errorf("series requested must be kept; got %v", r.Series)
		}
		if r.Episodes != nil {
			t.Errorf("episodes not requested must be stripped; got %v", r.Episodes)
		}
		if r.Episode != nil {
			t.Errorf("v5 must always strip the singular Episode; got %v", r.Episode)
		}
	})
	t.Run("episodes only keeps plural strips singular", func(t *testing.T) {
		t.Parallel()
		rr := httptest.NewRecorder()
		ListHandler(mkV5Lister()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue?includeSubresources=episodes", nil))
		r := decode(rr)
		if r.Episodes == nil || len(*r.Episodes) != 2 {
			t.Errorf("episodes requested must be kept (plural); got %v", r.Episodes)
		}
		if r.Series != nil {
			t.Errorf("series not requested must be stripped; got %v", r.Series)
		}
		if r.Episode != nil {
			t.Errorf("v5 must always strip the singular Episode; got %v", r.Episode)
		}
	})
	t.Run("both", func(t *testing.T) {
		t.Parallel()
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v5/queue?includeSubresources=series&includeSubresources=episodes", nil)
		ListHandler(mkV5Lister()).ServeHTTP(rr, req)
		r := decode(rr)
		if r.Series == nil || r.Episodes == nil {
			t.Errorf("both requested must keep series + episodes; got series=%v episodes=%v", r.Series, r.Episodes)
		}
	})
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
