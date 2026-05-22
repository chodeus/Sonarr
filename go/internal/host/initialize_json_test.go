package host_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/host"
)

// stubInitializeCfg satisfies host.InitializeJSONConfig with pinned
// return values so the tests can assert wire-shape correctness without
// standing up a real configfile.Provider.
type stubInitializeCfg struct {
	apiKey       string
	urlBase      string
	instanceName string
	branch       string
	theme        string
	apiKeyErr    error
}

func (s stubInitializeCfg) APIKey(context.Context) (string, error) {
	return s.apiKey, s.apiKeyErr
}
func (s stubInitializeCfg) URLBase(context.Context) (string, error)      { return s.urlBase, nil }
func (s stubInitializeCfg) InstanceName(context.Context) (string, error) { return s.instanceName, nil }
func (s stubInitializeCfg) Branch(context.Context) (string, error)       { return s.branch, nil }
func (s stubInitializeCfg) Theme(context.Context) (string, error)        { return s.theme, nil }

func TestInitializeJSON_WireShape(t *testing.T) {
	t.Parallel()
	cfg := stubInitializeCfg{
		apiKey:       "test-api-key-1234567890",
		urlBase:      "",
		instanceName: "Sonarr",
		branch:       "main",
		theme:        "auto",
	}
	srv := httptest.NewServer(host.InitializeJSONHandler(cfg))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q, want application/json prefix", ct)
	}

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Field names + types that the React app reads from window.Sonarr
	// after assigning the parsed JSON. Verified against
	// frontend/src/index.ts + downstream usage.
	checks := []struct {
		key  string
		want any
	}{
		{"apiRoot", "/api/v3"},
		{"apiKey", "test-api-key-1234567890"},
		{"version", "dev"},
		{"instanceName", "Sonarr"},
		{"theme", "auto"},
		{"branch", "main"},
		{"release", "main"}, // mirrors branch in the Go-port
		{"urlBase", ""},
		{"analytics", false},
		{"isProduction", false}, // "dev" version → not production
	}
	for _, c := range checks {
		if got[c.key] != c.want {
			t.Errorf("%s: got %v (%T), want %v", c.key, got[c.key], got[c.key], c.want)
		}
	}
	if uh, ok := got["userHash"].(string); !ok || len(uh) != 64 {
		t.Errorf("userHash: got %v, want 64-char hex SHA256", got["userHash"])
	}
}

func TestInitializeJSON_URLBaseInApiRoot(t *testing.T) {
	t.Parallel()
	cfg := stubInitializeCfg{
		apiKey:       "k",
		urlBase:      "/sonarr",
		instanceName: "Sonarr",
		branch:       "main",
		theme:        "dark",
	}
	srv := httptest.NewServer(host.InitializeJSONHandler(cfg))
	defer srv.Close()

	resp, _ := http.Get(srv.URL)
	defer func() { _ = resp.Body.Close() }()
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["apiRoot"] != "/sonarr/api/v3" {
		t.Errorf("apiRoot with url-base: got %v, want /sonarr/api/v3", got["apiRoot"])
	}
	if got["urlBase"] != "/sonarr" {
		t.Errorf("urlBase: got %v, want /sonarr", got["urlBase"])
	}
}

func TestInitializeJSON_APIKeyError_Returns500(t *testing.T) {
	t.Parallel()
	cfg := stubInitializeCfg{apiKeyErr: errAPIKey{}}
	srv := httptest.NewServer(host.InitializeJSONHandler(cfg))
	defer srv.Close()
	resp, _ := http.Get(srv.URL)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500 on APIKey error", resp.StatusCode)
	}
}

type errAPIKey struct{}

func (errAPIKey) Error() string { return "stub APIKey error" }
