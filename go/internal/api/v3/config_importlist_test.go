package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeImportListConfig struct {
	level string
	tag   int

	setLevelCalls int
	setTagCalls   int
}

func (f *fakeImportListConfig) ListSyncLevel() string { return f.level }
func (f *fakeImportListConfig) ListSyncTag() int      { return f.tag }
func (f *fakeImportListConfig) SetListSyncLevel(_ context.Context, v string) error {
	f.level = v
	f.setLevelCalls++
	return nil
}
func (f *fakeImportListConfig) SetListSyncTag(_ context.Context, v int) error {
	f.tag = v
	f.setTagCalls++
	return nil
}

func TestImportListConfigHandler_Defaults(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListConfigHandler(&fakeImportListConfig{level: "Disabled", tag: 0}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/config/importlist", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got ImportListConfigResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := ImportListConfigResource{ID: 1, ListSyncLevel: "disabled", ListSyncTag: 0}
	if got != want {
		t.Errorf("body: got %+v want %+v", got, want)
	}
}

func TestImportListConfigHandler_KeepAndTagCamelCase(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListConfigHandler(&fakeImportListConfig{level: "KeepAndTag", tag: 42}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/config/importlist", nil))
	var got ImportListConfigResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.ListSyncLevel != "keepAndTag" {
		t.Errorf("camelCase: got %q", got.ListSyncLevel)
	}
	if got.ListSyncTag != 42 {
		t.Errorf("tag: %d", got.ListSyncTag)
	}
}

func putImportListReq(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/importlist/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	return req
}

func TestImportListConfigUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeImportListConfig{}
	rr := httptest.NewRecorder()
	ImportListConfigUpdateHandler(w).ServeHTTP(rr, putImportListReq(`{"id":1,"listSyncLevel":"keepAndUnmonitor","listSyncTag":0}`))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.level != "KeepAndUnmonitor" {
		t.Errorf("ListSyncLevel PascalCase: got %q", w.level)
	}
	if w.setLevelCalls != 1 || w.setTagCalls != 1 {
		t.Errorf("setter call counts: level=%d tag=%d (want 1,1)", w.setLevelCalls, w.setTagCalls)
	}
	var got ImportListConfigResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.ListSyncLevel != "keepAndUnmonitor" {
		t.Errorf("response camelCase: got %q", got.ListSyncLevel)
	}
}

func TestImportListConfigUpdateHandler_KeepAndTagWithoutTag_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListConfigUpdateHandler(&fakeImportListConfig{}).
		ServeHTTP(rr, putImportListReq(`{"id":1,"listSyncLevel":"keepAndTag","listSyncTag":0}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Tag must be specified") {
		t.Errorf("expected C# verbatim message; body=%s", rr.Body.String())
	}
}

func TestImportListConfigUpdateHandler_KeepAndTagWithTag_Success(t *testing.T) {
	t.Parallel()
	w := &fakeImportListConfig{}
	rr := httptest.NewRecorder()
	ImportListConfigUpdateHandler(w).
		ServeHTTP(rr, putImportListReq(`{"id":1,"listSyncLevel":"keepAndTag","listSyncTag":7}`))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.tag != 7 {
		t.Errorf("tag: %d", w.tag)
	}
}

func TestImportListConfigUpdateHandler_UnknownLevel_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListConfigUpdateHandler(&fakeImportListConfig{}).
		ServeHTTP(rr, putImportListReq(`{"id":1,"listSyncLevel":"nope","listSyncTag":0}`))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListConfigUpdateHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListConfigUpdateHandler(&fakeImportListConfig{}).
		ServeHTTP(rr, putImportListReq("not json"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
