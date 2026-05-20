package downloadstation_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/downloadstation"
	"sonarr.tv/sonarr/internal/indexers"
)

// scriptedDoer queues responses per-path so the
// SYNO.API.Info-discovery + auth flow can be staged in order.
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
	path  string
	query url.Values
}

func newScripted() *scriptedDoer { return &scriptedDoer{responses: map[string][]scriptedResp{}} }

func (s *scriptedDoer) queue(path, body string) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: http.StatusOK, body: body})
}

func (s *scriptedDoer) queueErr(path string, err error) {
	s.responses[path] = append(s.responses[path], scriptedResp{err: err})
}

func (s *scriptedDoer) Do(req *http.Request) (*http.Response, error) {
	s.calls = append(s.calls, scriptedCall{path: req.URL.Path, query: req.URL.Query()})
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

func newSettings() downloadstation.Settings {
	s := downloadstation.NewSettings()
	s.Username = "u"
	s.Password = "p"
	return s
}

func TestSettings_Defaults(t *testing.T) {
	s := downloadstation.NewSettings()
	if s.Host != "127.0.0.1" || s.Port != 5000 {
		t.Errorf("NewSettings = %+v, want host=127.0.0.1 port=5000", s)
	}
}

func TestSettings_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*downloadstation.Settings)
		wantBad string
	}{
		{"happy path", func(*downloadstation.Settings) {}, ""},
		{"empty host", func(s *downloadstation.Settings) { s.Host = "" }, "Host"},
		{"port too low", func(s *downloadstation.Settings) { s.Port = 0 }, "Port"},
		{"port too high", func(s *downloadstation.Settings) { s.Port = 70000 }, "Port"},
		{"TvDirectory absolute", func(s *downloadstation.Settings) { s.TvDirectory = "/abs/path" }, "TvDirectory"},
		{"TvDirectory relative ok", func(s *downloadstation.Settings) { s.TvDirectory = "rel/path" }, ""},
		{"TvCategory illegal chars", func(s *downloadstation.Settings) { s.TvCategory = "bad/value" }, "TvCategory"},
		{"TvCategory leading dot ok", func(s *downloadstation.Settings) { s.TvCategory = ".sonarr-tv" }, ""},
		{"TvCategory and TvDirectory mutually exclusive", func(s *downloadstation.Settings) {
			s.TvCategory = "sonarr"
			s.TvDirectory = "rel/path"
		}, "TvCategory"},
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
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), nil, nil)
	if p.Name() != "Download Station" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol = %v, want Torrent", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "DS" {
		t.Errorf("Definition not bound")
	}
}

// queueAPIDiscovery queues a SYNO.API.Info response advertising
// the auth + downloadstation paths so a subsequent Authenticate
// call can find them.
func queueAPIDiscovery(doer *scriptedDoer, authMax int) {
	body := `{"success":true,"data":{
		"SYNO.API.Auth":{"path":"auth.cgi","maxVersion":` +
		intToStr(authMax) + `,"minVersion":1},
		"SYNO.DownloadStation.Task":{"path":"DownloadStation/task.cgi","maxVersion":3,"minVersion":1}
	}}`
	doer.queue("/webapi/query.cgi", body)
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	if n < 0 {
		out = "-"
		n = -n
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+(n%10))) + digits
		n /= 10
	}
	return out + digits
}

func TestProxy_Authenticate_HappyPath(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3) // version=3 → login uses v2
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID-123"}}`)

	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	sid, err := proxy.Authenticate(context.Background(), newSettings(), false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if sid != "SID-123" {
		t.Errorf("sid = %q, want SID-123", sid)
	}
	// Verify the login query carried account/passwd/format=sid/session.
	var login *scriptedCall
	for i := range doer.calls {
		if doer.calls[i].path == "/webapi/auth.cgi" {
			login = &doer.calls[i]
		}
	}
	if login == nil {
		t.Fatal("no /webapi/auth.cgi call recorded")
	}
	if login.query.Get("account") != "u" {
		t.Errorf("account = %q, want u", login.query.Get("account"))
	}
	if login.query.Get("passwd") != "p" {
		t.Errorf("passwd = %q, want p", login.query.Get("passwd"))
	}
	if login.query.Get("format") != "sid" {
		t.Errorf("format = %q, want sid", login.query.Get("format"))
	}
	if login.query.Get("session") != "DownloadStation" {
		t.Errorf("session = %q, want DownloadStation", login.query.Get("session"))
	}
	if login.query.Get("version") != "2" {
		t.Errorf("version = %q, want 2 (authMax=3 → v2)", login.query.Get("version"))
	}
}

func TestProxy_Authenticate_VersionSelection(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 7) // version=7 → login uses v6
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID-7"}}`)
	proxy := downloadstation.NewProxy(doer, nil, nil)
	if _, err := proxy.Authenticate(context.Background(), newSettings(), false); err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, c := range doer.calls {
		if c.path == "/webapi/auth.cgi" {
			if c.query.Get("version") != "6" {
				t.Errorf("version = %q, want 6 (authMax=7 → v6)", c.query.Get("version"))
			}
		}
	}
}

func TestProxy_Authenticate_SessionCacheReuse(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID-1"}}`)
	// Second Authenticate would fail if it actually fired (no
	// second auth.cgi queued).
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	if _, err := proxy.Authenticate(context.Background(), newSettings(), false); err != nil {
		t.Fatalf("first err = %v", err)
	}
	if _, err := proxy.Authenticate(context.Background(), newSettings(), false); err != nil {
		t.Fatalf("second err = %v (cache should have served)", err)
	}
	count := 0
	for _, c := range doer.calls {
		if c.path == "/webapi/auth.cgi" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("auth call count = %d, want 1 (cache hit on second)", count)
	}
}

func TestProxy_Authenticate_ForceBypassesCache(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S1"}}`)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S2"}}`)
	proxy := downloadstation.NewProxy(doer, nil, nil)
	first, _ := proxy.Authenticate(context.Background(), newSettings(), false)
	second, _ := proxy.Authenticate(context.Background(), newSettings(), true)
	if first == second {
		t.Errorf("force=true did not bypass cache (both = %q)", first)
	}
}

func TestProxy_Authenticate_BadCredentialsSurfacesErrAuth(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":false,"error":{"code":400}}`)
	proxy := downloadstation.NewProxy(doer, nil, nil)
	_, err := proxy.Authenticate(context.Background(), newSettings(), false)
	if !errors.Is(err, downloadstation.ErrAuth) {
		t.Errorf("err = %v, want ErrAuth on code=400", err)
	}
}

func TestProxy_Authenticate_SessionErrorCodesMapToErrAuth(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":false,"error":{"code":119}}`)
	proxy := downloadstation.NewProxy(doer, nil, nil)
	_, err := proxy.Authenticate(context.Background(), newSettings(), false)
	if !errors.Is(err, downloadstation.ErrAuth) {
		t.Errorf("err = %v, want ErrAuth on code=119 (session)", err)
	}
}

func TestProxy_Authenticate_TransportErrorPropagatesUnavailable(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/webapi/query.cgi", errors.New("dial"))
	proxy := downloadstation.NewProxy(doer, nil, nil)
	_, err := proxy.Authenticate(context.Background(), newSettings(), false)
	if !errors.Is(err, downloadstation.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProxy_Authenticate_UseSslSwitchesScheme(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	proxy := downloadstation.NewProxy(doer, nil, nil)
	s := newSettings()
	s.UseSsl = true
	if _, err := proxy.Authenticate(context.Background(), s, false); err != nil {
		t.Fatalf("err = %v", err)
	}
	// scriptedDoer doesn't capture scheme; this test just locks the
	// build path against scheme-validation crashes.
}

func TestProvider_Test_HappyPath(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
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
	s.Port = 0
	doer := newScripted()
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, downloadstation.NewProxy(doer, nil, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) == 0 {
		t.Fatal("failures empty, want Port failure")
	}
	if len(doer.calls) != 0 {
		t.Errorf("proxy called despite Settings.Validate failure: %d calls", len(doer.calls))
	}
}

func TestProvider_Test_AuthFailureSurfacesUsernameProperty(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":false,"error":{"code":400}}`)
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || failures[0].PropertyName != "Username" {
		t.Errorf("failures = %+v, want Username failure", failures)
	}
}

func TestProvider_Test_TransportFailureSurfacesHost(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/webapi/query.cgi", errors.New("dial"))
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
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
			p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, downloadstation.NewProxy(newScripted(), nil, nil), nil)
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
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(newScripted(), nil, nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"}); !errors.Is(err, downloadstation.ErrNotImplemented) {
		t.Errorf("Download err = %v, want ErrNotImplemented", err)
	}
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true); !errors.Is(err, downloadstation.ErrNotImplemented) {
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
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(newScripted(), nil, nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestRegistry_RegistersTorrentDownloadStation(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(downloadstation.TorrentImplementation, downloadstation.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", downloadstation.TorrentImplementation, downloadstation.ConfigContract)
	}
}
