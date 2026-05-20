package flood_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/flood"
	"sonarr.tv/sonarr/internal/indexers"
)

// scriptedDoer queues responses per-path so the cookie auth flow +
// follow-on call shape can be staged in order.
type scriptedDoer struct {
	responses map[string][]scriptedResp
	calls     []scriptedCall
}

type scriptedResp struct {
	status     int
	body       string
	setCookies []*http.Cookie
	err        error
}

type scriptedCall struct {
	method  string
	path    string
	body    []byte
	cookies []*http.Cookie
}

func newScripted() *scriptedDoer {
	return &scriptedDoer{responses: map[string][]scriptedResp{}}
}

func (s *scriptedDoer) queue(path string) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: http.StatusOK, body: "{}"})
}

func (s *scriptedDoer) queueWithCookies(path string, cookies ...*http.Cookie) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: http.StatusOK, body: "{}", setCookies: cookies})
}

func (s *scriptedDoer) queueUnauthorized(path string) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: http.StatusUnauthorized, body: "denied"})
}

func (s *scriptedDoer) queueErr(path string, err error) {
	s.responses[path] = append(s.responses[path], scriptedResp{err: err})
}

func (s *scriptedDoer) Do(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	s.calls = append(s.calls, scriptedCall{
		method:  req.Method,
		path:    req.URL.Path,
		body:    body,
		cookies: req.Cookies(),
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
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	for _, c := range head.setCookies {
		hdr.Add("Set-Cookie", c.String())
	}
	st := head.status
	if st == 0 {
		st = http.StatusOK
	}
	return &http.Response{
		StatusCode: st,
		Header:     hdr,
		Body:       io.NopCloser(strings.NewReader(head.body)),
	}, nil
}

func newSettings() flood.Settings {
	s := flood.NewSettings()
	s.Username = "u"
	s.Password = "p"
	return s
}

func TestSettings_Defaults(t *testing.T) {
	s := flood.NewSettings()
	if s.Host != "localhost" || s.Port != 3000 || !s.StartOnAdd {
		t.Errorf("NewSettings = %+v, want host=localhost port=3000 startOnAdd=true", s)
	}
	if len(s.Tags) != 1 || s.Tags[0] != "sonarr" {
		t.Errorf("Tags = %v, want [sonarr]", s.Tags)
	}
}

func TestSettings_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*flood.Settings)
		wantBad string
	}{
		{"happy path", func(*flood.Settings) {}, ""},
		{"empty host", func(s *flood.Settings) { s.Host = "" }, "Host"},
		{"port too low", func(s *flood.Settings) { s.Port = 0 }, "Port"},
		{"port too high", func(s *flood.Settings) { s.Port = 70000 }, "Port"},
		{"tags + post-import overlap", func(s *flood.Settings) {
			s.Tags = []string{"sonarr", "shared"}
			s.PostImportTags = []string{"shared", "imported"}
		}, "PostImportTags"},
		{"tags + post-import disjoint", func(s *flood.Settings) {
			s.Tags = []string{"sonarr"}
			s.PostImportTags = []string{"imported"}
		}, ""},
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
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), nil, nil)
	if p.Name() != "Flood" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol = %v, want Torrent", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "FL" {
		t.Errorf("Definition not bound")
	}
}

func TestProxy_AuthFlow_PostsCredentialsAndCachesCookie(t *testing.T) {
	doer := newScripted()
	cookie := &http.Cookie{Name: "jwt", Value: "abc"}
	doer.queueWithCookies("/api/auth/authenticate", cookie)
	doer.queue("/api/auth/verify")
	// Second AuthVerify reuses the cached cookie — no second authenticate.
	doer.queue("/api/auth/verify")

	proxy := flood.NewProxy(doer, nil)
	if err := proxy.AuthVerify(context.Background(), newSettings()); err != nil {
		t.Fatalf("first verify err = %v", err)
	}
	if err := proxy.AuthVerify(context.Background(), newSettings()); err != nil {
		t.Fatalf("second verify err = %v", err)
	}

	// Verify the authenticate POST shape on the first call.
	var authCall *scriptedCall
	authCount := 0
	for i := range doer.calls {
		if doer.calls[i].path == "/api/auth/authenticate" {
			if authCall == nil {
				authCall = &doer.calls[i]
			}
			authCount++
		}
	}
	if authCount != 1 {
		t.Errorf("auth-call count = %d, want 1 (second verify hit the cookie cache)", authCount)
	}
	if authCall == nil {
		t.Fatal("no auth/authenticate call recorded")
	}
	if authCall.method != http.MethodPost {
		t.Errorf("auth method = %q, want POST", authCall.method)
	}
	var bodyParsed map[string]any
	_ = json.Unmarshal(authCall.body, &bodyParsed)
	if bodyParsed["username"] != "u" || bodyParsed["password"] != "p" {
		t.Errorf("auth body = %v, want {username:u, password:p}", bodyParsed)
	}

	// Verify the cookie was replayed on the verify GETs.
	verifyCookieFound := false
	for _, c := range doer.calls {
		if c.path == "/api/auth/verify" {
			for _, ck := range c.cookies {
				if ck.Name == "jwt" && ck.Value == "abc" {
					verifyCookieFound = true
				}
			}
		}
	}
	if !verifyCookieFound {
		t.Errorf("jwt cookie not replayed on verify call; cookies recorded: %+v", doer.calls)
	}
}

func TestProxy_AuthVerify_FreshAuthThenVerifyFails_ReturnsErrAuthNoRetry(t *testing.T) {
	// First-ever AuthVerify: no cached cookie → fresh authenticate
	// + resource call. Verify rejects with 401 → ErrAuth surfaces
	// immediately. The retry branch is SKIPPED because the cookies
	// were fresh (re-running the same credentials would just fail
	// the same way).
	doer := newScripted()
	doer.queueWithCookies("/api/auth/authenticate", &http.Cookie{Name: "jwt", Value: "first"})
	doer.queueUnauthorized("/api/auth/verify")
	proxy := flood.NewProxy(doer, nil)
	err := proxy.AuthVerify(context.Background(), newSettings())
	if !errors.Is(err, flood.ErrAuth) {
		t.Errorf("err = %v, want ErrAuth", err)
	}
	// Verify only ONE auth/authenticate call was made.
	count := 0
	for _, c := range doer.calls {
		if c.path == "/api/auth/authenticate" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("auth/authenticate count = %d, want 1 (no retry on fresh-auth ErrAuth)", count)
	}
}

func TestProxy_AuthVerify_StaleCachedCookie_RetriesWithReauth(t *testing.T) {
	// Pre-warm the cache: a first AuthVerify lands cookies. A
	// follow-up AuthVerify against a stale-cookie scenario should
	// trigger one reauth + retry.
	doer := newScripted()
	doer.queueWithCookies("/api/auth/authenticate", &http.Cookie{Name: "jwt", Value: "v1"})
	doer.queue("/api/auth/verify") // first verify succeeds → cookies cached
	// Second verify fails (cookie went stale server-side).
	doer.queueUnauthorized("/api/auth/verify")
	// Forced reauth issues fresh cookies.
	doer.queueWithCookies("/api/auth/authenticate", &http.Cookie{Name: "jwt", Value: "v2"})
	// Retried verify succeeds.
	doer.queue("/api/auth/verify")

	proxy := flood.NewProxy(doer, nil)
	if err := proxy.AuthVerify(context.Background(), newSettings()); err != nil {
		t.Fatalf("first verify err = %v", err)
	}
	if err := proxy.AuthVerify(context.Background(), newSettings()); err != nil {
		t.Fatalf("second verify (after stale-cookie retry) err = %v", err)
	}
	// Exactly two authenticate calls (initial + reauth) and three
	// verify calls (success / stale-fail / retry-success).
	authCount, verifyCount := 0, 0
	for _, c := range doer.calls {
		switch c.path {
		case "/api/auth/authenticate":
			authCount++
		case "/api/auth/verify":
			verifyCount++
		}
	}
	if authCount != 2 || verifyCount != 3 {
		t.Errorf("authenticate=%d verify=%d, want 2/3", authCount, verifyCount)
	}
}

func TestProxy_AuthFlow_AuthFailureOnAuthenticate(t *testing.T) {
	doer := newScripted()
	doer.queueUnauthorized("/api/auth/authenticate")
	proxy := flood.NewProxy(doer, nil)
	err := proxy.AuthVerify(context.Background(), newSettings())
	if !errors.Is(err, flood.ErrAuth) {
		t.Errorf("err = %v, want ErrAuth", err)
	}
}

func TestProxy_AuthFlow_TransportFailure(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/api/auth/authenticate", errors.New("dial tcp"))
	proxy := flood.NewProxy(doer, nil)
	err := proxy.AuthVerify(context.Background(), newSettings())
	if !errors.Is(err, flood.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProxy_AuthFlow_UrlBaseHonoured(t *testing.T) {
	doer := newScripted()
	doer.queueWithCookies("/flood/api/auth/authenticate", &http.Cookie{Name: "jwt", Value: "x"})
	doer.queue("/flood/api/auth/verify")
	proxy := flood.NewProxy(doer, nil)
	s := newSettings()
	s.UrlBase = "/flood"
	if err := proxy.AuthVerify(context.Background(), s); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestProxy_AuthFlow_HttpsScheme(t *testing.T) {
	doer := newScripted()
	doer.queueWithCookies("/api/auth/authenticate", &http.Cookie{Name: "jwt", Value: "x"})
	doer.queue("/api/auth/verify")
	proxy := flood.NewProxy(doer, nil)
	s := newSettings()
	s.UseSsl = true
	if err := proxy.AuthVerify(context.Background(), s); err != nil {
		t.Fatalf("err = %v", err)
	}
	// The captured request URL isn't surfaced by scriptedDoer; this
	// test exists to lock the build path (no scheme=https crash from
	// http.NewRequest validating the scheme).
}

func TestProvider_Test_HappyPath(t *testing.T) {
	doer := newScripted()
	doer.queueWithCookies("/api/auth/authenticate", &http.Cookie{Name: "jwt", Value: "x"})
	doer.queue("/api/auth/verify")
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
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
	s.Host = ""
	doer := newScripted()
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) == 0 {
		t.Fatal("failures empty, want Host failure")
	}
	if len(doer.calls) != 0 {
		t.Errorf("proxy called despite Settings.Validate failure")
	}
}

func TestProvider_Test_AuthFailureSurfacesPassword(t *testing.T) {
	doer := newScripted()
	doer.queueUnauthorized("/api/auth/authenticate")
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || failures[0].PropertyName != "Password" {
		t.Errorf("failures = %+v, want Password failure", failures)
	}
}

func TestProvider_Test_TransportFailureSurfacesHost(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/api/auth/authenticate", errors.New("dial tcp"))
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
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
			p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(newScripted(), nil), nil)
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
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(newScripted(), nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"}); !errors.Is(err, flood.ErrNotImplemented) {
		t.Errorf("Download err = %v, want ErrNotImplemented", err)
	}
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true); !errors.Is(err, flood.ErrNotImplemented) {
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
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(newScripted(), nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestRegistry_RegistersFlood(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(flood.Implementation, flood.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", flood.Implementation, flood.ConfigContract)
	}
}
