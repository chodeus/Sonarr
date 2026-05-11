package transmission_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/transmission"
)

// ============================================================================
// Settings.Validate — ports rules from
// `Download/Clients/Transmission/TransmissionSettings.cs:10-25`.
// ============================================================================

func TestSettings_DefaultsValid(t *testing.T) {
	t.Parallel()
	s := transmission.NewSettings()
	got := s.Validate()
	if len(got) != 0 {
		t.Errorf("Validate = %+v, want no failures", got)
	}
}

func TestSettings_PortOutOfRange(t *testing.T) {
	t.Parallel()
	for _, port := range []int{0, -1, 65536, 99999} {
		s := transmission.NewSettings()
		s.Port = port
		got := s.Validate()
		found := false
		for _, f := range got {
			if f.PropertyName == "Port" {
				found = true
			}
		}
		if !found {
			t.Errorf("Validate(Port=%d) failures = %+v, want Port failure", port, got)
		}
	}
}

func TestSettings_UrlBaseMustStartWithSlash(t *testing.T) {
	t.Parallel()
	s := transmission.NewSettings()
	s.UrlBase = "transmission"
	got := s.Validate()
	found := false
	for _, f := range got {
		if f.PropertyName == "UrlBase" {
			found = true
		}
	}
	if !found {
		t.Errorf("Validate failures = %+v, want UrlBase failure", got)
	}
}

func TestSettings_CategoryCharRestriction(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"tv sonarr", "tv_sonarr", "tv/sonarr", "tv1"} {
		s := transmission.NewSettings()
		s.TvCategory = c
		got := s.Validate()
		found := false
		for _, f := range got {
			if f.PropertyName == "TvCategory" {
				found = true
			}
		}
		if !found {
			t.Errorf("Validate(TvCategory=%q) failures = %+v, want TvCategory failure", c, got)
		}
	}
}

func TestSettings_CategoryAllowsDotPrefix(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"tv-sonarr", ".tv-sonarr", "TV-SONARR", ""} {
		s := transmission.NewSettings()
		s.TvCategory = c
		got := s.Validate()
		for _, f := range got {
			if f.PropertyName == "TvCategory" {
				t.Errorf("Validate(TvCategory=%q) reported failure %+v (allowed format)", c, f)
			}
		}
	}
}

func TestSettings_CategoryAndDirectoryMutuallyExclusive(t *testing.T) {
	t.Parallel()
	s := transmission.NewSettings()
	s.TvCategory = "tv-sonarr"
	s.TvDirectory = "/data/downloads"
	got := s.Validate()
	count := 0
	for _, f := range got {
		if f.PropertyName == "TvCategory" {
			count++
		}
	}
	if count == 0 {
		t.Errorf("Validate failures = %+v, want TvCategory mutually-exclusive failure", got)
	}
}

// ============================================================================
// Proxy.BuildBaseURL — ports `TransmissionProxy.GetBaseUrl :245-248`.
// ============================================================================

func TestProxy_BuildBaseURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		settings transmission.Settings
		want     string
	}{
		{
			name:     "default localhost",
			settings: transmission.Settings{Host: "localhost", Port: 9091, UrlBase: "/transmission/"},
			want:     "http://localhost:9091/transmission/rpc",
		},
		{
			name:     "tls + no urlbase",
			settings: transmission.Settings{Host: "host", Port: 443, UseSsl: true},
			want:     "https://host:443/rpc",
		},
		{
			name:     "urlbase without leading slash gets one",
			settings: transmission.Settings{Host: "h", Port: 9091, UrlBase: "t/"},
			want:     "http://h:9091/t/rpc",
		},
	}
	p, err := transmission.NewProxy(http.DefaultClient, slog.Default())
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := p.BuildBaseURL(c.settings); got != c.want {
				t.Errorf("BuildBaseURL = %q, want %q", got, c.want)
			}
		})
	}
}

// ============================================================================
// Proxy.AddTorrentFromURL — Transmission session-id auth flow +
// torrent-add → hashString extraction.
// ============================================================================

func TestProxy_AddTorrentFromURLEndToEnd(t *testing.T) {
	t.Parallel()
	const sessionID = "test-session-abc"
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		if r.URL.Path != "/transmission/rpc" {
			t.Errorf("path = %q, want /transmission/rpc", r.URL.Path)
		}
		incoming := r.Header.Get("X-Transmission-Session-Id")
		// First call: no session ID present → reply 409 with the
		// session header. Second call: echo verifies + return success.
		if incoming != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var reqBody struct {
			Method    string         `json:"method"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if reqBody.Method != "torrent-add" {
			t.Errorf("method = %q, want torrent-add", reqBody.Method)
		}
		if reqBody.Arguments["filename"] != "magnet:?xt=urn:btih:DEADBEEF" {
			t.Errorf("filename = %v, want magnet", reqBody.Arguments["filename"])
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"torrent-added":{"hashString":"DEADBEEF","name":"x"}}}`))
	}))
	defer srv.Close()

	settings := transmission.Settings{
		Host: stripScheme(srv.URL), Port: portOf(srv.URL),
		UrlBase: "/transmission/",
	}
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())

	hash, err := p.AddTorrentFromURL(context.Background(), "magnet:?xt=urn:btih:DEADBEEF", "", settings)
	if err != nil {
		t.Fatalf("AddTorrentFromURL: %v", err)
	}
	if hash != "DEADBEEF" {
		t.Errorf("hash = %q, want DEADBEEF", hash)
	}
	// Two HTTP roundtrips expected: the auth probe (409) and the
	// real request (200).
	if callCount.Load() < 2 {
		t.Errorf("callCount = %d, want >= 2 (auth probe + request)", callCount.Load())
	}
}

func TestProxy_AddTorrentFromDataBase64Encodes(t *testing.T) {
	t.Parallel()
	const sessionID = "sess-id"
	rawTorrent := []byte("d8:announce-list... binary torrent ...")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method    string         `json:"method"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(body, &req)
		got, ok := req.Arguments["metainfo"].(string)
		if !ok {
			t.Fatalf("metainfo missing")
		}
		decoded, err := base64.StdEncoding.DecodeString(got)
		if err != nil || !equalBytes(decoded, rawTorrent) {
			t.Errorf("decoded metainfo = %v err=%v, want %v", decoded, err, rawTorrent)
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"torrent-added":{"hashString":"abc123"}}}`))
	}))
	defer srv.Close()

	settings := transmission.Settings{
		Host: stripScheme(srv.URL), Port: portOf(srv.URL),
		UrlBase: "/transmission/",
	}
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())

	hash, err := p.AddTorrentFromData(context.Background(), rawTorrent, "", settings)
	if err != nil {
		t.Fatalf("AddTorrentFromData: %v", err)
	}
	if hash != "abc123" {
		t.Errorf("hash = %q, want abc123", hash)
	}
}

func TestProxy_AddTorrentDuplicateFallsBack(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "s")
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"torrent-duplicate":{"hashString":"hashDUP"}}}`))
	}))
	defer srv.Close()

	settings := transmission.Settings{
		Host: stripScheme(srv.URL), Port: portOf(srv.URL),
		UrlBase: "/transmission/",
	}
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())

	hash, err := p.AddTorrentFromURL(context.Background(), "magnet:?xt=urn:btih:HASHDUP", "", settings)
	if err != nil {
		t.Fatalf("AddTorrentFromURL: %v", err)
	}
	if hash != "hashDUP" {
		t.Errorf("hash = %q, want hashDUP (duplicate path)", hash)
	}
}

func TestProxy_AddTorrentNoHashReturned(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "s")
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{}}`))
	}))
	defer srv.Close()

	settings := transmission.Settings{Host: stripScheme(srv.URL), Port: portOf(srv.URL), UrlBase: "/transmission/"}
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())

	_, err := p.AddTorrentFromURL(context.Background(), "magnet:x", "", settings)
	if !errors.Is(err, transmission.ErrNoHashReturned) {
		t.Errorf("err = %v, want ErrNoHashReturned", err)
	}
}

func TestProxy_RPCFailureSurfaces(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "s")
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"result":"invalid argument","arguments":{}}`))
	}))
	defer srv.Close()

	settings := transmission.Settings{Host: stripScheme(srv.URL), Port: portOf(srv.URL), UrlBase: "/transmission/"}
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())

	_, err := p.AddTorrentFromURL(context.Background(), "magnet:x", "", settings)
	if !errors.Is(err, transmission.ErrRPCFailure) {
		t.Errorf("err = %v, want ErrRPCFailure", err)
	}
}

func TestProxy_AuthFailedOn401(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "s")
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	settings := transmission.Settings{Host: stripScheme(srv.URL), Port: portOf(srv.URL), UrlBase: "/transmission/"}
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())

	_, err := p.AddTorrentFromURL(context.Background(), "magnet:x", "", settings)
	if !errors.Is(err, transmission.ErrAuthFailed) {
		t.Errorf("err = %v, want ErrAuthFailed", err)
	}
}

// GetClientVersion against a session-get RPC reply.
func TestProxy_GetClientVersion(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "s")
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"version":"3.00 (bb6b5a062e)","rpc-version":"17"}}`))
	}))
	defer srv.Close()

	settings := transmission.Settings{Host: stripScheme(srv.URL), Port: portOf(srv.URL), UrlBase: "/transmission/"}
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())

	got, err := p.GetClientVersion(context.Background(), settings)
	if err != nil {
		t.Fatalf("GetClientVersion: %v", err)
	}
	if got != "3.00 (bb6b5a062e)" {
		t.Errorf("version = %q, want 3.00 (bb6b5a062e)", got)
	}
}

// ============================================================================
// Transmission.Test — version-floor check.
// ============================================================================

func TestTransmission_TestSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "s")
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"version":"3.00"}}`))
	}))
	defer srv.Close()

	prov := newTransmissionForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want none", failures)
	}
}

func TestTransmission_TestVersionFloor(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "s")
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"version":"2.39"}}`))
	}))
	defer srv.Close()

	prov := newTransmissionForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "Version" {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want Version failure (2.39 < 2.40)", failures)
	}
}

func TestTransmission_TestSurfacesUnreachable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close()

	prov := newTransmissionForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) == 0 {
		t.Fatal("expected at least one failure for unreachable host")
	}
}

// ============================================================================
// Transmission.Download — end-to-end Provider invocation.
// ============================================================================

func TestTransmission_DownloadMagnetSurfacesHash(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "s")
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"torrent-added":{"hashString":"ABCDEF1234"}}}`))
	}))
	defer srv.Close()

	prov := newTransmissionForURL(t, srv)
	req := &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01-Group",
		MagnetURL:    "magnet:?xt=urn:btih:ABCDEF1234",
	}
	hash, err := prov.Download(context.Background(), req)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if hash != "abcdef1234" {
		t.Errorf("hash = %q, want abcdef1234 (lowercased)", hash)
	}
}

func TestTransmission_DownloadRejectsEmptyRequest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	prov := newTransmissionForURL(t, srv)
	_, err := prov.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Error("Download with no URLs / body should error")
	}
}

func TestTransmission_StubbedMethodsReturnNotImplementedYet(t *testing.T) {
	t.Parallel()
	prov := newTransmissionForURL(t, nil)
	if _, err := prov.GetItems(context.Background()); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetItems err = %v, want ErrNotImplementedYet", err)
	}
	if _, err := prov.GetStatus(context.Background()); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetStatus err = %v, want ErrNotImplementedYet", err)
	}
	if err := prov.RemoveItem(context.Background(), nil, false); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("RemoveItem err = %v, want ErrNotImplementedYet", err)
	}
	if err := prov.MarkItemAsImported(context.Background(), nil); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("MarkItemAsImported err = %v, want ErrNotImplementedYet", err)
	}
}

// ============================================================================
// Registry — init() registers Transmission in Default().
// ============================================================================

func TestRegistry_TransmissionRegistered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(transmission.Implementation, transmission.ConfigContract) {
		t.Errorf("Default registry missing %s/%s", transmission.Implementation, transmission.ConfigContract)
	}
}

// ============================================================================
// Helpers
// ============================================================================

func newTransmissionForURL(t *testing.T, srv *httptest.Server) *transmission.Transmission {
	t.Helper()
	url := ""
	var client *http.Client
	if srv != nil {
		url = srv.URL
		client = srv.Client()
	} else {
		client = http.DefaultClient
	}
	proxy, err := transmission.NewProxy(client, slog.Default())
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	settings := transmission.NewSettings()
	if url != "" {
		settings.Host = stripScheme(url)
		settings.Port = portOf(url)
	}
	def := &downloadclients.DownloadClient{
		ID:             2,
		Name:           "test-transmission",
		Implementation: transmission.Implementation,
		ConfigContract: transmission.ConfigContract,
	}
	return transmission.New(def, settings, proxy, slog.Default())
}

func stripScheme(s string) string {
	for _, p := range []string{"http://", "https://"} {
		s = strings.TrimPrefix(s, p)
	}
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i]
	}
	return s
}

func portOf(s string) int {
	for _, p := range []string{"http://", "https://"} {
		s = strings.TrimPrefix(s, p)
	}
	idx := strings.Index(s, ":")
	if idx < 0 {
		return 0
	}
	rest := s[idx+1:]
	port := 0
	for _, c := range rest {
		if c < '0' || c > '9' {
			break
		}
		port = port*10 + int(c-'0')
	}
	return port
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
