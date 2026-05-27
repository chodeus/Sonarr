package episodefile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/episodefiles"
)

// fakeWriter implements Writer (alias of apiv3.EpisodeFileWriter) —
// the minimum surface DeleteHandler / BulkUpdateHandler / BulkDeleteHandler
// need.
type fakeWriter struct {
	files       map[int]*episodefiles.File
	getManyErr  error
	deleted     []int
	updated     []*episodefiles.File
	updateErr   error
}

func (f *fakeWriter) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if v, ok := f.files[id]; ok {
		return v, nil
	}
	return nil, episodefiles.ErrNotFound
}

func (f *fakeWriter) GetFiles(_ context.Context, ids []int) ([]episodefiles.File, error) {
	if f.getManyErr != nil {
		return nil, f.getManyErr
	}
	out := make([]episodefiles.File, 0, len(ids))
	for _, id := range ids {
		if v, ok := f.files[id]; ok {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (f *fakeWriter) Delete(_ context.Context, file *episodefiles.File, _ episodefiles.DeleteMediaFileReason) error {
	f.deleted = append(f.deleted, file.ID)
	return nil
}

func (f *fakeWriter) Update(_ context.Context, file *episodefiles.File) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	cp := *file
	f.updated = append(f.updated, &cp)
	return nil
}

func (f *fakeWriter) UpdateMany(_ context.Context, files []*episodefiles.File) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for _, fi := range files {
		cp := *fi
		f.updated = append(f.updated, &cp)
	}
	return nil
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestDeleteHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{files: map[int]*episodefiles.File{
		7: {ID: 7, SeriesID: 1, RelativePath: "Show.S01E01.mkv"},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/episodefile/7", nil)
	req.SetPathValue("id", "7")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type should be stripped for 204; got %q", ct)
	}
	if len(w.deleted) != 1 || w.deleted[0] != 7 {
		t.Errorf("delete dispatch: got %v want [7]", w.deleted)
	}
}

func TestDeleteHandler_NotFound_PassesThrough404(t *testing.T) {
	t.Parallel()
	// episodefile DELETE is NOT in the J232 idempotency-aligned set —
	// v3 returns 404 explicitly (mirroring C#
	// `NzbDroneClientException(HttpStatusCode.NotFound)` throw at
	// EpisodeFileController.cs:120). The wrapper preserves 404 verbatim.
	w := &fakeWriter{files: map[int]*episodefiles.File{}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/episodefile/999", nil)
	req.SetPathValue("id", "999")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (passthrough; episodefile is non-idempotent)", rr.Code)
	}
	if len(w.deleted) != 0 {
		t.Errorf("delete must not dispatch on missing id; got %v", w.deleted)
	}
}

// ----- DELETE /bulk — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestBulkDeleteHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{files: map[int]*episodefiles.File{
		1: {ID: 1, SeriesID: 1, RelativePath: "a.mkv"},
		2: {ID: 2, SeriesID: 1, RelativePath: "b.mkv"},
	}}
	body, _ := json.Marshal(map[string][]int{"episodeFileIds": {1, 2}})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/episodefile/bulk", bytes.NewReader(body))
	BulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if len(w.deleted) != 2 {
		t.Errorf("delete dispatch: got %v want both", w.deleted)
	}
}

func TestBulkDeleteHandler_EmptyList_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	// v3 short-circuits empty list to 200 + `{}`; v5 rewrites to 204 + empty.
	w := &fakeWriter{}
	body, _ := json.Marshal(map[string][]int{"episodeFileIds": {}})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/episodefile/bulk", bytes.NewReader(body))
	BulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (empty-list short-circuit)", rr.Code)
	}
	if len(w.deleted) != 0 {
		t.Errorf("empty list must not dispatch deletes; got %v", w.deleted)
	}
}

func TestBulkDeleteHandler_MalformedBody_PassesThrough400(t *testing.T) {
	t.Parallel()
	// The wrapper only rewrites 200+`{}` → 204; 400+envelope passes through.
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/episodefile/bulk", bytes.NewReader([]byte(`{not-json`)))
	BulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (passthrough)", rr.Code)
	}
	if rr.Body.Len() == 0 {
		t.Error("400 envelope should pass through with non-empty body")
	}
}

// ----- PUT /bulk — v5 returns 200 + body (v3 returns 202 + body) -----

func TestBulkUpdateHandler_Success_Returns200WithBodyPreserved(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{files: map[int]*episodefiles.File{
		3: {ID: 3, SeriesID: 1, RelativePath: "c.mkv"},
	}}
	body, _ := json.Marshal([]Resource{{ID: 3, SceneName: "Show.S01E03.Scene"}})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/episodefile/bulk", bytes.NewReader(body))
	BulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 (v3 202 demoted to v5 200); body=%q", rr.Code, rr.Body.String())
	}
	// Body should be the updated-rows array, byte-preserved from v3.
	var got []Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body decode: %v; raw=%q", err, rr.Body.String())
	}
	if len(got) != 1 || got[0].ID != 3 {
		t.Errorf("body content: got %+v", got)
	}
	if len(w.updated) != 1 {
		t.Errorf("update dispatch: got %v want 1 row", w.updated)
	}
}

func TestBulkUpdateHandler_MalformedBody_PassesThrough400(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/episodefile/bulk", bytes.NewReader([]byte(`[broken`)))
	BulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (passthrough)", rr.Code)
	}
}

func TestBulkUpdateHandler_ServiceError_PassesThrough500(t *testing.T) {
	t.Parallel()
	// 500 must NOT be demoted — only 202 + body matches the rewrite predicate.
	w := &fakeWriter{
		files:     map[int]*episodefiles.File{4: {ID: 4, SeriesID: 1, RelativePath: "d.mkv"}},
		updateErr: errors.New("disk full"),
	}
	body, _ := json.Marshal([]Resource{{ID: 4}})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/episodefile/bulk", bytes.NewReader(body))
	BulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500 (passthrough; rewrite only triggers on 202+body)", rr.Code)
	}
}

// ----- type aliases (compile-time invariant guard) -----

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.EpisodeFileResource{}
	var _ apiv3.EpisodeFileResource = Resource{}
	// Interface aliases — declaration-only assertion (not assignable to
	// the zero value of an interface from outside).
	var _ Reader = (apiv3.EpisodeFileReader)(nil)
	var _ Writer = (apiv3.EpisodeFileWriter)(nil)
	var _ SeriesExistsChecker = (apiv3.SeriesExistsChecker)(nil)
}
