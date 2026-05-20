package utorrent_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/utorrent"
)

// utorrentServer is the test scaffolding for the token-cookie auth
// dance + the per-action endpoints. The `/token.html` arm emits the
// HTML token body + a GUID cookie; the `/gui/` arm validates the
// token query param + the cookie replay.
type utorrentServer struct {
	srv *httptest.Server

	token       string
	tokenStatus atomic.Int32 // 0 → 200
	guidCookie  string

	calls          atomic.Int32
	tokenFetches   atomic.Int32
	lastAction     atomic.Value // string
	lastToken      atomic.Value // string
	lastCookieGUID atomic.Value // string
	lastBasicUser  atomic.Value // string

	guiBody      atomic.Value // string
	guiStatus    atomic.Int32 // 0 → 200
	failOnceWith int          // status code emitted once then reset to 200
}

func newUTorrentServer(t *testing.T, token, guiResponseJSON string) *utorrentServer {
	t.Helper()
	h := &utorrentServer{
		token:      token,
		guidCookie: "test-guid-cookie",
	}
	h.guiBody.Store(guiResponseJSON)

	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, _, ok := r.BasicAuth(); ok {
			h.lastBasicUser.Store(u)
		}
		// /gui/token.html arm.
		if strings.HasSuffix(r.URL.Path, "/token.html") {
			h.tokenFetches.Add(1)
			if s := int(h.tokenStatus.Load()); s != 0 {
				w.WriteHeader(s)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "GUID", Value: h.guidCookie})
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<html><div id='token' style='display:none;'>`+h.token+`</div></html>`)
			return
		}
		// /gui/ — per-action endpoint.
		h.calls.Add(1)
		q := r.URL.Query()
		h.lastAction.Store(q.Get("action"))
		h.lastToken.Store(q.Get("token"))
		if c, err := r.Cookie("GUID"); err == nil {
			h.lastCookieGUID.Store(c.Value)
		}
		// One-shot fail injection.
		if h.failOnceWith != 0 {
			code := h.failOnceWith
			h.failOnceWith = 0
			w.WriteHeader(code)
			return
		}
		if s := int(h.guiStatus.Load()); s != 0 {
			w.WriteHeader(s)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, h.guiBody.Load().(string))
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *utorrentServer) settings() utorrent.Settings {
	u, _ := url.Parse(h.srv.URL)
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		n := 0
		for _, r := range p {
			n = n*10 + int(r-'0')
		}
		port = n
	}
	s := utorrent.NewSettings()
	s.Host = u.Hostname()
	s.Port = port
	return s
}

const sampleGetSettingsResponse = `{"build":45828,"settings":[],"label":[]}`

func TestProxy_GetVersionFetchesTokenAndReplays(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "FAKE-TOKEN", sampleGetSettingsResponse)
	p := utorrent.NewProxy(h.srv.Client(), nil)

	build, err := p.GetVersion(context.Background(), h.settings())
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if build != 45828 {
		t.Errorf("build: got %d, want 45828", build)
	}
	if h.tokenFetches.Load() != 1 {
		t.Errorf("token fetches: got %d, want 1", h.tokenFetches.Load())
	}
	if action, _ := h.lastAction.Load().(string); action != "getsettings" {
		t.Errorf("action: got %q, want getsettings", action)
	}
	if tok, _ := h.lastToken.Load().(string); tok != "FAKE-TOKEN" {
		t.Errorf("token replay: got %q, want FAKE-TOKEN", tok)
	}
	if c, _ := h.lastCookieGUID.Load().(string); c != "test-guid-cookie" {
		t.Errorf("GUID cookie replay: got %q, want test-guid-cookie", c)
	}
}

func TestProxy_TokenCachedAcrossCalls(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "FAKE-TOKEN", sampleGetSettingsResponse)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	for i := 0; i < 3; i++ {
		if _, err := p.GetVersion(context.Background(), s); err != nil {
			t.Fatalf("GetVersion %d: %v", i, err)
		}
	}
	if h.tokenFetches.Load() != 1 {
		t.Errorf("token fetched %d times, want 1 (cached)", h.tokenFetches.Load())
	}
	if h.calls.Load() != 3 {
		t.Errorf("gui calls: got %d, want 3", h.calls.Load())
	}
}

func TestProxy_ReauthOnStaleSession(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN-1", sampleGetSettingsResponse)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	if _, err := p.GetVersion(context.Background(), s); err != nil {
		t.Fatalf("first GetVersion: %v", err)
	}
	// Inject a 400 on the next /gui/ call → proxy should re-auth +
	// retry the GET, producing 2 token fetches + 2 successful gui
	// calls.
	h.failOnceWith = http.StatusBadRequest
	h.token = "TOKEN-2"
	if _, err := p.GetVersion(context.Background(), s); err != nil {
		t.Fatalf("post-stale GetVersion: %v", err)
	}
	if h.tokenFetches.Load() != 2 {
		t.Errorf("token fetches: got %d, want 2 (one re-auth)", h.tokenFetches.Load())
	}
	if tok, _ := h.lastToken.Load().(string); tok != "TOKEN-2" {
		t.Errorf("post-reauth token: got %q, want TOKEN-2", tok)
	}
}

func TestProxy_AuthFailedOn401TokenFetch(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", sampleGetSettingsResponse)
	h.tokenStatus.Store(http.StatusUnauthorized)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	_, err := p.GetVersion(context.Background(), h.settings())
	if !errors.Is(err, utorrent.ErrAuthFailed) {
		t.Errorf("expected ErrAuthFailed; got %v", err)
	}
}

func TestProxy_TokenMissingFromBodyErrors(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "", sampleGetSettingsResponse) // empty token
	p := utorrent.NewProxy(h.srv.Client(), nil)
	if _, err := p.GetVersion(context.Background(), h.settings()); err == nil {
		t.Errorf("empty token: want error")
	}
}

func TestProxy_BasicAuthSentOnTokenFetch(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", sampleGetSettingsResponse)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.Username = "sonarr"
	s.Password = "hunter2"
	if _, err := p.GetVersion(context.Background(), s); err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if u, _ := h.lastBasicUser.Load().(string); u != "sonarr" {
		t.Errorf("Basic-Auth user: got %q, want sonarr", u)
	}
}

func TestProxy_UrlBasePreservedInEndpoint(t *testing.T) {
	t.Parallel()
	// Verify the endpoint construction includes the urlBase prefix
	// before /gui/.
	gotPath := atomic.Value{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/token.html") {
			http.SetCookie(w, &http.Cookie{Name: "GUID", Value: "g"})
			_, _ = io.WriteString(w, `<html><div id='token'>tok</div></html>`)
			return
		}
		_, _ = io.WriteString(w, sampleGetSettingsResponse)
	}))
	t.Cleanup(srv.Close)
	p := utorrent.NewProxy(srv.Client(), nil)

	u, _ := url.Parse(srv.URL)
	port := 80
	if pStr := u.Port(); pStr != "" {
		n := 0
		for _, r := range pStr {
			n = n*10 + int(r-'0')
		}
		port = n
	}
	s := utorrent.NewSettings()
	s.Host = u.Hostname()
	s.Port = port
	s.UrlBase = "/utorrent"

	if _, err := p.GetVersion(context.Background(), s); err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	// Last call's path should include /utorrent/gui/.
	if path, _ := gotPath.Load().(string); !strings.HasPrefix(path, "/utorrent/gui/") {
		t.Errorf("endpoint path: got %q, want /utorrent/gui/ prefix", path)
	}
}

// Provider Test() integration — exercises the full auth path
// against a real httptest server.
func TestProvider_TestProbesVersionViaProxy(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", sampleGetSettingsResponse)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	u := utorrent.New(nil, s, p, nil)

	failures, err := u.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test on good build: %+v, want no failures", failures)
	}
}

func TestProvider_TestRejectsBelowMinBuild(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", `{"build":12345}`)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	u := utorrent.New(nil, s, p, nil)
	failures, err := u.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) == 0 || failures[0].PropertyName != "Host" {
		t.Errorf("Test on old build: %+v, want Host failure", failures)
	}
}

func TestProvider_TestNetworkFailureSurfacesHostFailure(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", sampleGetSettingsResponse)
	h.srv.Close()
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	u := utorrent.New(nil, s, p, nil)
	failures, err := u.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) == 0 || failures[0].PropertyName != "Host" {
		t.Errorf("Test on dead host: %+v, want Host failure", failures)
	}
}
