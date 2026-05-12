package transmission_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

// TestTransmission_NilItemGuards locks the nil-item-input guards on
// the methods that take an *Item. The other previously-stubbed
// methods (GetItems / GetStatus / MarkItemAsImported) are now ported
// — their behaviour is exercised in the dedicated tests below.
func TestTransmission_NilItemGuards(t *testing.T) {
	t.Parallel()
	prov := newTransmissionForURL(t, nil)
	if err := prov.RemoveItem(context.Background(), nil, false); err == nil {
		t.Errorf("RemoveItem(nil) should error")
	}
	if err := prov.MarkItemAsImported(context.Background(), nil); err == nil {
		t.Errorf("MarkItemAsImported(nil) should error")
	}
}

// ============================================================================
// Slice 4.BT (C-cleanup sub-arc C2b): Transmission high-level methods
// + supporting Proxy endpoints.
// ============================================================================

// fakeTransmission serves canned `arguments` payloads keyed by
// method. The hander handles the 409-session-id handshake on the
// first call so transports auth identically to a real Transmission.
type fakeTransmission struct {
	sessionID    string
	versionResp  string // canned session-get reply (raw arguments JSON)
	torrentsResp string // canned torrent-get reply (raw arguments JSON)
	configResp   string // canned session-get reply for GetConfig path
	calls        []call // captures every (method, args) seen
}

type call struct {
	method string
	args   map[string]any
}

func (f *fakeTransmission) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	if f.sessionID == "" {
		f.sessionID = "test-session"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != f.sessionID {
			w.Header().Set("X-Transmission-Session-Id", f.sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method    string         `json:"method"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(body, &req)
		f.calls = append(f.calls, call{method: req.Method, args: req.Arguments})
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "session-get":
			payload := f.versionResp
			if f.configResp != "" {
				payload = f.configResp
			}
			if payload == "" {
				payload = `{"version":"3.00","rpc-version":"15","download-dir":"/data/Downloads"}`
			}
			_, _ = w.Write([]byte(`{"result":"success","arguments":` + payload + `}`))
		case "torrent-get":
			payload := f.torrentsResp
			if payload == "" {
				payload = `{"torrents":[]}`
			}
			_, _ = w.Write([]byte(`{"result":"success","arguments":` + payload + `}`))
		case "torrent-remove", "torrent-set":
			_, _ = w.Write([]byte(`{"result":"success","arguments":{}}`))
		default:
			_, _ = w.Write([]byte(`{"result":"success","arguments":{}}`))
		}
	}
}

func TestProxy_GetTorrentsDecodesPayload(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{
		torrentsResp: `{"torrents":[{"id":1,"hashString":"abc","name":"Show","downloadDir":"/data/tv","totalSize":1024,"leftUntilDone":0,"status":6,"labels":["tv"],"eta":-1,"isFinished":true,"file-count":1}]}`,
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	settings := transmission.NewSettings()
	settings.Host = stripScheme(srv.URL)
	settings.Port = portOf(srv.URL)
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())
	got, err := p.GetTorrents(context.Background(), nil, settings)
	if err != nil {
		t.Fatalf("GetTorrents: %v", err)
	}
	if len(got) != 1 || got[0].HashString != "abc" {
		t.Fatalf("got = %+v", got)
	}
	if got[0].FileCount() != 1 {
		t.Errorf("FileCount = %d, want 1", got[0].FileCount())
	}
	if got[0].Status != transmission.TorrentStatusSeeding {
		t.Errorf("Status = %v, want Seeding(6)", got[0].Status)
	}
}

func TestProxy_GetConfigDecodesFullShape(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{
		configResp: `{"version":"4.0.5","rpc-version":"17","download-dir":"/data/Done","seedRatioLimit":2.5,"seedRatioLimited":true,"idle-seeding-limit-enabled":true}`,
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	settings := transmission.NewSettings()
	settings.Host = stripScheme(srv.URL)
	settings.Port = portOf(srv.URL)
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())
	cfg, err := p.GetConfig(context.Background(), settings)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.Version != "4.0.5" || cfg.DownloadDir != "/data/Done" {
		t.Errorf("cfg = %+v", cfg)
	}
	if !cfg.SeedRatioLimited || !cfg.IdleSeedingLimitEnabled {
		t.Errorf("flags = %+v", cfg)
	}
}

func TestProxy_RemoveTorrentPostsCorrectArgs(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	settings := transmission.NewSettings()
	settings.Host = stripScheme(srv.URL)
	settings.Port = portOf(srv.URL)
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())
	if err := p.RemoveTorrent(context.Background(), "deadbeef", true, settings); err != nil {
		t.Fatalf("RemoveTorrent: %v", err)
	}
	var found bool
	for _, c := range fake.calls {
		if c.method == "torrent-remove" {
			found = true
			if c.args["delete-local-data"] != true {
				t.Errorf("delete-local-data = %v, want true", c.args["delete-local-data"])
			}
		}
	}
	if !found {
		t.Errorf("torrent-remove call not seen; calls=%+v", fake.calls)
	}
}

func TestProxy_SetTorrentLabelsPostsLabelsList(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	settings := transmission.NewSettings()
	settings.Host = stripScheme(srv.URL)
	settings.Port = portOf(srv.URL)
	p, _ := transmission.NewProxy(srv.Client(), slog.Default())
	if err := p.SetTorrentLabels(context.Background(), "h1", []string{"tv-imported"}, settings); err != nil {
		t.Fatalf("SetTorrentLabels: %v", err)
	}
	var found bool
	for _, c := range fake.calls {
		if c.method == "torrent-set" {
			found = true
		}
	}
	if !found {
		t.Errorf("torrent-set call not seen; calls=%+v", fake.calls)
	}
}

func TestTransmission_GetItemsLabelFilterWhenSupportsLabels(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{
		versionResp:  `{"version":"4.0.5","rpc-version":"17"}`,
		torrentsResp: `{"torrents":[{"hashString":"a","name":"A","downloadDir":"/d","totalSize":10,"leftUntilDone":0,"labels":["tv-sonarr"],"status":6,"eta":-1,"isFinished":true},{"hashString":"b","name":"B","downloadDir":"/d","totalSize":10,"leftUntilDone":0,"labels":["other"],"status":6,"eta":-1,"isFinished":true}]}`,
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	prov := newTransmissionForURL(t, srv)
	// Pin TvCategory so the filter runs.
	prov = setTvCategory(t, prov, srv, "tv-sonarr")
	got, err := prov.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 || got[0].DownloadID != "A" {
		t.Fatalf("expected single label-matching torrent, got %+v", got)
	}
}

func TestTransmission_GetItemsDirectoryFilterWhenNoLabels(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{
		// Old Transmission (3.x) → SupportsLabels = false → directory filter.
		versionResp:  `{"version":"3.00","rpc-version":"15"}`,
		torrentsResp: `{"torrents":[{"hashString":"a","name":"A","downloadDir":"/data/tv-sonarr/show","totalSize":10,"leftUntilDone":0,"status":6,"eta":-1,"isFinished":true},{"hashString":"b","name":"B","downloadDir":"/data/movies","totalSize":10,"leftUntilDone":0,"status":6,"eta":-1,"isFinished":true}]}`,
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	prov := setTvCategory(t, newTransmissionForURL(t, srv), srv, "tv-sonarr")
	got, err := prov.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 || got[0].DownloadID != "A" {
		t.Errorf("expected single directory-matching torrent, got %+v", got)
	}
}

func TestTransmission_GetItemsStatusProjections(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{
		versionResp: `{"version":"4.0.5","rpc-version":"17"}`,
		torrentsResp: `{"torrents":[
			{"hashString":"err","name":"Err","downloadDir":"/d","totalSize":10,"leftUntilDone":5,"labels":["tv"],"status":4,"eta":-1,"errorString":"boom"},
			{"hashString":"zero","name":"Z","downloadDir":"/d","totalSize":0,"leftUntilDone":0,"labels":["tv"],"status":4,"eta":-1},
			{"hashString":"done","name":"D","downloadDir":"/d","totalSize":10,"leftUntilDone":0,"labels":["tv"],"status":6,"eta":-1,"isFinished":true},
			{"hashString":"queued","name":"Q","downloadDir":"/d","totalSize":10,"leftUntilDone":5,"labels":["tv"],"status":3,"eta":300},
			{"hashString":"dl","name":"DL","downloadDir":"/d","totalSize":10,"leftUntilDone":5,"labels":["tv"],"status":4,"eta":120}
		]}`,
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	prov := setTvCategory(t, newTransmissionForURL(t, srv), srv, "tv")
	got, _ := prov.GetItems(context.Background())
	wantStatus := map[string]downloadclients.DownloadItemStatus{
		"ERR":    downloadclients.DownloadItemStatusWarning,
		"ZERO":   downloadclients.DownloadItemStatusQueued,
		"DONE":   downloadclients.DownloadItemStatusCompleted,
		"QUEUED": downloadclients.DownloadItemStatusQueued,
		"DL":     downloadclients.DownloadItemStatusDownloading,
	}
	if len(got) != len(wantStatus) {
		t.Fatalf("len(got)=%d want %d; got=%+v", len(got), len(wantStatus), got)
	}
	for _, it := range got {
		want, ok := wantStatus[it.DownloadID]
		if !ok {
			t.Errorf("unexpected DownloadID %q", it.DownloadID)
			continue
		}
		if it.Status != want {
			t.Errorf("%s: status=%v want=%v", it.DownloadID, it.Status, want)
		}
	}
}

func TestTransmission_RemoveItemPostsHashLowercase(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{versionResp: `{"version":"4.0.5"}`}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	prov := newTransmissionForURL(t, srv)
	item := &downloadclients.Item{DownloadID: "DEADBEEF"}
	if err := prov.RemoveItem(context.Background(), item, true); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	var seenIDs []any
	for _, c := range fake.calls {
		if c.method == "torrent-remove" {
			if ids, ok := c.args["ids"].([]any); ok {
				seenIDs = ids
			}
		}
	}
	if len(seenIDs) != 1 || seenIDs[0] != "deadbeef" {
		t.Errorf("torrent-remove ids = %v, want [\"deadbeef\"]", seenIDs)
	}
}

func TestTransmission_GetStatusUsesTvDirectoryWhenSet(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{configResp: `{"version":"4.0.5","download-dir":"/should/not/be/used"}`}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	prov := newTransmissionForURL(t, srv)
	prov = setTvDirectory(t, prov, srv, "/explicit/tv")
	info, err := prov.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(info.OutputRootFolders) != 1 {
		t.Fatalf("OutputRootFolders = %+v", info.OutputRootFolders)
	}
	// TvDirectory wins, no session-get call expected.
	for _, c := range fake.calls {
		if c.method == "session-get" {
			t.Errorf("session-get unexpectedly called when TvDirectory is set; calls=%+v", fake.calls)
		}
	}
}

func TestTransmission_GetStatusFallsBackToConfigDownloadDir(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{configResp: `{"version":"4.0.5","download-dir":"/data/Downloads"}`}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	prov := setTvCategory(t, newTransmissionForURL(t, srv), srv, "tv")
	info, err := prov.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(info.OutputRootFolders) != 1 {
		t.Fatalf("OutputRootFolders = %+v", info.OutputRootFolders)
	}
	// Expect /data/Downloads/tv (joined with TvCategory).
	full := info.OutputRootFolders[0].FullPath()
	if !strings.Contains(full, "tv") || !strings.Contains(full, "Downloads") {
		t.Errorf("OutputRootFolders[0] = %s, want a /data/Downloads + /tv join", full)
	}
}

func TestTransmission_MarkItemAsImportedRejectsBeforeVersion4(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{versionResp: `{"version":"3.00"}`}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	prov := newTransmissionForURL(t, srv)
	err := prov.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "x"})
	if !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestTransmission_MarkItemAsImportedSwapsLabelsAtVersion4(t *testing.T) {
	t.Parallel()
	fake := &fakeTransmission{
		versionResp:  `{"version":"4.0.5"}`,
		torrentsResp: `{"torrents":[{"hashString":"abcdef","name":"Show","labels":["tv-sonarr","other"]}]}`,
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	prov := newTransmissionForURL(t, srv)
	prov = setTvCategoryAndImported(t, prov, srv, "tv-sonarr", "imported-sonarr")

	if err := prov.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "ABCDEF"}); err != nil {
		t.Fatalf("MarkItemAsImported: %v", err)
	}

	var setLabels []any
	for _, c := range fake.calls {
		if c.method == "torrent-set" {
			if l, ok := c.args["labels"].([]any); ok {
				setLabels = l
			}
		}
	}
	if len(setLabels) == 0 {
		t.Fatalf("no torrent-set with labels seen; calls=%+v", fake.calls)
	}
	// Want: original "other" preserved, "tv-sonarr" removed, "imported-sonarr" added.
	gotSet := map[string]bool{}
	for _, l := range setLabels {
		gotSet[fmt.Sprint(l)] = true
	}
	if gotSet["tv-sonarr"] {
		t.Errorf("expected tv-sonarr REMOVED, got %v", setLabels)
	}
	if !gotSet["other"] || !gotSet["imported-sonarr"] {
		t.Errorf("expected other + imported-sonarr present, got %v", setLabels)
	}
}

// setTvCategory rebuilds the provider with TvCategory mutated. The
// test helper newTransmissionForURL doesn't expose Settings; we
// reconstruct the provider via the same scaffolding.
func setTvCategory(t *testing.T, _ *transmission.Transmission, srv *httptest.Server, tvCategory string) *transmission.Transmission {
	t.Helper()
	proxy, _ := transmission.NewProxy(srv.Client(), slog.Default())
	s := transmission.NewSettings()
	s.Host = stripScheme(srv.URL)
	s.Port = portOf(srv.URL)
	s.TvCategory = tvCategory
	def := &downloadclients.DownloadClient{ID: 2, Name: "test", Implementation: transmission.Implementation, ConfigContract: transmission.ConfigContract}
	return transmission.New(def, s, proxy, slog.Default())
}

func setTvDirectory(t *testing.T, _ *transmission.Transmission, srv *httptest.Server, tvDir string) *transmission.Transmission {
	t.Helper()
	proxy, _ := transmission.NewProxy(srv.Client(), slog.Default())
	s := transmission.NewSettings()
	s.Host = stripScheme(srv.URL)
	s.Port = portOf(srv.URL)
	s.TvDirectory = tvDir
	def := &downloadclients.DownloadClient{ID: 2, Name: "test", Implementation: transmission.Implementation, ConfigContract: transmission.ConfigContract}
	return transmission.New(def, s, proxy, slog.Default())
}

func setTvCategoryAndImported(t *testing.T, _ *transmission.Transmission, srv *httptest.Server, tvCat, imported string) *transmission.Transmission {
	t.Helper()
	proxy, _ := transmission.NewProxy(srv.Client(), slog.Default())
	s := transmission.NewSettings()
	s.Host = stripScheme(srv.URL)
	s.Port = portOf(srv.URL)
	s.TvCategory = tvCat
	s.TvImportedCategory = imported
	def := &downloadclients.DownloadClient{ID: 2, Name: "test", Implementation: transmission.Implementation, ConfigContract: transmission.ConfigContract}
	return transmission.New(def, s, proxy, slog.Default())
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
