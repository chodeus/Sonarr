package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSystemHandler_ListsDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "Series"))
	mustMkdir(t, filepath.Join(root, "Movies"))
	mustWrite(t, filepath.Join(root, "file.txt"), "hi")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/filesystem?path="+root+"/&allowFoldersWithoutTrailingSlashes=true", nil)
	FileSystemHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got FileSystemResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Directories) != 2 {
		t.Fatalf("expected 2 directories; got %d: %+v", len(got.Directories), got)
	}
	// Sorted ascending by Name → Movies, Series.
	if got.Directories[0].Name != "Movies" || got.Directories[1].Name != "Series" {
		t.Errorf("order: %q %q", got.Directories[0].Name, got.Directories[1].Name)
	}
	if len(got.Files) != 0 {
		t.Errorf("default includeFiles=false should hide files; got %+v", got.Files)
	}
}

func TestFileSystemHandler_IncludeFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "video.mkv"), "fake")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/filesystem?path="+root+"/&includeFiles=true&allowFoldersWithoutTrailingSlashes=true", nil)
	FileSystemHandler().ServeHTTP(rr, req)
	var got FileSystemResult
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Files) != 1 || got.Files[0].Name != "video.mkv" || got.Files[0].Extension != ".mkv" {
		t.Errorf("files: %+v", got.Files)
	}
	if got.Files[0].Size != 4 {
		t.Errorf("size: %d", got.Files[0].Size)
	}
}

// TestFileSystemHandler_DirectoryEmitsSizeZero pins the J164 fix —
// directory entries (which never set Size and previously omitted the
// "size" key under `omitempty`) must now emit `"size":0` to match C#'s
// `long Size` non-nullable shape under STJson's WhenWritingNull
// configuration. Catches regressions if a future edit re-adds the
// `omitempty` tag.
func TestFileSystemHandler_DirectoryEmitsSizeZero(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "Series"))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/filesystem?path="+root+"/&allowFoldersWithoutTrailingSlashes=true", nil)
	FileSystemHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	// Raw body check — must contain "size":0 for the directory entry,
	// NOT an omitted "size" key. Decoding into FileSystemModel hides
	// the omitempty vs always-emit distinction (both deserialize back
	// to int64(0)), so assert on the bytes.
	if !contains(rr.Body.String(), `"size":0`) {
		t.Errorf("directory body should contain \"size\":0; got %s", rr.Body.String())
	}
}

func TestFileSystemHandler_SpecialFoldersFiltered(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".Trash"))
	mustMkdir(t, filepath.Join(root, "Normal"))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/filesystem?path="+root+"/&allowFoldersWithoutTrailingSlashes=true", nil)
	FileSystemHandler().ServeHTTP(rr, req)
	var got FileSystemResult
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Directories) != 1 || got.Directories[0].Name != "Normal" {
		t.Errorf(".Trash should be filtered: %+v", got.Directories)
	}
}

func TestFileSystemTypeHandler_File(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := filepath.Join(root, "a.txt")
	mustWrite(t, p, "x")
	rr := httptest.NewRecorder()
	FileSystemTypeHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/filesystem/type?path="+p, nil))
	if rr.Body.String() != `{"type":"file"}` {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestFileSystemTypeHandler_FolderFallback(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	FileSystemTypeHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/filesystem/type?path=/does/not/exist", nil))
	if rr.Body.String() != `{"type":"folder"}` {
		t.Errorf("missing path should fall back to folder; got %q", rr.Body.String())
	}
}

func TestFileSystemMediaFilesHandler_FindsVideos(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "show.mkv"), "video1")
	mustMkdir(t, filepath.Join(root, "Season 01"))
	mustWrite(t, filepath.Join(root, "Season 01", "show.mp4"), "video2")
	mustWrite(t, filepath.Join(root, "ignored.txt"), "no")
	rr := httptest.NewRecorder()
	FileSystemMediaFilesHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/filesystem/mediafiles?path="+root, nil))
	var got []MediaFileResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: %d; %+v", len(got), got)
	}
	// Path-sorted ordering: root/show.mkv then root/Season 01/show.mp4.
	// Lexicographic sort gives "<root>/Season 01/..." before "<root>/show.mkv"
	// only when the path comparison falls there — check both got names.
	names := map[string]bool{}
	for _, m := range got {
		names[m.Name] = true
		if m.RelativePath == "" {
			t.Errorf("relativePath should be non-empty: %+v", m)
		}
	}
	if !names["show.mkv"] || !names["show.mp4"] {
		t.Errorf("expected both videos; got %+v", got)
	}
}

func TestFileSystemMediaFilesHandler_MissingFolder(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	FileSystemMediaFilesHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/filesystem/mediafiles?path=/does/not/exist", nil))
	if rr.Body.String() != `[]` {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}
