package update_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/update"
)

type stubBranch struct{ value string }

func (s stubBranch) Branch(_ context.Context) string { return s.value }

func TestNewService_RequiredArgs(t *testing.T) {
	t.Parallel()
	if _, err := update.NewService(nil, stubBranch{}, "1.0", "", 0); err == nil {
		t.Errorf("nil doer: want error")
	}
	if _, err := update.NewService(http.DefaultClient, nil, "1.0", "", 0); err == nil {
		t.Errorf("nil branch: want error")
	}
	if _, err := update.NewService(http.DefaultClient, stubBranch{}, "  ", "", 0); err == nil {
		t.Errorf("empty version: want error")
	}
}

func TestAvailableUpdate_PackageReturned(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/update/main") {
			t.Errorf("URL path: got %q, want /update/main", r.URL.Path)
		}
		if r.URL.Query().Get("version") != "5.0.1" {
			t.Errorf("version param: %q", r.URL.Query().Get("version"))
		}
		if r.URL.Query().Get("runtime") != "netcore" {
			t.Errorf("runtime param: %q", r.URL.Query().Get("runtime"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"available": true,
			"updatePackage": map[string]any{
				"version":     "5.0.42",
				"releaseDate": "2026-05-15T12:00:00Z",
				"fileName":    "Sonarr.5.0.42.zip",
				"url":         "https://download.example/Sonarr.zip",
				"hash":        "deadbeef",
				"branch":      "main",
			},
		})
	}))
	t.Cleanup(srv.Close)

	svc, err := update.NewService(srv.Client(), stubBranch{value: "main"}, "5.0.1", srv.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	pkg, err := svc.AvailableUpdate(context.Background())
	if err != nil {
		t.Fatalf("AvailableUpdate: %v", err)
	}
	if pkg == nil {
		t.Fatal("AvailableUpdate: nil pkg, want non-nil")
	}
	if pkg.Version != "5.0.42" {
		t.Errorf("Version: got %q, want 5.0.42", pkg.Version)
	}
	if pkg.Branch != "main" {
		t.Errorf("Branch: got %q, want main", pkg.Branch)
	}
}

func TestAvailableUpdate_NotAvailable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"available": false})
	}))
	t.Cleanup(srv.Close)

	svc, _ := update.NewService(srv.Client(), stubBranch{value: "main"}, "5.0.42", srv.URL, 0)
	pkg, err := svc.AvailableUpdate(context.Background())
	if err != nil {
		t.Fatalf("AvailableUpdate: unexpected err: %v", err)
	}
	if pkg != nil {
		t.Errorf("AvailableUpdate: got %+v, want nil (no update available)", pkg)
	}
}

func TestAvailableUpdate_EmptyBranchFallsBackToMain(t *testing.T) {
	t.Parallel()
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"available": false})
	}))
	t.Cleanup(srv.Close)

	svc, _ := update.NewService(srv.Client(), stubBranch{value: ""}, "5.0.1", srv.URL, 0)
	if _, err := svc.AvailableUpdate(context.Background()); err != nil {
		t.Fatalf("AvailableUpdate: %v", err)
	}
	if !strings.Contains(seenPath, "/update/main") {
		t.Errorf("empty branch should fall back to 'main'; got path %q", seenPath)
	}
}

func TestAvailableUpdate_Non2xxErrors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream broken"))
	}))
	t.Cleanup(srv.Close)

	svc, _ := update.NewService(srv.Client(), stubBranch{value: "main"}, "5.0.1", srv.URL, 0)
	if _, err := svc.AvailableUpdate(context.Background()); err == nil {
		t.Errorf("AvailableUpdate on 500: want error")
	}
}

func TestAvailableUpdate_MalformedBodyErrors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	svc, _ := update.NewService(srv.Client(), stubBranch{value: "main"}, "5.0.1", srv.URL, 0)
	if _, err := svc.AvailableUpdate(context.Background()); err == nil {
		t.Errorf("malformed body: want error")
	}
}
