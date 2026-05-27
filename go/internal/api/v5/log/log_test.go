package log

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// fakeFileLister implements both FileLister and UpdateFileLister via
// duck-typing — production logFileLister works the same way.
type fakeFileLister struct {
	folder       string
	updateFolder string
	urlBase      string
}

func (f *fakeFileLister) LogFolder() string       { return f.folder }
func (f *fakeFileLister) UpdateLogFolder() string { return f.updateFolder }
func (f *fakeFileLister) URLBase() string         { return f.urlBase }

// ----- GET /log — empty paged envelope -----

func TestLogHandler_EmptyCorpus_Returns200PagedEmpty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log", nil)
	LogHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// Empty paged envelope: records `[]`, sortKey rewritten to "time"
	// per the v3 handler's `id` → `time` response remap (matches C# v5
	// at LogController.cs:73).
	if !strings.Contains(body, `"records":[]`) {
		t.Errorf("body should contain `\"records\":[]`; got %q", body)
	}
	if !strings.Contains(body, `"sortKey":"time"`) {
		t.Errorf("sortKey should be rewritten to `time`; got %q", body)
	}
}

// ----- GET /log/file — empty folder returns `[]` -----

func TestFileListHandler_NonexistentFolder_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	svc := &fakeFileLister{folder: "/nonexistent/folder/xyz"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log/file", nil)
	FileListHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "[]" {
		t.Errorf("body: got %q want `[]`", body)
	}
}

// ----- GET /log/file/{filename} — 404 for missing file -----

func TestFileByNameHandler_Missing_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeFileLister{folder: "/nonexistent/folder/xyz"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log/file/foo.txt", nil)
	req.SetPathValue("filename", "foo.txt")
	FileByNameHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestFileByNameHandler_InvalidFilename_Returns400(t *testing.T) {
	t.Parallel()
	// Path-traversal protection: filename must match `^[-.a-zA-Z0-9]+\.txt$`.
	svc := &fakeFileLister{folder: "/tmp"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log/file/../etc/passwd", nil)
	req.SetPathValue("filename", "../etc/passwd")
	FileByNameHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (filename regex); got body=%q", rr.Code, rr.Body.String())
	}
}

// ----- GET /log/file/update — empty folder returns `[]` -----

func TestUpdateFileListHandler_NonexistentFolder_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	svc := &fakeFileLister{updateFolder: "/nonexistent/update/folder/xyz"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log/file/update", nil)
	UpdateFileListHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "[]" {
		t.Errorf("body: got %q want `[]`", body)
	}
}

// ----- alias guards -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	if FileListHandler == nil || FileByNameHandler == nil {
		t.Error("handler aliases must be non-nil")
	}
	if UpdateFileListHandler == nil || UpdateFileByNameHandler == nil {
		t.Error("update handler aliases must be non-nil")
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ FileResource = apiv3.LogFileResource{}
	var _ apiv3.LogFileResource = FileResource{}
	var _ FileLister = (apiv3.LogFileLister)(nil)
	var _ UpdateFileLister = (apiv3.UpdateLogFileLister)(nil)
}

// ----- v5 LogResource shape pin -----
//
// v5 omits the `method` field (per `src/Sonarr.Api.V5/Logs/LogResource.cs`).
// Pin the struct shape so a future field add doesn't silently widen
// the v5 wire to match v3.

func TestResource_OmitsMethodField(t *testing.T) {
	t.Parallel()
	rt := reflect.TypeOf(Resource{})
	for i := 0; i < rt.NumField(); i++ {
		if name := rt.Field(i).Name; name == "Method" {
			t.Fatalf("v5 Resource must not declare a Method field (got %q at index %d)", name, i)
		}
	}
	wantNames := []string{"ID", "Time", "Exception", "ExceptionType", "Level", "Logger", "Message"}
	if got := rt.NumField(); got != len(wantNames) {
		t.Fatalf("v5 Resource field count: got %d, want %d", got, len(wantNames))
	}
	for i, name := range wantNames {
		if got := rt.Field(i).Name; got != name {
			t.Errorf("field %d: got %q, want %q", i, got, name)
		}
	}
}

func TestResource_JSON_OmitsMethodKey(t *testing.T) {
	t.Parallel()
	buf, err := json.Marshal(Resource{ID: 7, Time: "2026-01-02T03:04:05Z", Level: "info", Logger: "Sonarr.Test", Message: "hello"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(buf), `"method"`) {
		t.Errorf("v5 Resource JSON must not contain `method`; got %s", buf)
	}
}

// ----- stripMethod wrapper -----

func TestStripMethod_RemovesMethodKeyFromEachRecord(t *testing.T) {
	t.Parallel()
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"page":1,"pageSize":10,"sortKey":"time","sortDirection":"descending","totalRecords":2,"records":[` +
			`{"id":1,"time":"t1","level":"info","logger":"a","message":"m1","method":"OldMethod"},` +
			`{"id":2,"time":"t2","level":"warn","logger":"b","message":"m2","method":"AnotherMethod"}]}`))
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log", nil)
	stripMethod(upstream).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, `"method"`) {
		t.Errorf("response must not contain `method` after strip; got %q", body)
	}
	if !strings.Contains(body, `"id":1`) || !strings.Contains(body, `"id":2`) {
		t.Errorf("response must preserve other record fields; got %q", body)
	}
	if !strings.Contains(body, `"records":[`) {
		t.Errorf("response must keep records envelope; got %q", body)
	}
}

func TestStripMethod_EmptyRecords_NoOp(t *testing.T) {
	t.Parallel()
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"page":1,"pageSize":10,"sortKey":"time","sortDirection":"descending","totalRecords":0,"records":[]}`))
	})
	rr := httptest.NewRecorder()
	stripMethod(upstream).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/log", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"records":[]`) {
		t.Errorf("empty records envelope must pass through; got %q", rr.Body.String())
	}
}

func TestStripMethod_NonOKStatus_PassesThrough(t *testing.T) {
	t.Parallel()
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom","method":"PreservedOnError"}`))
	})
	rr := httptest.NewRecorder()
	stripMethod(upstream).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/log", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rr.Code)
	}
	// Error envelope must pass through unchanged — including `method` key
	// if upstream emitted one (the wrapper only rewrites 200 paged shapes).
	if !strings.Contains(rr.Body.String(), `"method":"PreservedOnError"`) {
		t.Errorf("error envelope must pass through verbatim; got %q", rr.Body.String())
	}
}

func TestStripMethod_NonObjectBody_PassesThrough(t *testing.T) {
	t.Parallel()
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`null`))
	})
	rr := httptest.NewRecorder()
	stripMethod(upstream).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/log", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if rr.Body.String() != `null` {
		t.Errorf("non-object body must pass through verbatim; got %q", rr.Body.String())
	}
}

func TestStripMethod_NoRecordsKey_PassesThrough(t *testing.T) {
	t.Parallel()
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"page":1,"method":"keep"}`))
	})
	rr := httptest.NewRecorder()
	stripMethod(upstream).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/log", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	// No records[] key → no rewrite; envelope-level keys pass through.
	if !strings.Contains(rr.Body.String(), `"method":"keep"`) {
		t.Errorf("envelope-level keys must pass through when records[] absent; got %q", rr.Body.String())
	}
}
