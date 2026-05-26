package diskspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// fakeLister implements FreeSpaceLister (alias of apiv3.FreeSpaceLister)
// for the v5 GetHandler — keeps the test surface narrow.
type fakeLister struct {
	rows []apiv3.DiskSpaceResource
	err  error
}

func (f *fakeLister) GetFreeSpace(_ context.Context) ([]apiv3.DiskSpaceResource, error) {
	return f.rows, f.err
}

// TestGetHandler_ReturnsRowsWithIdenticalV3Shape pins the v5 endpoint's
// wire shape to be byte-equivalent to v3 — same fields, same ordering
// rule, same 200 status code. The handler IS the v3 handler under the
// hood (variable alias) so this is also a regression guard against
// future drift if someone tries to fork.
func TestGetHandler_ReturnsRowsWithIdenticalV3Shape(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{rows: []apiv3.DiskSpaceResource{
		{Path: "/Volumes/Bigger", Label: "Big", FreeSpace: 500_000_000_000, TotalSpace: 2_000_000_000_000},
		{Path: "/", Label: "", FreeSpace: 100_000_000_000, TotalSpace: 250_000_000_000},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/diskspace", nil)
	GetHandler(lister).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
	var got []Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows: got %d, want 2", len(got))
	}
	// OrderBy(Path, OrdinalIgnoreCase) is enforced by the v3 handler —
	// "/" sorts before "/Volumes/Bigger" so the lister's row order
	// should be reversed in the response.
	if got[0].Path != "/" {
		t.Errorf("row[0].Path got %q, want \"/\" (case-insensitive sort)", got[0].Path)
	}
	if got[1].Path != "/Volumes/Bigger" {
		t.Errorf("row[1].Path got %q", got[1].Path)
	}
	if got[1].FreeSpace != 500_000_000_000 {
		t.Errorf("FreeSpace: got %d", got[1].FreeSpace)
	}
}

// TestGetHandler_EmptyReturnsBracketsNotNull pins the empty-DB shape:
// `[]` not `null`. Matches v3 contract (and C# v5 byte-for-byte since
// it's the same handler).
func TestGetHandler_EmptyReturnsBracketsNotNull(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/diskspace", nil)
	GetHandler(&fakeLister{rows: nil}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q, want \"[]\"", got)
	}
}

// TestGetHandler_TypeAliasMatchesV3 is a compile-time regression guard:
// if v5 forks the Resource type away from v3, this assignment fails
// and the build breaks. Keeps the alias invariant explicit.
func TestGetHandler_TypeAliasMatchesV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.DiskSpaceResource{}
	var _ apiv3.DiskSpaceResource = Resource{}
	var _ FreeSpaceLister = (apiv3.FreeSpaceLister)(nil)
}
