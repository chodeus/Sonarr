package v3

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type fakeMediaCoverLister struct{ folder string }

func (f fakeMediaCoverLister) MediaCoverFolder() string { return f.folder }

func writeMediaCover(t *testing.T, root string, seriesID int, name, content string) {
	t.Helper()
	dir := filepath.Join(root, "1")
	if seriesID != 1 {
		dir = filepath.Join(root, "0")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func mediaCoverReq(seriesID, filename string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v3/mediacover/"+seriesID+"/"+filename, nil)
	req.SetPathValue("seriesId", seriesID)
	req.SetPathValue("filename", filename)
	return req
}

func TestMediaCoverHandler_Found(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMediaCover(t, root, 1, "poster.jpg", "JPEG_BYTES")
	rr := httptest.NewRecorder()
	MediaCoverHandler(fakeMediaCoverLister{folder: root}).ServeHTTP(rr, mediaCoverReq("1", "poster.jpg"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("Content-Type: %q", rr.Header().Get("Content-Type"))
	}
	if rr.Body.String() != "JPEG_BYTES" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestMediaCoverHandler_ResizedFallbackToBase(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// Only base file exists; resized variant is missing → fallback to base.
	writeMediaCover(t, root, 1, "poster.jpg", "BASE")
	rr := httptest.NewRecorder()
	MediaCoverHandler(fakeMediaCoverLister{folder: root}).ServeHTTP(rr, mediaCoverReq("1", "poster-500.jpg"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "BASE" {
		t.Errorf("fallback content: %q", rr.Body.String())
	}
}

func TestMediaCoverHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rr := httptest.NewRecorder()
	MediaCoverHandler(fakeMediaCoverLister{folder: root}).ServeHTTP(rr, mediaCoverReq("1", "missing.jpg"))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMediaCoverHandler_ZeroSizeIs404(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMediaCover(t, root, 1, "poster.png", "") // zero bytes
	rr := httptest.NewRecorder()
	MediaCoverHandler(fakeMediaCoverLister{folder: root}).ServeHTTP(rr, mediaCoverReq("1", "poster.png"))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMediaCoverHandler_BadSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	MediaCoverHandler(fakeMediaCoverLister{folder: "/tmp"}).ServeHTTP(rr, mediaCoverReq("abc", "poster.jpg"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMediaCoverHandler_BadFilename_400(t *testing.T) {
	t.Parallel()
	cases := []string{"poster", "poster.txt", "../etc/passwd", ""}
	for _, fn := range cases {
		rr := httptest.NewRecorder()
		MediaCoverHandler(fakeMediaCoverLister{folder: "/tmp"}).ServeHTTP(rr, mediaCoverReq("1", fn))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("filename %q: status %d", fn, rr.Code)
		}
	}
}

func TestMediaCoverContentType(t *testing.T) {
	t.Parallel()
	cases := []struct{ path, want string }{
		{"x.jpg", "image/jpeg"},
		{"x.JPG", "image/jpeg"},
		{"x.png", "image/png"},
		{"x.gif", "image/gif"},
		{"x.bin", "application/octet-stream"},
	}
	for _, c := range cases {
		if got := mediaCoverContentType(c.path); got != c.want {
			t.Errorf("mediaCoverContentType(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
