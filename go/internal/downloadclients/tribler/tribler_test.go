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

func TestProvider_StubReturns(t *testing.T) {
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(newScripted(), nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"}); !errors.Is(err, tribler.ErrNotImplemented) {
		t.Errorf("Download err = %v, want ErrNotImplemented", err)
	}
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
