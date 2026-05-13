package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/rootfolders"
)

type fakeRootFolderLister struct {
	rows []rootfolders.Folder
	err  error
}

func (f *fakeRootFolderLister) AllWithUnmappedFolders(context.Context) ([]rootfolders.Folder, error) {
	return f.rows, f.err
}

func TestRootFolderHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RootFolderHandler(&fakeRootFolderLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestRootFolderHandler_PopulatedShape(t *testing.T) {
	t.Parallel()
	free := int64(1_000_000_000)
	total := int64(2_000_000_000)
	rows := []rootfolders.Folder{
		{
			ID: 1, Path: "/tv/", Accessible: true,
			FreeSpace: &free, TotalSpace: &total,
			UnmappedFolders: []rootfolders.UnmappedFolder{{Name: "Show", Path: "/tv/Show", RelativePath: "Show"}},
		},
		{
			ID: 2, Path: "/movies/", Accessible: false,
			// FreeSpace + TotalSpace nil → omitted
			UnmappedFolders: nil, // → []
		},
	}
	rr := httptest.NewRecorder()
	RootFolderHandler(&fakeRootFolderLister{rows: rows}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`, `"id":2`,
		`"accessible":true`, `"accessible":false`,
		`"freeSpace":1000000000`, `"totalSpace":2000000000`,
		`"unmappedFolders":[{"name":"Show","path":"/tv/Show","relativePath":"Show"}]`,
		`"unmappedFolders":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
	// Path should be cleaned (trailing slash dropped per CleanPath).
	var out []RootFolderResource
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out) != 2 || out[0].Path == "/tv/" {
		t.Errorf("path not cleaned: %+v", out)
	}
	// IsEmpty must NOT appear in body.
	if strings.Contains(body, "isEmpty") {
		t.Errorf("body unexpectedly contains isEmpty: %s", body)
	}
}

func TestRootFolderHandler_NilFreeSpaceOmitted(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RootFolderHandler(&fakeRootFolderLister{rows: []rootfolders.Folder{
		{ID: 1, Path: "/tv", Accessible: true},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder", nil))
	body := rr.Body.String()
	for _, gone := range []string{"freeSpace", "totalSpace"} {
		if strings.Contains(body, gone) {
			t.Errorf("nil %s should be omitted: %s", gone, body)
		}
	}
}

func TestRootFolderHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RootFolderHandler(&fakeRootFolderLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
