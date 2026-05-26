package blocklist

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// fakeWriter implements Writer.
type fakeWriter struct {
	deletes     atomic.Int64
	bulkDeletes atomic.Int64
}

func (f *fakeWriter) Delete(_ context.Context, _ int) error {
	f.deletes.Add(1)
	return nil
}

func (f *fakeWriter) DeleteByIDs(_ context.Context, ids []int) (int64, error) {
	f.bulkDeletes.Add(1)
	return int64(len(ids)), nil
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestDeleteHandler_Success_Returns204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/blocklist/42", nil)
	req.SetPathValue("id", "42")
	DeleteHandler(w).ServeHTTP(rr, req)
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
	// J232 idempotency: v3 always returns 200+`{}` regardless of
	// existence; v5 wrapper demotes to 204.
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/blocklist/9999", nil)
	req.SetPathValue("id", "9999")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
}

// ----- DELETE /bulk — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestBulkDeleteHandler_Success_Returns204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	body := struct {
		IDs []int `json:"ids"`
	}{IDs: []int{1, 2, 3}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/blocklist/bulk", bytes.NewReader(buf))
	BulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if w.bulkDeletes.Load() != 1 {
		t.Errorf("svc.DeleteByIDs should be called once; got %d", w.bulkDeletes.Load())
	}
}

func TestBulkDeleteHandler_EmptyIDs_Returns204(t *testing.T) {
	t.Parallel()
	// C# silently no-ops on empty IDs; v3 Go-port matches; v5 wrapper
	// demotes to 204. svc.DeleteByIDs IS called (just with empty list).
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/blocklist/bulk", bytes.NewReader([]byte(`{"ids":[]}`)))
	BulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
}

// ----- alias guards -----

func TestVariableAlias_GetHandlerNonNil(t *testing.T) {
	t.Parallel()
	if GetHandler == nil {
		t.Error("GetHandler alias is nil — v3 re-export broken")
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.BlocklistResource{}
	var _ apiv3.BlocklistResource = Resource{}
}
