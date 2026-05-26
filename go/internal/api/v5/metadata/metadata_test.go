package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/metadata"
)

// fakeWriter implements Writer (alias of apiv3.MetadataWriter) — the
// minimum surface DeleteHandler needs.
type fakeWriter struct {
	rows   []metadata.Metadata
	nextID int64
}

func (f *fakeWriter) Get(_ context.Context, id int) (*metadata.Metadata, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, metadata.ErrNotFound
}

func (f *fakeWriter) All(_ context.Context) ([]metadata.Metadata, error) {
	out := make([]metadata.Metadata, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func (f *fakeWriter) Insert(_ context.Context, n *metadata.Metadata) (*metadata.Metadata, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}

func (f *fakeWriter) Update(_ context.Context, n *metadata.Metadata) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return metadata.ErrNotFound
}

func (f *fakeWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return metadata.ErrNotFound
}

// fakeEmitter implements Emitter — captures the per-CRUD broadcasts.
type fakeEmitter struct {
	deleted []Resource
}

func (f *fakeEmitter) Created(_ context.Context, _ Resource) error { return nil }
func (f *fakeEmitter) Updated(_ context.Context, _ Resource) error { return nil }
func (f *fakeEmitter) Deleted(_ context.Context, r Resource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

// fakeTester implements Tester.
type fakeTester struct {
	result []metadata.TestFailure
	err    error
}

func (f *fakeTester) Test(_ context.Context, _ *metadata.Metadata) ([]metadata.TestFailure, error) {
	return f.result, f.err
}

func validMetadataBody() Resource {
	return Resource{
		Name:               "Kodi",
		Implementation:     "XbmcMetadata",
		ImplementationName: "Kodi (XBMC) / Emby",
		ConfigContract:     "XbmcMetadataSettings",
		Tags:               []int{},
		Enable:             true,
		Fields: []apiv3.ProviderField{
			{Name: "seriesMetadata", Value: true},
			{Name: "episodeMetadata", Value: false},
		},
	}
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestDeleteHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []metadata.Metadata{{ID: 1}}}
	em := &fakeEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/metadata/1", nil)
	req.SetPathValue("id", "1")
	DeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type should be stripped for 204; got %q", ct)
	}
	if len(em.deleted) != 1 {
		t.Errorf("SignalR Deleted should fire on actual delete; got %d emits", len(em.deleted))
	}
}

func TestDeleteHandler_MissingID_Returns204Idempotent(t *testing.T) {
	t.Parallel()
	// C.1.i-follow-4 idempotency carry-through (J232): v3 returns 200+`{}`
	// on missing id; the rewrite layer demotes to 204. No SignalR emit.
	w := &fakeWriter{}
	em := &fakeEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/metadata/999", nil)
	req.SetPathValue("id", "999")
	DeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (idempotent missing-id)", rr.Code)
	}
	if len(em.deleted) != 0 {
		t.Errorf("SignalR Deleted must not fire on missing-id swallow; got %d emits", len(em.deleted))
	}
}

// ----- POST /test — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestTestHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	tester := &fakeTester{}
	body, _ := json.Marshal(validMetadataBody())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/metadata/test", bytes.NewReader(body))
	TestHandler(tester).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
}

func TestTestHandler_ValidationFailure_PassesThrough400(t *testing.T) {
	t.Parallel()
	// The wrapper only rewrites 200+`{}` → 204; other shapes pass through.
	// A test handler that surfaces validation failures emits 400+envelope;
	// the v5 wrapper should preserve that verbatim.
	tester := &fakeTester{result: []metadata.TestFailure{{PropertyName: "Path", ErrorMessage: "Not writable"}}}
	body, _ := json.Marshal(validMetadataBody())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/metadata/test", bytes.NewReader(body))
	TestHandler(tester).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (validation passthrough)", rr.Code)
	}
	if rr.Body.Len() == 0 {
		t.Error("validation envelope should NOT be stripped on 400 passthrough")
	}
}

// ----- type aliases (compile-time invariant guard) -----

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.MetadataResource{}
	var _ BulkResource = apiv3.MetadataBulkResource{}
	var _ apiv3.MetadataResource = Resource{}
	var _ apiv3.MetadataBulkResource = BulkResource{}
}
