package hadouken_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/hadouken"
	"sonarr.tv/sonarr/internal/indexers"
)

// base64Decode is a thin alias for [base64.StdEncoding.DecodeString]
// so the request-shape assertions read cleanly.
func base64Decode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// stubDoer records the last seen request and replies with the
// canned response.
type stubDoer struct {
	last        *http.Request
	lastBody    []byte
	respStatus  int
	respBody    string
	respHeaders map[string]string
	err         error
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	s.last = req
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		s.lastBody = b
	}
	if s.err != nil {
		return nil, s.err
	}
	status := s.respStatus
	if status == 0 {
		status = http.StatusOK
	}
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	for k, v := range s.respHeaders {
		hdr.Set(k, v)
	}
	return &http.Response{
		StatusCode: status,
		Header:     hdr,
		Body:       io.NopCloser(strings.NewReader(s.respBody)),
	}, nil
}

func newSettings() hadouken.Settings {
	s := hadouken.NewSettings()
	s.Username = "u"
	s.Password = "p"
	return s
}

func TestSettings_Defaults(t *testing.T) {
	s := hadouken.NewSettings()
	if s.Host != "localhost" || s.Port != 7070 || s.Category != "sonarr-tv" {
		t.Errorf("NewSettings = %+v, want host=localhost port=7070 category=sonarr-tv", s)
	}
}

func TestSettings_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(s *hadouken.Settings)
		wantBad string // expected first failure PropertyName, "" = no failure
	}{
		{"happy path", func(*hadouken.Settings) {}, ""},
		{"empty host", func(s *hadouken.Settings) { s.Host = "" }, "Host"},
		{"port too low", func(s *hadouken.Settings) { s.Port = 0 }, "Port"},
		{"port too high", func(s *hadouken.Settings) { s.Port = 99999 }, "Port"},
		{"urlbase without slash", func(s *hadouken.Settings) { s.UrlBase = "api" }, "UrlBase"},
		{"urlbase with slash ok", func(s *hadouken.Settings) { s.UrlBase = "/api" }, ""},
		{"empty username", func(s *hadouken.Settings) { s.Username = "" }, "Username"},
		{"empty password", func(s *hadouken.Settings) { s.Password = "" }, "Password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSettings()
			tt.mutate(&s)
			f := s.Validate()
			if tt.wantBad == "" {
				if len(f) != 0 {
					t.Errorf("Validate() = %+v, want empty", f)
				}
				return
			}
			if len(f) == 0 {
				t.Fatalf("Validate() = empty, want %s failure", tt.wantBad)
			}
			if f[0].PropertyName != tt.wantBad {
				t.Errorf("first failure = %q, want %q", f[0].PropertyName, tt.wantBad)
			}
		})
	}
}

func TestIdentity(t *testing.T) {
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), nil, nil)
	if p.Name() != "Hadouken" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol = %v, want Torrent", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "HD" {
		t.Errorf("Definition not bound")
	}
}

func TestProxy_GetSystemInfo_HappyPath(t *testing.T) {
	doer := &stubDoer{respBody: `{"jsonrpc":"2.0","id":1,"result":{"commitish":"abc","branch":"main","versions":{"hadouken":"5.1.4"}}}`}
	proxy := hadouken.NewProxy(doer, nil)
	info, err := proxy.GetSystemInfo(context.Background(), newSettings())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if info.Versions["hadouken"] != "5.1.4" {
		t.Errorf("version = %q", info.Versions["hadouken"])
	}
	if doer.last == nil {
		t.Fatal("no request captured")
	}
	if u, p, ok := doer.last.BasicAuth(); !ok || u != "u" || p != "p" {
		t.Errorf("basic auth = %q/%q ok=%v, want u/p ok=true", u, p, ok)
	}
	if !strings.HasSuffix(doer.last.URL.String(), "/api") {
		t.Errorf("URL = %q, want /api suffix", doer.last.URL.String())
	}
	// Verify the request body is a JSON-RPC 2.0 envelope.
	var got map[string]any
	_ = json.Unmarshal(doer.lastBody, &got)
	if got["jsonrpc"] != "2.0" || got["method"] != "core.getSystemInfo" {
		t.Errorf("request body = %v, want jsonrpc=2.0 method=core.getSystemInfo", got)
	}
}

func TestProxy_GetSystemInfo_UrlBaseHonoured(t *testing.T) {
	doer := &stubDoer{respBody: `{"result":{"versions":{"hadouken":"5.1"}}}`}
	proxy := hadouken.NewProxy(doer, nil)
	s := newSettings()
	s.UrlBase = "/hadouken"
	_, err := proxy.GetSystemInfo(context.Background(), s)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasSuffix(doer.last.URL.Path, "/hadouken/api") {
		t.Errorf("URL path = %q, want /hadouken/api", doer.last.URL.Path)
	}
}

func TestProxy_GetSystemInfo_UseSsl(t *testing.T) {
	doer := &stubDoer{respBody: `{"result":{"versions":{"hadouken":"5.1"}}}`}
	proxy := hadouken.NewProxy(doer, nil)
	s := newSettings()
	s.UseSsl = true
	_, err := proxy.GetSystemInfo(context.Background(), s)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if doer.last.URL.Scheme != "https" {
		t.Errorf("scheme = %q, want https", doer.last.URL.Scheme)
	}
}

func TestProxy_GetSystemInfo_AuthFails(t *testing.T) {
	doer := &stubDoer{respStatus: http.StatusUnauthorized, respBody: "denied"}
	proxy := hadouken.NewProxy(doer, nil)
	_, err := proxy.GetSystemInfo(context.Background(), newSettings())
	if !errors.Is(err, hadouken.ErrAuth) {
		t.Errorf("err = %v, want ErrAuth", err)
	}
}

func TestProxy_GetSystemInfo_TransportFails(t *testing.T) {
	doer := &stubDoer{err: errors.New("dial tcp")}
	proxy := hadouken.NewProxy(doer, nil)
	_, err := proxy.GetSystemInfo(context.Background(), newSettings())
	if !errors.Is(err, hadouken.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProxy_GetSystemInfo_5xx(t *testing.T) {
	doer := &stubDoer{respStatus: http.StatusInternalServerError, respBody: "boom"}
	proxy := hadouken.NewProxy(doer, nil)
	_, err := proxy.GetSystemInfo(context.Background(), newSettings())
	if !errors.Is(err, hadouken.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable (5xx)", err)
	}
}

func TestProxy_GetSystemInfo_RPCError(t *testing.T) {
	doer := &stubDoer{respBody: `{"error":{"code":-32601,"message":"Method not found"}}`}
	proxy := hadouken.NewProxy(doer, nil)
	_, err := proxy.GetSystemInfo(context.Background(), newSettings())
	if !errors.Is(err, hadouken.ErrRPC) {
		t.Errorf("err = %v, want ErrRPC", err)
	}
}

func TestProvider_Test_HappyPath(t *testing.T) {
	doer := &stubDoer{respBody: `{"result":{"versions":{"hadouken":"5.2.0"}}}`}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want empty", failures)
	}
}

func TestProvider_Test_SettingsValidationGate(t *testing.T) {
	// Empty Username → Settings.Validate fails; the proxy is never
	// called.
	s := newSettings()
	s.Username = ""
	doer := &stubDoer{respBody: `{"result":{"versions":{"hadouken":"5.2"}}}`}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, s, hadouken.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) == 0 {
		t.Fatal("failures empty, want Username failure")
	}
	if doer.last != nil {
		t.Error("proxy was called despite Settings.Validate failure")
	}
}

func TestProvider_Test_VersionTooOld(t *testing.T) {
	doer := &stubDoer{respBody: `{"result":{"versions":{"hadouken":"5.0.99"}}}`}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want one entry", failures)
	}
	if !strings.Contains(failures[0].ErrorMessage, "5.0.99") {
		t.Errorf("ErrorMessage = %q, want it to mention the reported version", failures[0].ErrorMessage)
	}
}

func TestProvider_Test_AuthFails(t *testing.T) {
	doer := &stubDoer{respStatus: http.StatusUnauthorized}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want one entry", failures)
	}
	if failures[0].PropertyName != "Password" {
		t.Errorf("PropertyName = %q, want Password", failures[0].PropertyName)
	}
}

func TestProvider_Test_TransportFails(t *testing.T) {
	doer := &stubDoer{err: errors.New("dial")}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want one entry", failures)
	}
	if failures[0].PropertyName != "Host" {
		t.Errorf("PropertyName = %q, want Host", failures[0].PropertyName)
	}
}

func TestProvider_GetStatus_LocalhostHostFlagsLocalhostTrue(t *testing.T) {
	cases := map[string]bool{
		"localhost": true,
		"127.0.0.1": true,
		"::1":       true,
		"example":   false,
	}
	for host, want := range cases {
		t.Run(host, func(t *testing.T) {
			s := newSettings()
			s.Host = host
			p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, s, hadouken.NewProxy(&stubDoer{}, nil), nil)
			info, err := p.GetStatus(context.Background())
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if info.IsLocalhost != want {
				t.Errorf("host=%q IsLocalhost = %v, want %v", host, info.IsLocalhost, want)
			}
		})
	}
}

func TestProvider_Download_NilRequest(t *testing.T) {
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(&stubDoer{}, nil), nil)
	_, err := p.Download(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want nil-request error")
	}
}

func TestProvider_Download_NoBodyOrMagnet(t *testing.T) {
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(&stubDoer{}, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Fatal("err = nil, want neither-body-nor-magnet error")
	}
}

func TestProvider_Download_TorrentBody_RequestShapeAndResponse(t *testing.T) {
	// Server replies with a plain string (lowercase hash) — Provider
	// uppercases it.
	doer := &stubDoer{respBody: `{"jsonrpc":"2.0","id":1,"result":"abcdef0123"}`}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show S01E01",
		TorrentBody:  []byte("FAKE-TORRENT-BYTES"),
	})
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	if id != "ABCDEF0123" {
		t.Errorf("id = %q, want uppercased ABCDEF0123", id)
	}
	// Verify the request body shape: webui.addTorrent + ["file",
	// "<base64>", {"label":"sonarr-tv"}].
	var env map[string]any
	_ = json.Unmarshal(doer.lastBody, &env)
	if env["method"] != "webui.addTorrent" {
		t.Errorf("method = %v, want webui.addTorrent", env["method"])
	}
	params, ok := env["params"].([]any)
	if !ok || len(params) != 3 {
		t.Fatalf("params = %v, want 3-element slice", env["params"])
	}
	if params[0] != "file" {
		t.Errorf("params[0] = %v, want 'file'", params[0])
	}
	// params[1] is the base64-encoded body — must decode back to original.
	encoded, _ := params[1].(string)
	if encoded == "" {
		t.Fatalf("params[1] = %v, want base64 string", params[1])
	}
	decoded, derr := base64Decode(encoded)
	if derr != nil || string(decoded) != "FAKE-TORRENT-BYTES" {
		t.Errorf("params[1] decode = %q (err=%v), want FAKE-TORRENT-BYTES", decoded, derr)
	}
	labelObj, _ := params[2].(map[string]any)
	if labelObj["label"] != "sonarr-tv" {
		t.Errorf("label = %v, want sonarr-tv", labelObj["label"])
	}
}

func TestProvider_Download_TorrentBody_EmptyResponseFallsBackToInfoHash(t *testing.T) {
	// Hadouken replies with empty string → Provider falls back to req.InfoHash.
	doer := &stubDoer{respBody: `{"result":""}`}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		TorrentBody:  []byte("y"),
		InfoHash:     "deadbeef",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "DEADBEEF" {
		t.Errorf("id = %q, want DEADBEEF (fallback path)", id)
	}
}

func TestProvider_Download_TorrentBody_EmptyResponseAndNoInfoHash_Errors(t *testing.T) {
	doer := &stubDoer{respBody: `{"result":""}`}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		TorrentBody:  []byte("y"),
	})
	if err == nil {
		t.Fatal("err = nil, want empty-response + no-InfoHash error")
	}
}

func TestProvider_Download_Magnet_RequestShapeAndInfoHashReturn(t *testing.T) {
	doer := &stubDoer{respBody: `{"result":"ignored-by-provider"}`}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:cafebabe",
		InfoHash:     "cafebabe",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "CAFEBABE" {
		t.Errorf("id = %q, want CAFEBABE (req.InfoHash uppercased)", id)
	}
	var env map[string]any
	_ = json.Unmarshal(doer.lastBody, &env)
	params, _ := env["params"].([]any)
	if len(params) != 3 || params[0] != "url" {
		t.Errorf("params = %v, want ['url', <magnet>, {label}]", params)
	}
	if params[1] != "magnet:?xt=urn:btih:cafebabe" {
		t.Errorf("params[1] = %v, want the magnet URL verbatim", params[1])
	}
}

func TestProvider_Download_Magnet_MissingInfoHashErrors(t *testing.T) {
	doer := &stubDoer{respBody: `{"result":"ok"}`}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:abc",
	})
	if err == nil {
		t.Fatal("err = nil, want missing-InfoHash error on magnet path")
	}
}

func TestProvider_Download_PropagatesProxyError(t *testing.T) {
	doer := &stubDoer{respStatus: http.StatusInternalServerError, respBody: "boom"}
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		TorrentBody:  []byte("y"),
		InfoHash:     "abc",
	})
	if !errors.Is(err, hadouken.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable propagated", err)
	}
}

func TestProvider_RemoveItemStub_ReturnsNotImplemented(t *testing.T) {
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(&stubDoer{}, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true); !errors.Is(err, hadouken.ErrNotImplemented) {
		t.Errorf("err = %v, want ErrNotImplemented", err)
	}
}

func TestProvider_GetItemsStub_ReturnsEmpty(t *testing.T) {
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(&stubDoer{}, nil), nil)
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0 (skeleton stub)", len(items))
	}
}

func TestProvider_MarkItemAsImported_ReturnsUnsupported(t *testing.T) {
	p := hadouken.New(&downloadclients.DownloadClient{Name: "HD"}, newSettings(), hadouken.NewProxy(&stubDoer{}, nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestRegistry_RegistersHadouken(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(hadouken.Implementation, hadouken.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", hadouken.Implementation, hadouken.ConfigContract)
	}
}
