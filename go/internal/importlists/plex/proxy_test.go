package plex_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/importlists/plex"
)

// newTestProxy builds a plex.Proxy whose three plex.tv hosts all
// point at a single httptest.Server. The handler can branch on
// r.URL.Path to differentiate the endpoints.
func newTestProxy(t *testing.T, handler http.HandlerFunc) *plex.Proxy {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return plex.NewProxy(srv.Client(), "test-cid", "TestApp", "v1.2.3",
		plex.WithHosts(srv.URL, srv.URL, srv.URL))
}

func TestProxy_GetPinUrl(t *testing.T) {
	t.Parallel()
	proxy := newTestProxy(t, func(http.ResponseWriter, *http.Request) {})

	got := proxy.GetPinUrl(context.Background())
	if !got.Poll {
		t.Errorf("Poll: false want true")
	}
	if !strings.Contains(got.URL, "/api/v2/pins") {
		t.Errorf("URL: %q does not contain /api/v2/pins", got.URL)
	}
	if !strings.Contains(got.URL, "X-Plex-Client-Identifier=test-cid") {
		t.Errorf("URL: missing X-Plex-Client-Identifier=test-cid: %s", got.URL)
	}
	if !strings.Contains(got.URL, "strong=true") {
		t.Errorf("URL: missing strong=true: %s", got.URL)
	}
	if got.Headers["Accept"] != "application/json" {
		t.Errorf("Headers Accept: %v", got.Headers)
	}
}

func TestProxy_GetSignInUrl(t *testing.T) {
	t.Parallel()
	proxy := newTestProxy(t, func(http.ResponseWriter, *http.Request) {})

	got := proxy.GetSignInUrl(context.Background(), "http://localhost:8989/cb", 42, "ABCD")
	if got.PinID != 42 {
		t.Errorf("PinID: %d want 42", got.PinID)
	}
	if !strings.Contains(got.OauthURL, "/auth/hashBang/#!?") {
		t.Errorf("OauthURL: missing /auth/hashBang/#!: %s", got.OauthURL)
	}
	if !strings.Contains(got.OauthURL, "clientID=test-cid") {
		t.Errorf("OauthURL: missing clientID: %s", got.OauthURL)
	}
	if !strings.Contains(got.OauthURL, "code=ABCD") {
		t.Errorf("OauthURL: missing code=ABCD: %s", got.OauthURL)
	}
}

func TestProxy_GetAuthToken_Success(t *testing.T) {
	t.Parallel()
	proxy := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v2/pins/") {
			t.Errorf("path: %q", r.URL.Path)
		}
		if r.URL.Query().Get("X-Plex-Client-Identifier") != "test-cid" {
			t.Errorf("query missing clientID")
		}
		_, _ = w.Write([]byte(`{"authToken":"abc-123"}`))
	})

	token, err := proxy.GetAuthToken(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}
	if token != "abc-123" {
		t.Errorf("token: %q want abc-123", token)
	}
}

func TestProxy_GetAuthToken_EmptyResponse(t *testing.T) {
	t.Parallel()
	proxy := newTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	token, err := proxy.GetAuthToken(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}
	if token != "" {
		t.Errorf("token: %q want empty", token)
	}
}

func TestProxy_GetAuthToken_BadJSON(t *testing.T) {
	t.Parallel()
	proxy := newTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	})
	// C# TryDeserialize fallback → empty token, nil err.
	token, err := proxy.GetAuthToken(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}
	if token != "" {
		t.Errorf("token: %q want empty", token)
	}
}

func TestProxy_GetWatchlist_Success(t *testing.T) {
	t.Parallel()
	proxy := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/sections/watchlist/all" {
			t.Errorf("path: %q", r.URL.Path)
		}
		if r.URL.Query().Get("X-Plex-Token") != "user-tok" {
			t.Errorf("X-Plex-Token missing")
		}
		if r.URL.Query().Get("X-Plex-Container-Size") != "100" {
			t.Errorf("size: %q", r.URL.Query().Get("X-Plex-Container-Size"))
		}
		if r.URL.Query().Get("X-Plex-Container-Start") != "200" {
			t.Errorf("start: %q", r.URL.Query().Get("X-Plex-Container-Start"))
		}
		if r.URL.Query().Get("type") != "2" {
			t.Errorf("type: %q want 2", r.URL.Query().Get("type"))
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"r1","title":"X","year":2020,"Guid":[{"id":"tvdb://1"}]}]}}`))
	})

	c, err := proxy.GetWatchlist(context.Background(), "user-tok", 100, 200)
	if err != nil {
		t.Fatalf("GetWatchlist: %v", err)
	}
	if len(c.Items) != 1 || c.Items[0].Title != "X" {
		t.Errorf("items: %+v", c.Items)
	}
}

func TestProxy_GetWatchlist_Non2xx(t *testing.T) {
	t.Parallel()
	proxy := newTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := proxy.GetWatchlist(context.Background(), "t", 100, 0)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !errors.Is(err, plex.ErrRequestFailed) {
		t.Errorf("err wraps: %v want ErrRequestFailed", err)
	}
}

func TestProxy_FallbackClientID(t *testing.T) {
	t.Parallel()
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query().Get("X-Plex-Client-Identifier")
		_, _ = w.Write([]byte(`{"authToken":""}`))
	}))
	t.Cleanup(srv.Close)

	proxy := plex.NewProxy(srv.Client(), "", "", "",
		plex.WithHosts(srv.URL, srv.URL, srv.URL))
	if _, err := proxy.GetAuthToken(context.Background(), 1); err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}
	if seen != "sonarr" {
		t.Errorf("clientID fallback: got %q want sonarr", seen)
	}
}
