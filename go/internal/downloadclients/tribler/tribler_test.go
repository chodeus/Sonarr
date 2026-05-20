package tribler_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/tribler"
	"sonarr.tv/sonarr/internal/indexers"
)

// scriptedDoer queues responses per-path so the test can stage
// each Tribler call independently. Records the X-Api-Key header on
// every call.
type scriptedDoer struct {
	responses map[string][]scriptedResp
	calls     []scriptedCall
}

type scriptedResp struct {
	status int
	body   string
	err    error
}

type scriptedCall struct {
	method string
	path   string
	apiKey string
}

func newScripted() *scriptedDoer { return &scriptedDoer{responses: map[string][]scriptedResp{}} }

func (s *scriptedDoer) queue(path, body string) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: http.StatusOK, body: body})
}

func (s *scriptedDoer) queueStatus(status int) {
	s.responses["/api/settings"] = append(s.responses["/api/settings"], scriptedResp{status: status})
}

func (s *scriptedDoer) queueErr(path string, err error) {
	s.responses[path] = append(s.responses[path], scriptedResp{err: err})
}

func (s *scriptedDoer) Do(req *http.Request) (*http.Response, error) {
	s.calls = append(s.calls, scriptedCall{
		method: req.Method,
		path:   req.URL.Path,
		apiKey: req.Header.Get("X-Api-Key"),
	})
	resps, ok := s.responses[req.URL.Path]
	if !ok || len(resps) == 0 {
		return nil, errors.New("scriptedDoer: no response queued for " + req.URL.Path)
	}
	head := resps[0]
	s.responses[req.URL.Path] = resps[1:]
	if head.err != nil {
		return nil, head.err
	}
	return &http.Response{
		StatusCode: head.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(head.body)),
	}, nil
}

func newSettings() tribler.Settings {
	s := tribler.NewSettings()
	s.ApiKey = "secret-api-key"
	return s
}

func TestSettings_Defaults(t *testing.T) {
	s := tribler.NewSettings()
	if s.Host != "localhost" || s.Port != 20100 || s.AnonymityLevel != 1 || !s.SafeSeeding {
		t.Errorf("NewSettings = %+v, want defaults", s)
	}
}

func TestSettings_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*tribler.Settings)
		wantBad string
	}{
		{"happy", func(*tribler.Settings) {}, ""},
		{"empty host", func(s *tribler.Settings) { s.Host = "" }, "Host"},
		{"port too low", func(s *tribler.Settings) { s.Port = 0 }, "Port"},
		{"port too high", func(s *tribler.Settings) { s.Port = 70000 }, "Port"},
		{"urlbase missing slash", func(s *tribler.Settings) { s.UrlBase = "tribler" }, "UrlBase"},
		{"empty ApiKey", func(s *tribler.Settings) { s.ApiKey = "" }, "ApiKey"},
		{"TvCategory illegal chars", func(s *tribler.Settings) { s.TvCategory = "bad/value" }, "TvCategory"},
		{"TvCategory leading dot ok", func(s *tribler.Settings) { s.TvCategory = ".sonarr" }, ""},
		{"TvCategory + TvDirectory mutually exclusive", func(s *tribler.Settings) {
			s.TvCategory = "sonarr"
			s.TvDirectory = "/dl"
		}, "TvCategory"},
		{"negative AnonymityLevel", func(s *tribler.Settings) { s.AnonymityLevel = -1 }, "AnonymityLevel"},
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
				t.Fatalf("Validate() empty, want %s", tt.wantBad)
			}
			if f[0].PropertyName != tt.wantBad {
				t.Errorf("first failure = %q, want %q", f[0].PropertyName, tt.wantBad)
			}
		})
	}
}

func TestIdentity(t *testing.T) {
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), nil, nil)
	if p.Name() != "Tribler" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol = %v, want Torrent", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "TR" {
		t.Errorf("Definition not bound")
	}
}

func TestProxy_GetConfig_HappyPath_SendsApiKeyHeader(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/settings", `{"settings":{"libtorrent":{"download_defaults":{"saveas":"/tribler/downloads"}}}}`)
	proxy := tribler.NewProxy(doer, nil)
	cfg, err := proxy.GetConfig(context.Background(), newSettings())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cfg.Settings.LibTorrent.DownloadDefaults.SaveAs != "/tribler/downloads" {
		t.Errorf("SaveAs = %q, want /tribler/downloads", cfg.Settings.LibTorrent.DownloadDefaults.SaveAs)
	}
	// Verify the X-Api-Key header was sent.
	if len(doer.calls) != 1 || doer.calls[0].apiKey != "secret-api-key" {
		t.Errorf("X-Api-Key = %q, want 'secret-api-key'", doer.calls[0].apiKey)
	}
	if doer.calls[0].method != http.MethodGet {
		t.Errorf("method = %q, want GET", doer.calls[0].method)
	}
}

func TestProxy_GetConfig_UnauthorizedSurfacesErrAuth(t *testing.T) {
	doer := newScripted()
	doer.queueStatus(http.StatusUnauthorized)
	proxy := tribler.NewProxy(doer, nil)
	_, err := proxy.GetConfig(context.Background(), newSettings())
	if !errors.Is(err, tribler.ErrAuth) {
		t.Errorf("err = %v, want ErrAuth", err)
	}
}

func TestProxy_GetConfig_ForbiddenSurfacesErrAuth(t *testing.T) {
	doer := newScripted()
	doer.queueStatus(http.StatusForbidden)
	proxy := tribler.NewProxy(doer, nil)
	_, err := proxy.GetConfig(context.Background(), newSettings())
	if !errors.Is(err, tribler.ErrAuth) {
		t.Errorf("err = %v, want ErrAuth on 403", err)
	}
}

func TestProxy_GetConfig_5xxSurfacesUnavailable(t *testing.T) {
	doer := newScripted()
	doer.queueStatus(http.StatusInternalServerError)
	proxy := tribler.NewProxy(doer, nil)
	_, err := proxy.GetConfig(context.Background(), newSettings())
	if !errors.Is(err, tribler.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProxy_GetConfig_TransportFailureSurfacesUnavailable(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/api/settings", errors.New("dial"))
	proxy := tribler.NewProxy(doer, nil)
	_, err := proxy.GetConfig(context.Background(), newSettings())
	if !errors.Is(err, tribler.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProvider_Test_HappyPath(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/settings", `{}`)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want empty", failures)
	}
}

func TestProvider_Test_SettingsGate(t *testing.T) {
	s := newSettings()
	s.ApiKey = ""
	doer := newScripted()
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, s, tribler.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) == 0 {
		t.Fatal("failures empty, want ApiKey failure")
	}
	if len(doer.calls) != 0 {
		t.Errorf("proxy called despite Settings.Validate failure: %d calls", len(doer.calls))
	}
}

func TestProvider_Test_AuthFailureSurfacesApiKey(t *testing.T) {
	doer := newScripted()
	doer.queueStatus(http.StatusUnauthorized)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || failures[0].PropertyName != "ApiKey" {
		t.Errorf("failures = %+v, want ApiKey failure", failures)
	}
}

func TestProvider_Test_TransportFailureSurfacesHost(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/api/settings", errors.New("dial"))
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || failures[0].PropertyName != "Host" {
		t.Errorf("failures = %+v, want Host failure", failures)
	}
}

func TestProvider_GetStatus_LocalhostFlag(t *testing.T) {
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
			p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, s, tribler.NewProxy(newScripted(), nil), nil)
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

func TestProvider_RemainingStubs(t *testing.T) {
	// J82 wires Download; GetItems + RemoveItem remain stubs.
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(newScripted(), nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true); !errors.Is(err, tribler.ErrNotImplemented) {
		t.Errorf("RemoveItem err = %v, want ErrNotImplemented", err)
	}
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems err = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("GetItems len = %d, want 0", len(items))
	}
}

func TestProvider_Download_TorrentBodyUnsupported(t *testing.T) {
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(newScripted(), nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{TorrentBody: []byte("x")})
	if !errors.Is(err, tribler.ErrTorrentFileUnsupported) {
		t.Errorf("err = %v, want ErrTorrentFileUnsupported", err)
	}
}

func TestProvider_Download_NoMagnet(t *testing.T) {
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(newScripted(), nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Fatal("err = nil, want missing-magnet error")
	}
}

// captureDoer wraps scriptedDoer and grabs the last PUT body so the
// add-download payload shape can be asserted.
type captureDoer struct {
	scripted *scriptedDoer
	lastBody []byte
}

func (c *captureDoer) Do(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPut && req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		c.lastBody = body
		req.Body = io.NopCloser(strings.NewReader(""))
	}
	return c.scripted.Do(req)
}

func TestProvider_Download_MagnetHappyPath_RequestShape(t *testing.T) {
	doer := &captureDoer{scripted: newScripted()}
	doer.scripted.queue("/api/downloads", `{"infohash":"NEW-HASH","started":true}`)
	settings := newSettings()
	settings.AnonymityLevel = 2
	settings.SafeSeeding = false
	settings.TvDirectory = "/tribler/tv"
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, settings, tribler.NewProxy(doer, nil), nil)

	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "NEW-HASH" {
		t.Errorf("id = %q, want NEW-HASH", id)
	}
	if len(doer.lastBody) == 0 {
		t.Fatal("no PUT body captured")
	}
	// Body must include uri + destination + anon_hops + safe_seeding.
	body := string(doer.lastBody)
	if !strings.Contains(body, `"uri":"magnet:?xt=urn:btih:abc"`) {
		t.Errorf("body missing uri: %s", body)
	}
	if !strings.Contains(body, `"destination":"/tribler/tv"`) {
		t.Errorf("body missing destination: %s", body)
	}
	if !strings.Contains(body, `"anon_hops":2`) {
		t.Errorf("body missing anon_hops=2: %s", body)
	}
	if !strings.Contains(body, `"safe_seeding":false`) {
		t.Errorf("body missing safe_seeding=false: %s", body)
	}
}

func TestProvider_Download_MagnetUsesGetConfigForCategoryDestination(t *testing.T) {
	// TvCategory non-empty + TvDirectory empty → GetConfig is
	// called to resolve the SaveAs base.
	doer := &captureDoer{scripted: newScripted()}
	doer.scripted.queue("/api/settings", `{"settings":{"libtorrent":{"download_defaults":{"saveas":"/tribler/dl"}}}}`)
	doer.scripted.queue("/api/downloads", `{"infohash":"HASH","started":true}`)
	settings := newSettings()
	settings.TvCategory = "sonarr"
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, settings, tribler.NewProxy(doer, nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{MagnetURL: "magnet:?abc"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(string(doer.lastBody), `"destination":"/tribler/dl/sonarr"`) {
		t.Errorf("destination = %s, want /tribler/dl/sonarr (base + category)", doer.lastBody)
	}
}

func TestProvider_Download_NoCategoryNoDirectoryOmitsDestination(t *testing.T) {
	// Neither TvDirectory nor TvCategory set → destination=""
	// in the request; the Tribler server falls back to its own
	// default. The omitempty json tag drops the field entirely.
	doer := &captureDoer{scripted: newScripted()}
	doer.scripted.queue("/api/downloads", `{"infohash":"HASH"}`)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{MagnetURL: "magnet:?abc"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(string(doer.lastBody), `"destination"`) {
		t.Errorf("destination key emitted with no TvDirectory + no TvCategory: %s", doer.lastBody)
	}
}

func TestProvider_Download_AuthFailurePropagates(t *testing.T) {
	doer := newScripted()
	doer.responses["/api/downloads"] = append(doer.responses["/api/downloads"], scriptedResp{status: http.StatusUnauthorized})
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{MagnetURL: "magnet:?abc"})
	if !errors.Is(err, tribler.ErrAuth) {
		t.Errorf("err = %v, want ErrAuth", err)
	}
}

func TestProvider_Download_EmptyInfohashSurfacesError(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/downloads", `{"infohash":""}`)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{MagnetURL: "magnet:?abc"})
	if err == nil {
		t.Fatal("err = nil, want empty-infohash error")
	}
}

func TestProvider_MarkItemAsImported_ReturnsUnsupported(t *testing.T) {
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(newScripted(), nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestRegistry_RegistersTribler(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(tribler.Implementation, tribler.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", tribler.Implementation, tribler.ConfigContract)
	}
}
