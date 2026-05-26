package filesystem

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestListHandler_ServesV3Shape pins the v5 endpoint to the v3 wire
// contract — directories + files projected as `FileSystemResult` with
// the same field ordering, content-type, status.
func TestListHandler_ServesV3Shape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/filesystem?path="+dir+string(filepath.Separator)+"&includeFiles=true&allowFoldersWithoutTrailingSlashes=true", nil)
	ListHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got FileSystemResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Directories) != 1 || got.Directories[0].Name != "sub" {
		t.Errorf("directories: got %+v, want [{Name:sub}]", got.Directories)
	}
	if len(got.Files) != 1 || got.Files[0].Name != "x.mkv" {
		t.Errorf("files: got %+v, want [{Name:x.mkv}]", got.Files)
	}
}

// TestTypeHandler_FileReturnsFile pins the file-detection branch.
func TestTypeHandler_FileReturnsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/filesystem/type?path="+f, nil)
	TypeHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	if got := rr.Body.String(); got != `{"type":"file"}` {
		t.Errorf("body: got %q, want {\"type\":\"file\"}", got)
	}
}

// TestTypeHandler_NonExistentReturnsFolder pins the folder-default
// branch — C# `:35-46` says folder is the safe default to avoid
// leaking existence information to the UI.
func TestTypeHandler_NonExistentReturnsFolder(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/filesystem/type?path=/__definitely_not_a_real_path__", nil)
	TypeHandler().ServeHTTP(rr, req)
	if got := rr.Body.String(); got != `{"type":"folder"}` {
		t.Errorf("body: got %q, want {\"type\":\"folder\"} (folder default)", got)
	}
}

// TestMediaFilesHandler_FiltersVideoExtensions — drop a .mkv + .txt
// into a tmpdir; mediafiles endpoint should return only the .mkv.
func TestMediaFilesHandler_FiltersVideoExtensions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkv := filepath.Join(dir, "v.mkv")
	txt := filepath.Join(dir, "v.txt")
	for _, p := range []string{mkv, txt} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/filesystem/mediafiles?path="+dir, nil)
	MediaFilesHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var got []MediaFileResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "v.mkv" {
		t.Errorf("got %+v, want one entry named v.mkv (the .txt should be filtered out)", got)
	}
}

// TestMediaFilesHandler_MissingFolderReturnsEmptyArray pins the
// `Array.Empty<string>()` short-circuit when the target folder
// doesn't exist (C# `:50-53`).
func TestMediaFilesHandler_MissingFolderReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/filesystem/mediafiles?path=/__definitely_not_a_real_path__", nil)
	MediaFilesHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("body: got %q, want \"[]\"", got)
	}
}

// TestTypeAliasesMatchV3 — compile-time invariant guard per J251 pattern.
// If a future slice forks any of these types away from v3, the build
// breaks here rather than silently drifting at runtime.
func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ FileSystemModel = apiv3.FileSystemModel{}
	var _ FileSystemResult = apiv3.FileSystemResult{}
	var _ MediaFileResult = apiv3.MediaFileResult{}
	var _ apiv3.FileSystemModel = FileSystemModel{}
	var _ apiv3.FileSystemResult = FileSystemResult{}
	var _ apiv3.MediaFileResult = MediaFileResult{}
}
