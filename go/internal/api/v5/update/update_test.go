package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// fakeLister implements Lister (alias of apiv3.UpdateLister).
type fakeLister struct {
	rows []apiv3.UpdateResource
	err  error
}

func (f *fakeLister) RecentUpdates(_ context.Context) ([]apiv3.UpdateResource, error) {
	return f.rows, f.err
}

func TestGetHandler_ReturnsRowsWithIdenticalV3Shape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	lister := &fakeLister{rows: []apiv3.UpdateResource{
		{
			Version:     "5.0.0.1658",
			Branch:      "main",
			ReleaseDate: now,
			FileName:    "Sonarr-5.0.0.1658.tar.gz",
			URL:         "https://download.sonarr.tv/v5/main/Sonarr-5.0.0.1658.tar.gz",
			Latest:      true,
			Installed:   true,
			Changes:     apiv3.UpdateChanges{New: []string{}, Fixed: []string{}},
			Hash:        "abc123",
		},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/update", nil)
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
	if len(got) != 1 {
		t.Fatalf("rows: got %d, want 1", len(got))
	}
	if got[0].Version != "5.0.0.1658" {
		t.Errorf("version: got %q", got[0].Version)
	}
	if !got[0].Latest || !got[0].Installed {
		t.Errorf("latest/installed flags: got %+v", got[0])
	}
}

func TestGetHandler_EmptyReturnsBracketsNotNull(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/update", nil)
	GetHandler(&fakeLister{rows: nil}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("body: got %q, want \"[]\"", got)
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.UpdateResource{}
	var _ Changes = apiv3.UpdateChanges{}
	var _ apiv3.UpdateResource = Resource{}
	var _ apiv3.UpdateChanges = Changes{}
}
