package sonarr_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/importlists/sonarr"
)

// Proxy round-trip via httptest.Server. NO live network — every
// per-port test convention.

func newTestServer(t *testing.T, handler http.HandlerFunc) (*sonarr.Proxy, *sonarr.Settings, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	settings := &sonarr.Settings{BaseURL: srv.URL, APIKey: "test-key"}
	proxy := sonarr.NewProxy(srv.Client())
	return proxy, settings, srv.Close
}

func TestProxy_GetSeries_HappyPath(t *testing.T) {
	t.Parallel()
	proxy, settings, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/series" {
			t.Errorf("path: got %q want /api/v3/series", r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("X-Api-Key: got %q", r.Header.Get("X-Api-Key"))
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept: got %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`[{"title":"Show A","tvdbId":111,"qualityProfileId":1,"languageProfileId":2,"rootFolderPath":"/mnt/tv","tags":[5,7],"seasons":[{"seasonNumber":1,"monitored":true}]}]`))
	})
	defer cleanup()

	got, err := proxy.GetSeries(context.Background(), settings)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("series count: got %d want 1", len(got))
	}
	if got[0].Title != "Show A" || got[0].TvdbID != 111 || got[0].QualityProfileID != 1 ||
		got[0].LanguageProfileID != 2 || got[0].RootFolderPath != "/mnt/tv" {
		t.Errorf("series: %+v", got[0])
	}
	if len(got[0].Tags) != 2 || got[0].Tags[0] != 5 || got[0].Tags[1] != 7 {
		t.Errorf("Tags: %v", got[0].Tags)
	}
	if len(got[0].Seasons) != 1 || got[0].Seasons[0].SeasonNumber != 1 || !got[0].Seasons[0].Monitored {
		t.Errorf("Seasons: %+v", got[0].Seasons)
	}
}

func TestProxy_GetSeries_EmptySettingsReturnsEmpty(t *testing.T) {
	t.Parallel()
	// No server — should not even attempt a request.
	proxy := sonarr.NewProxy(http.DefaultClient)
	got, err := proxy.GetSeries(context.Background(), &sonarr.Settings{BaseURL: "", APIKey: ""})
	if err != nil || len(got) != 0 {
		t.Errorf("expected []+nil, got %v / %v", got, err)
	}
}

func TestProxy_GetSeries_Non2xx(t *testing.T) {
	t.Parallel()
	proxy, settings, cleanup := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	})
	defer cleanup()

	_, err := proxy.GetSeries(context.Background(), settings)
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if !errors.Is(err, sonarr.ErrRequestFailed) {
		t.Errorf("err: %v want wraps ErrRequestFailed", err)
	}
}

func TestProxy_BaseURL_TrailingSlash(t *testing.T) {
	t.Parallel()
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	proxy := sonarr.NewProxy(srv.Client())
	settings := &sonarr.Settings{BaseURL: srv.URL + "/", APIKey: "k"}
	if _, err := proxy.GetSeries(context.Background(), settings); err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if seen != "/api/v3/series" {
		t.Errorf("path: got %q want /api/v3/series (trailing slash should be trimmed)", seen)
	}
}

func TestProxy_Test_Success(t *testing.T) {
	t.Parallel()
	proxy, settings, cleanup := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	defer cleanup()
	failures := proxy.Test(context.Background(), settings)
	if len(failures) != 0 {
		t.Errorf("expected no failures, got %+v", failures)
	}
}

func TestProxy_Test_Unauthorized(t *testing.T) {
	t.Parallel()
	proxy, settings, cleanup := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	defer cleanup()
	failures := proxy.Test(context.Background(), settings)
	if len(failures) != 1 || failures[0].PropertyName != "ApiKey" {
		t.Errorf("expected ApiKey failure, got %+v", failures)
	}
}

func TestProxy_Test_Redirect(t *testing.T) {
	t.Parallel()
	// Use a custom client that does NOT follow redirects, so we
	// observe a 3xx response directly (matches the C#
	// HttpClient configured with AllowAutoRedirect=false).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "https://other.example/", http.StatusFound)
	}))
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	proxy := sonarr.NewProxy(client)
	settings := &sonarr.Settings{BaseURL: srv.URL, APIKey: "k"}

	failures := proxy.Test(context.Background(), settings)
	if len(failures) != 1 || failures[0].PropertyName != "BaseUrl" {
		t.Errorf("expected BaseUrl failure, got %+v", failures)
	}
}

func TestProxy_Test_NetworkError(t *testing.T) {
	t.Parallel()
	// Build a proxy pointed at a closed server to force a transport
	// error.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	proxy := sonarr.NewProxy(http.DefaultClient)
	settings := &sonarr.Settings{BaseURL: url, APIKey: "k"}
	failures := proxy.Test(context.Background(), settings)
	if len(failures) != 1 {
		t.Fatalf("expected one failure, got %d", len(failures))
	}
	if failures[0].PropertyName != "" || !strings.Contains(failures[0].ErrorMessage, "Unable to connect") {
		t.Errorf("expected generic 'Unable to connect' failure, got %+v", failures[0])
	}
}
