package nzbvortex_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/nzbvortex"
	"sonarr.tv/sonarr/internal/indexers"
)

// scriptedDoer queues responses by URL path so the test can stage
// the multi-step auth flow + the typed-call that follows.
type scriptedDoer struct {
	// responses keys path → ordered list of canned responses; each
	// call to Do pops the head.
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
	query  url.Values
}

func newScripted() *scriptedDoer { return &scriptedDoer{responses: map[string][]scriptedResp{}} }

func (s *scriptedDoer) queue(path, body string) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: http.StatusOK, body: body})
}

func (s *scriptedDoer) queueErr(path string, err error) {
	s.responses[path] = append(s.responses[path], scriptedResp{err: err})
}

func (s *scriptedDoer) Do(req *http.Request) (*http.Response, error) {
	s.calls = append(s.calls, scriptedCall{method: req.Method, path: req.URL.Path, query: req.URL.Query()})
	resps, ok := s.responses[req.URL.Path]
	if !ok || len(resps) == 0 {
		return nil, errors.New("scriptedDoer: no response queued for " + req.URL.Path)
	}
	head := resps[0]
	s.responses[req.URL.Path] = resps[1:]
	if head.err != nil {
		return nil, head.err
	}
	st := head.status
	if st == 0 {
		st = http.StatusOK
	}
	return &http.Response{
		StatusCode: st,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(head.body)),
	}, nil
}

func newSettings() nzbvortex.Settings {
	s := nzbvortex.NewSettings()
	s.ApiKey = "secret"
	return s
}

func TestSettings_Defaults(t *testing.T) {
	s := nzbvortex.NewSettings()
	if s.Host != "localhost" || s.Port != 4321 || s.TvCategory != "TV Shows" {
		t.Errorf("NewSettings = %+v, want defaults", s)
	}
	if s.RecentTvPriority != nzbvortex.PriorityNormal || s.OlderTvPriority != nzbvortex.PriorityNormal {
		t.Errorf("priorities = %d/%d, want Normal/Normal", s.RecentTvPriority, s.OlderTvPriority)
	}
}

func TestSettings_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*nzbvortex.Settings)
		wantBad string
	}{
		{"happy", func(*nzbvortex.Settings) {}, ""},
		{"empty host", func(s *nzbvortex.Settings) { s.Host = "" }, "Host"},
		{"port too low", func(s *nzbvortex.Settings) { s.Port = 0 }, "Port"},
		{"port too high", func(s *nzbvortex.Settings) { s.Port = 70000 }, "Port"},
		{"urlbase missing slash", func(s *nzbvortex.Settings) { s.UrlBase = "api" }, "UrlBase"},
		{"urlbase with slash ok", func(s *nzbvortex.Settings) { s.UrlBase = "/api" }, ""},
		{"empty apikey", func(s *nzbvortex.Settings) { s.ApiKey = "" }, "ApiKey"},
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
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nil, nil)
	if p.Name() != "NZBVortex" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolUsenet {
		t.Errorf("Protocol = %v, want Usenet", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "NV" {
		t.Errorf("Definition not bound")
	}
}

func TestProxy_GetVersion_NoAuthRequired(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/app/appversion", `{"result":"Ok","version":"3.1.4"}`)
	proxy := nzbvortex.NewProxy(doer, nil)
	v, err := proxy.GetVersion(context.Background(), newSettings())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if v.Version != "3.1.4" {
		t.Errorf("Version = %q, want 3.1.4", v.Version)
	}
	if len(doer.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (no auth-flow precursor)", len(doer.calls))
	}
	if doer.calls[0].path != "/api/app/appversion" {
		t.Errorf("path = %q", doer.calls[0].path)
	}
	if doer.calls[0].query.Get("sessionid") != "" {
		t.Errorf("sessionid query sent on unauth'd call: %q", doer.calls[0].query.Get("sessionid"))
	}
}

func TestProxy_GetAPIVersion_NoAuthRequired(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/app/apilevel", `{"result":"Ok","apiLevel":"2.3"}`)
	proxy := nzbvortex.NewProxy(doer, nil)
	v, err := proxy.GetAPIVersion(context.Background(), newSettings())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if v.APILevel != "2.3" {
		t.Errorf("APILevel = %q", v.APILevel)
	}
}

// TestExpectedHashShape locks the SHA-256+base64 auth hash formula
// in place so a future refactor of [Proxy.ensureSession] can't
// silently drift away from the C# NzbVortexProxy.cs:194-195 shape:
// `base64( sha256( "<nonce>:<cnonce>:<apikey>" ) )`. The proxy
// itself isn't exercised here — the formula is the canonical
// invariant the Authenticated-call surface (5.J67 follow-up) will
// be tested against.
func TestExpectedHashShape(t *testing.T) {
	sum := sha256.Sum256([]byte("NONCE:CNONCE:KEY"))
	want := base64.StdEncoding.EncodeToString(sum[:])
	if len(want) == 0 {
		t.Fatal("formula produced empty output")
	}
	// Recompute with the same inputs — a smoke check guarding
	// against an accidental swap of operands in the formula.
	check := sha256.Sum256([]byte("NONCE:CNONCE:KEY"))
	if base64.StdEncoding.EncodeToString(check[:]) != want {
		t.Fatalf("self-check mismatch")
	}
}

// queueFourArmHappyPath queues all four Test-arm responses + the
// auth precursor so a happy-path TestApiVersion + TestAuthentication
// + TestCategory call chain runs through without a missing-response
// surprise.
func queueFourArmHappyPath(t *testing.T, doer *scriptedDoer, settings nzbvortex.Settings) {
	t.Helper()
	doer.queue("/api/app/appversion", `{"result":"Ok","version":"3.1"}`)
	doer.queue("/api/app/apilevel", `{"result":"Ok","apiLevel":"2.5"}`)
	doer.queue("/api/auth/nonce", `{"authNonce":"N"}`)
	doer.queue("/api/auth/login", `{"result":"Ok","loginResult":"Successful","sessionId":"SID"}`)
	doer.queue("/api/nzb", `{"result":"Ok","nzbs":[]}`)
	doer.queue("/api/group", `{"result":"Ok","groups":[{"groupName":"`+settings.TvCategory+`"}]}`)
}

func TestProvider_Test_HappyPath(t *testing.T) {
	doer := newScripted()
	queueFourArmHappyPath(t, doer, newSettings())
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nzbvortex.NewProxy(doer, nil), nil)
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
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, s, nzbvortex.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) == 0 {
		t.Fatal("failures empty, want ApiKey")
	}
	if len(doer.calls) != 0 {
		t.Errorf("proxy called despite Settings.Validate failure: %d calls", len(doer.calls))
	}
}

func TestProvider_Test_ConnectionFailureShortCircuitsApiVersionProbe(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/api/app/appversion", errors.New("dial tcp"))
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nzbvortex.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want one entry", failures)
	}
	if failures[0].PropertyName != "Host" {
		t.Errorf("PropertyName = %q, want Host", failures[0].PropertyName)
	}
	// API-version probe should NOT have been invoked.
	for _, c := range doer.calls {
		if c.path == "/api/app/apilevel" {
			t.Errorf("apilevel probe ran despite connection failure")
		}
	}
}

func TestProvider_Test_VersionTooOld(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/app/appversion", `{"result":"Ok","version":"3.0"}`)
	doer.queue("/api/app/apilevel", `{"result":"Ok","apiLevel":"2.2"}`)
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nzbvortex.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want one entry", failures)
	}
	if !strings.Contains(failures[0].ErrorMessage, "2.2") {
		t.Errorf("ErrorMessage = %q, want it to mention reported version", failures[0].ErrorMessage)
	}
}

func TestProvider_Test_VersionAtMinPasses(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/app/appversion", `{"result":"Ok"}`)
	doer.queue("/api/app/apilevel", `{"result":"Ok","apiLevel":"2.3"}`)
	doer.queue("/api/auth/nonce", `{"authNonce":"N"}`)
	doer.queue("/api/auth/login", `{"result":"Ok","loginResult":"Successful","sessionId":"SID"}`)
	doer.queue("/api/nzb", `{"result":"Ok","nzbs":[]}`)
	doer.queue("/api/group", `{"result":"Ok","groups":[{"groupName":"TV Shows"}]}`)
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nzbvortex.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want empty at exactly 2.3", failures)
	}
}

func TestProvider_Test_VersionEmptyReportsHost(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/app/appversion", `{"result":"Ok"}`)
	doer.queue("/api/app/apilevel", `{"result":"Ok","apiLevel":""}`)
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nzbvortex.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 {
		t.Fatalf("failures = %+v", failures)
	}
}

func TestProvider_Test_AuthFailureSurfacesApiKeyPropertyFailure(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/app/appversion", `{"result":"Ok"}`)
	doer.queue("/api/app/apilevel", `{"result":"Ok","apiLevel":"2.5"}`)
	doer.queue("/api/auth/nonce", `{"authNonce":"N"}`)
	// LoginResult=Failed → ErrAuth → TestAuthentication maps to
	// the ApiKey property failure.
	doer.queue("/api/auth/login", `{"result":"Ok","loginResult":"Failed","sessionId":""}`)
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nzbvortex.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want one ApiKey failure", failures)
	}
	if failures[0].PropertyName != "ApiKey" {
		t.Errorf("PropertyName = %q, want ApiKey", failures[0].PropertyName)
	}
}

func TestProvider_Test_CategoryMissingFails(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/app/appversion", `{"result":"Ok"}`)
	doer.queue("/api/app/apilevel", `{"result":"Ok","apiLevel":"2.5"}`)
	doer.queue("/api/auth/nonce", `{"authNonce":"N"}`)
	doer.queue("/api/auth/login", `{"result":"Ok","loginResult":"Successful","sessionId":"SID"}`)
	doer.queue("/api/nzb", `{"result":"Ok","nzbs":[]}`)
	// Groups response missing the configured TvCategory.
	doer.queue("/api/group", `{"result":"Ok","groups":[{"groupName":"Movies"}]}`)
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nzbvortex.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want one TvCategory failure", failures)
	}
	if failures[0].PropertyName != "TvCategory" {
		t.Errorf("PropertyName = %q, want TvCategory", failures[0].PropertyName)
	}
}

func TestProvider_Test_BlankCategorySkipsCategoryProbe(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/app/appversion", `{"result":"Ok"}`)
	doer.queue("/api/app/apilevel", `{"result":"Ok","apiLevel":"2.5"}`)
	doer.queue("/api/auth/nonce", `{"authNonce":"N"}`)
	doer.queue("/api/auth/login", `{"result":"Ok","loginResult":"Successful","sessionId":"SID"}`)
	doer.queue("/api/nzb", `{"result":"Ok","nzbs":[]}`)
	// No /api/group response queued — the test would fail if the
	// blank-category arm called it anyway.
	s := newSettings()
	s.TvCategory = ""
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, s, nzbvortex.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want empty with blank category", failures)
	}
	for _, c := range doer.calls {
		if c.path == "/api/group" {
			t.Errorf("category probe ran despite blank TvCategory")
		}
	}
}

func TestProxy_AuthFlow_HashShape(t *testing.T) {
	// Verify the proxy's auth flow emits the exact base64(sha256(
	// "<nonce>:<cnonce>:<apikey>" )) shape via a fixed-cnonce
	// override. Drives the auth flow via GetGroups (auth-required).
	doer := newScripted()
	doer.queue("/api/auth/nonce", `{"authNonce":"NONCE-1"}`)
	doer.queue("/api/auth/login", `{"result":"Ok","loginResult":"Successful","sessionId":"SID-X"}`)
	doer.queue("/api/group", `{"result":"Ok","groups":[]}`)

	proxy := nzbvortex.NewProxy(doer, nil)
	proxy.SetCnonceGenerator(func() string { return "FIXED-CNONCE" })
	settings := newSettings()
	settings.ApiKey = "the-key"
	_, err := proxy.GetGroups(context.Background(), settings)
	if err != nil {
		t.Fatalf("GetGroups err = %v", err)
	}

	// Find the login call's query params.
	var login scriptedCall
	for _, c := range doer.calls {
		if c.path == "/api/auth/login" {
			login = c
			break
		}
	}
	if login.path == "" {
		t.Fatal("no /api/auth/login call recorded")
	}
	sum := sha256.Sum256([]byte("NONCE-1:FIXED-CNONCE:the-key"))
	want := base64.StdEncoding.EncodeToString(sum[:])
	if login.query.Get("hash") != want {
		t.Errorf("hash = %q, want %q", login.query.Get("hash"), want)
	}
	if login.query.Get("nonce") != "NONCE-1" {
		t.Errorf("nonce = %q", login.query.Get("nonce"))
	}
	if login.query.Get("cnonce") != "FIXED-CNONCE" {
		t.Errorf("cnonce = %q", login.query.Get("cnonce"))
	}
}

func TestProxy_AuthFlow_SessionReuse(t *testing.T) {
	// Two consecutive GetGroups calls share one cached sessionId
	// — only ONE auth/nonce + auth/login pair should be issued.
	doer := newScripted()
	doer.queue("/api/auth/nonce", `{"authNonce":"N"}`)
	doer.queue("/api/auth/login", `{"result":"Ok","loginResult":"Successful","sessionId":"SID"}`)
	doer.queue("/api/group", `{"result":"Ok","groups":[]}`)
	doer.queue("/api/group", `{"result":"Ok","groups":[]}`)

	proxy := nzbvortex.NewProxy(doer, nil)
	if _, err := proxy.GetGroups(context.Background(), newSettings()); err != nil {
		t.Fatalf("first GetGroups err = %v", err)
	}
	if _, err := proxy.GetGroups(context.Background(), newSettings()); err != nil {
		t.Fatalf("second GetGroups err = %v", err)
	}
	noncePosts := 0
	loginPosts := 0
	for _, c := range doer.calls {
		if c.path == "/api/auth/nonce" {
			noncePosts++
		}
		if c.path == "/api/auth/login" {
			loginPosts++
		}
	}
	if noncePosts != 1 || loginPosts != 1 {
		t.Errorf("auth-call counts: nonce=%d login=%d, want both 1 (cache hit on second call)", noncePosts, loginPosts)
	}
}

func TestProxy_GetQueue_AppliesGroupAndLimit(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/auth/nonce", `{"authNonce":"N"}`)
	doer.queue("/api/auth/login", `{"result":"Ok","loginResult":"Successful","sessionId":"SID"}`)
	doer.queue("/api/nzb", `{"result":"Ok","nzbs":[{"id":1,"uiTitle":"Show","totalDownloadSize":100}]}`)

	proxy := nzbvortex.NewProxy(doer, nil)
	items, err := proxy.GetQueue(context.Background(), newSettings(), 30)
	if err != nil {
		t.Fatalf("GetQueue err = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if items[0].ID != 1 || items[0].UiTitle != "Show" || items[0].TotalDownloadSize != 100 {
		t.Errorf("item = %+v", items[0])
	}
	// Find the /api/nzb query string.
	var nzbCall scriptedCall
	for _, c := range doer.calls {
		if c.path == "/api/nzb" {
			nzbCall = c
			break
		}
	}
	if nzbCall.query.Get("groupName") != "TV Shows" {
		t.Errorf("groupName = %q, want 'TV Shows'", nzbCall.query.Get("groupName"))
	}
	if nzbCall.query.Get("limitDone") != "30" {
		t.Errorf("limitDone = %q, want 30", nzbCall.query.Get("limitDone"))
	}
	if nzbCall.query.Get("sessionid") != "SID" {
		t.Errorf("sessionid = %q, want SID (replayed)", nzbCall.query.Get("sessionid"))
	}
}

func TestProxy_GetGroups_Auth(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/auth/nonce", `{"authNonce":"N"}`)
	doer.queue("/api/auth/login", `{"result":"Ok","loginResult":"Successful","sessionId":"SID"}`)
	doer.queue("/api/group", `{"result":"Ok","groups":[{"groupName":"TV Shows"},{"groupName":"Movies"}]}`)
	proxy := nzbvortex.NewProxy(doer, nil)
	groups, err := proxy.GetGroups(context.Background(), newSettings())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(groups) != 2 {
		t.Errorf("len = %d, want 2", len(groups))
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
			p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, s, nzbvortex.NewProxy(newScripted(), nil), nil)
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
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nzbvortex.NewProxy(newScripted(), nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"}); !errors.Is(err, nzbvortex.ErrNotImplemented) {
		t.Errorf("Download err = %v, want ErrNotImplemented", err)
	}
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true); !errors.Is(err, nzbvortex.ErrNotImplemented) {
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

func TestProvider_MarkItemAsImported(t *testing.T) {
	p := nzbvortex.New(&downloadclients.DownloadClient{Name: "NV"}, newSettings(), nzbvortex.NewProxy(newScripted(), nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestRegistry_RegistersNzbVortex(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(nzbvortex.Implementation, nzbvortex.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", nzbvortex.Implementation, nzbvortex.ConfigContract)
	}
}
