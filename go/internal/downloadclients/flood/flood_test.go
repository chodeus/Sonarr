package flood_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/flood"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
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

// queueBody is a body-aware variant for tests that need the SAB
// response shape (e.g. J118 GetClientSettings tests).
func (s *scriptedDoer) queueBody(path, body string) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: http.StatusOK, body: body})
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

// --- J118 GetStatus OutputRootFolders projection ----------------

func TestProvider_GetStatus_J118_DestinationSettingShortCircuits(t *testing.T) {
	// Settings.Destination non-empty → no proxy call, OutputRootFolders
	// uses the setting verbatim.
	doer := newScripted()
	s := newSettings()
	s.Destination = "/data/from-setting"
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(doer, nil), nil)
	info, err := p.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].FullPath() != "/data/from-setting" {
		t.Errorf("OutputRootFolders=%v, want [/data/from-setting]", info.OutputRootFolders)
	}
	// No proxy traffic — the auth + client/settings calls did NOT fire.
	if len(doer.calls) != 0 {
		t.Errorf("doer fired %d calls; want 0 (Destination short-circuits)", len(doer.calls))
	}
}

func TestProvider_GetStatus_J118_FetchesDirectoryDefaultFromProxy(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queueBody("/api/client/settings", `{"directoryDefault":"/data/flood-default"}`)
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	info, err := p.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].FullPath() != "/data/flood-default" {
		t.Errorf("OutputRootFolders=%v, want [/data/flood-default]", info.OutputRootFolders)
	}
}

func TestProvider_GetStatus_J118_ClientSettingsFailureDegradesToIsLocalhostOnly(t *testing.T) {
	// No /client/settings handler queued → call fails. Non-fatal:
	// GetStatus returns just IsLocalhost.
	doer := newScripted()
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	info, err := p.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders=%v, want empty on GetClientSettings failure", info.OutputRootFolders)
	}
}

func TestProvider_GetStatus_J118_RemotePathMapperApplied(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queueBody("/api/client/settings", `{"directoryDefault":"/remote/flood"}`)
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetRemotePathMapper(stubRemapper{from: "/remote/flood", to: "/local/flood"})
	info, err := p.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].FullPath() != "/local/flood" {
		t.Errorf("OutputRootFolders=%v, want [/local/flood] (remapped)", info.OutputRootFolders)
	}
}

func TestProvider_GetStatus_J118_EmptyDirectoryDefault(t *testing.T) {
	// Server responds but DirectoryDefault is empty → no
	// OutputRootFolders. IsLocalhost still surfaces.
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queueBody("/api/client/settings", `{"directoryDefault":""}`)
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	info, err := p.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders=%v, want empty on blank DirectoryDefault", info.OutputRootFolders)
	}
}

// (5.J74 closed the arc — Download, GetItems, RemoveItem,
// MarkItemAsImported, and GetImportItem are all wired. Per-method
// tests cover the behaviour individually.)

// queueAuthOnce queues the authenticate response + a cookie so
// subsequent authenticated calls find a fresh cookie in the cache.
func queueAuthOnce(doer *scriptedDoer) {
	doer.queueWithCookies("/api/auth/authenticate", &http.Cookie{Name: "jwt", Value: "x"})
}

func TestProvider_Download_NilRequest(t *testing.T) {
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(newScripted(), nil), nil)
	if _, err := p.Download(context.Background(), nil); err == nil {
		t.Fatal("err = nil, want nil-request error")
	}
}

func TestProvider_Download_MissingInfoHash(t *testing.T) {
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(newScripted(), nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{TorrentBody: []byte("x")}); err == nil {
		t.Fatal("err = nil, want missing-InfoHash error")
	}
}

func TestProvider_Download_NoBodyOrMagnet(t *testing.T) {
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(newScripted(), nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{InfoHash: "abc"}); err == nil {
		t.Fatal("err = nil, want neither-body-nor-magnet error")
	}
}

func TestProvider_Download_TorrentBody_RequestShape(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queue("/api/torrents/add-files")

	settings := newSettings()
	settings.Tags = []string{"sonarr", "tv"}
	settings.Destination = "/downloads/tv"
	settings.StartOnAdd = true
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, settings, flood.NewProxy(doer, nil), nil)

	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X.S01E01",
		TorrentBody:  []byte("RAW-TORRENT-BYTES"),
		InfoHash:     "deadbeef",
	})
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	if id != "DEADBEEF" {
		t.Errorf("id = %q, want DEADBEEF (uppercased InfoHash)", id)
	}

	var addCall *scriptedCall
	for i := range doer.calls {
		if doer.calls[i].path == "/api/torrents/add-files" {
			addCall = &doer.calls[i]
			break
		}
	}
	if addCall == nil {
		t.Fatal("no /api/torrents/add-files call recorded")
	}
	if addCall.method != http.MethodPost {
		t.Errorf("method = %q, want POST", addCall.method)
	}
	var body map[string]any
	if err := json.Unmarshal(addCall.body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	files, _ := body["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v, want 1-element slice", body["files"])
	}
	encoded, _ := files[0].(string)
	decoded, _ := base64.StdEncoding.DecodeString(encoded)
	if string(decoded) != "RAW-TORRENT-BYTES" {
		t.Errorf("base64-decoded files[0] = %q, want RAW-TORRENT-BYTES", decoded)
	}
	tags, _ := body["tags"].([]any)
	if len(tags) != 2 || tags[0] != "sonarr" || tags[1] != "tv" {
		t.Errorf("tags = %v, want [sonarr, tv]", body["tags"])
	}
	if body["destination"] != "/downloads/tv" {
		t.Errorf("destination = %v, want /downloads/tv", body["destination"])
	}
	if body["start"] != true {
		t.Errorf("start = %v, want true", body["start"])
	}
}

func TestProvider_Download_Magnet_RequestShape(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queue("/api/torrents/add-urls")

	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:cafebabe",
		InfoHash:     "cafebabe",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "CAFEBABE" {
		t.Errorf("id = %q, want CAFEBABE", id)
	}
	var addCall *scriptedCall
	for i := range doer.calls {
		if doer.calls[i].path == "/api/torrents/add-urls" {
			addCall = &doer.calls[i]
		}
	}
	if addCall == nil {
		t.Fatal("no /api/torrents/add-urls call recorded")
	}
	var body map[string]any
	_ = json.Unmarshal(addCall.body, &body)
	urls, _ := body["urls"].([]any)
	if len(urls) != 1 || urls[0] != "magnet:?xt=urn:btih:cafebabe" {
		t.Errorf("urls = %v, want single magnet URL", body["urls"])
	}
}

func TestProvider_Download_BlankDestinationOmitsKey(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queue("/api/torrents/add-files")

	settings := newSettings()
	settings.Destination = "" // blank
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, settings, flood.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		TorrentBody: []byte("x"),
		InfoHash:    "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, c := range doer.calls {
		if c.path == "/api/torrents/add-files" {
			var body map[string]any
			_ = json.Unmarshal(c.body, &body)
			if _, present := body["destination"]; present {
				t.Errorf("destination key emitted with blank value: %v", body)
			}
		}
	}
}

func TestProvider_Download_StartOnAddFalseOmitsKey(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queue("/api/torrents/add-files")

	settings := newSettings()
	settings.StartOnAdd = false
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, settings, flood.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		TorrentBody: []byte("x"),
		InfoHash:    "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, c := range doer.calls {
		if c.path == "/api/torrents/add-files" {
			var body map[string]any
			_ = json.Unmarshal(c.body, &body)
			if _, present := body["start"]; present {
				t.Errorf("start key emitted with StartOnAdd=false: %v", body)
			}
		}
	}
}

func TestProvider_Download_TagsBlanksStripped(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queue("/api/torrents/add-files")

	settings := newSettings()
	settings.Tags = []string{"sonarr", "", "  ", "tv"}
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, settings, flood.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		TorrentBody: []byte("x"),
		InfoHash:    "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, c := range doer.calls {
		if c.path == "/api/torrents/add-files" {
			var body map[string]any
			_ = json.Unmarshal(c.body, &body)
			tags, _ := body["tags"].([]any)
			if len(tags) != 2 {
				t.Errorf("tags = %v, want 2 entries after blank-strip", body["tags"])
			}
		}
	}
}

func TestProvider_Download_PropagatesProxyError(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queueErr("/api/torrents/add-files", errors.New("dial"))
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		TorrentBody: []byte("x"),
		InfoHash:    "abc",
	})
	if !errors.Is(err, flood.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProvider_MarkItemAsImported_BlankPostImportTagsSkips(t *testing.T) {
	// PostImportTags empty → MarkItemAsImported returns nil without
	// touching the proxy.
	doer := newScripted()
	s := newSettings()
	s.PostImportTags = nil
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(doer, nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "h"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(doer.calls) != 0 {
		t.Errorf("proxy called despite blank PostImportTags: %d calls", len(doer.calls))
	}
}

func TestProvider_MarkItemAsImported_PatchTagsForMatchingHash(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.responses["/api/torrents"] = append(doer.responses["/api/torrents"], scriptedResp{
		status: http.StatusOK,
		body:   `{"torrents":{"h":{"name":"X","tags":["sonarr","tv"]}}}`,
	})
	doer.queue("/api/torrents/tags")

	s := newSettings()
	s.PostImportTags = []string{"imported"}
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(doer, nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "h"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	var patchCall *scriptedCall
	for i := range doer.calls {
		if doer.calls[i].path == "/api/torrents/tags" {
			patchCall = &doer.calls[i]
		}
	}
	if patchCall == nil {
		t.Fatal("no /api/torrents/tags call recorded")
	}
	if patchCall.method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", patchCall.method)
	}
	var body map[string]any
	_ = json.Unmarshal(patchCall.body, &body)
	hashes, _ := body["hashes"].([]any)
	if len(hashes) != 1 || hashes[0] != "h" {
		t.Errorf("hashes = %v, want [h]", body["hashes"])
	}
	tags, _ := body["tags"].([]any)
	if len(tags) != 3 {
		t.Errorf("tags = %v, want 3 entries (sonarr, tv, imported)", body["tags"])
	}
}

func TestProvider_MarkItemAsImported_NoMatchingHashSkipsPatch(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.responses["/api/torrents"] = append(doer.responses["/api/torrents"], scriptedResp{
		status: http.StatusOK,
		body:   `{"torrents":{"other":{"name":"X","tags":["sonarr"]}}}`,
	})
	s := newSettings()
	s.PostImportTags = []string{"imported"}
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(doer, nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "missing"}); err != nil {
		t.Errorf("err = %v, want nil (no-match silently skips)", err)
	}
	// No PATCH call should have been issued.
	for _, c := range doer.calls {
		if c.path == "/api/torrents/tags" {
			t.Errorf("PATCH was issued despite no matching hash: %v", c)
		}
	}
}

func TestProvider_RemoveItem_DispatchesDelete(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queue("/api/torrents/delete")

	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABCDEF"}, true); err != nil {
		t.Fatalf("err = %v", err)
	}
	var deleteCall *scriptedCall
	for i := range doer.calls {
		if doer.calls[i].path == "/api/torrents/delete" {
			deleteCall = &doer.calls[i]
		}
	}
	if deleteCall == nil {
		t.Fatal("no /api/torrents/delete call recorded")
	}
	if deleteCall.method != http.MethodPost {
		t.Errorf("method = %q, want POST", deleteCall.method)
	}
	var body map[string]any
	_ = json.Unmarshal(deleteCall.body, &body)
	hashes, _ := body["hashes"].([]any)
	if len(hashes) != 1 || hashes[0] != "ABCDEF" {
		t.Errorf("hashes = %v, want [ABCDEF]", body["hashes"])
	}
	if body["deleteData"] != true {
		t.Errorf("deleteData = %v, want true", body["deleteData"])
	}
}

func TestProvider_RemoveItem_DeleteDataFalse(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queue("/api/torrents/delete")
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "h"}, false); err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, c := range doer.calls {
		if c.path == "/api/torrents/delete" {
			var body map[string]any
			_ = json.Unmarshal(c.body, &body)
			if body["deleteData"] != false {
				t.Errorf("deleteData = %v, want false", body["deleteData"])
			}
		}
	}
}

func TestProvider_RemoveItem_NilItem(t *testing.T) {
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(newScripted(), nil), nil)
	if err := p.RemoveItem(context.Background(), nil, true); err == nil {
		t.Fatal("err = nil, want nil-item error")
	}
}

func TestProvider_RemoveItem_EmptyDownloadID(t *testing.T) {
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(newScripted(), nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true); err == nil {
		t.Fatal("err = nil, want empty-DownloadID error")
	}
}

func TestProvider_RemoveItem_PropagatesProxyError(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queueErr("/api/torrents/delete", errors.New("dial"))
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "h"}, true); !errors.Is(err, flood.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProvider_GetImportItem_SingleFile(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.responses["/api/torrents/h/contents"] = append(doer.responses["/api/torrents/h/contents"], scriptedResp{
		status: http.StatusOK,
		body:   `[{"path":"file.mkv"}]`,
	})
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	item := &downloadclients.Item{DownloadID: "h", OutputPath: ospath.New("/local/dl")}
	out, err := p.GetImportItem(context.Background(), item, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.OutputPath.FullPath() != "/local/dl/file.mkv" {
		t.Errorf("OutputPath = %q, want /local/dl/file.mkv (single-file branch)", out.OutputPath.FullPath())
	}
}

func TestProvider_GetImportItem_MultiFileSharedBase(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.responses["/api/torrents/h/contents"] = append(doer.responses["/api/torrents/h/contents"], scriptedResp{
		status: http.StatusOK,
		body:   `[{"path":"Show.S01/ep01.mkv"},{"path":"Show.S01/ep02.mkv"}]`,
	})
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	item := &downloadclients.Item{DownloadID: "h", OutputPath: ospath.New("/local/dl")}
	out, _ := p.GetImportItem(context.Background(), item, nil)
	if out.OutputPath.FullPath() != "/local/dl/Show.S01" {
		t.Errorf("OutputPath = %q, want /local/dl/Show.S01 (shared-base branch)", out.OutputPath.FullPath())
	}
}

func TestProvider_GetImportItem_MultiFileDivergentBase(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.responses["/api/torrents/h/contents"] = append(doer.responses["/api/torrents/h/contents"], scriptedResp{
		status: http.StatusOK,
		body:   `[{"path":"alpha/ep01.mkv"},{"path":"beta/ep02.mkv"}]`,
	})
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	item := &downloadclients.Item{DownloadID: "h", OutputPath: ospath.New("/local/dl")}
	out, _ := p.GetImportItem(context.Background(), item, nil)
	// Divergent first-segment → OutputPath stays at the item's base.
	if out.OutputPath.FullPath() != "/local/dl" {
		t.Errorf("OutputPath = %q, want /local/dl (divergent first-segment leaves it untouched)", out.OutputPath.FullPath())
	}
}

func TestProvider_GetImportItem_EmptyContentsSurfacesUnavailable(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.responses["/api/torrents/h/contents"] = append(doer.responses["/api/torrents/h/contents"], scriptedResp{
		status: http.StatusOK,
		body:   `[]`,
	})
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	_, err := p.GetImportItem(context.Background(), &downloadclients.Item{DownloadID: "h", OutputPath: ospath.New("/x")}, nil)
	if !errors.Is(err, flood.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProvider_GetImportItem_NilItem(t *testing.T) {
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(newScripted(), nil), nil)
	if _, err := p.GetImportItem(context.Background(), nil, nil); err == nil {
		t.Fatal("err = nil, want nil-item error")
	}
}

// queueGetTorrents stages the auth flow + a torrents response.
func queueGetTorrents(doer *scriptedDoer, body string) {
	queueAuthOnce(doer)
	doer.responses["/api/torrents"] = append(doer.responses["/api/torrents"], scriptedResp{status: http.StatusOK, body: body})
}

func TestProvider_GetItems_EmptyMap(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{}}`)
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len = %d, want 0", len(items))
	}
}

func TestProvider_GetItems_StatusTable(t *testing.T) {
	doer := newScripted()
	// One entry per status arm. Each torrent has [sonarr] tag to
	// pass the Tags gate.
	queueGetTorrents(doer, `{"torrents":{
		"hashA":{"name":"A","status":["seeding"],"tags":["sonarr"],"sizeBytes":100,"bytesDone":100},
		"hashB":{"name":"B","status":["complete"],"tags":["sonarr"],"sizeBytes":100,"bytesDone":100},
		"hashC":{"name":"C","status":["stopped"],"tags":["sonarr"],"sizeBytes":100,"bytesDone":50},
		"hashD":{"name":"D","status":["error"],"tags":["sonarr"],"sizeBytes":100,"bytesDone":50,"message":"corrupt"},
		"hashE":{"name":"E","status":["downloading"],"tags":["sonarr"],"sizeBytes":100,"bytesDone":50}
	}}`)
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("len = %d, want 5", len(items))
	}
	byID := map[string]*downloadclients.Item{}
	for _, it := range items {
		byID[it.DownloadID] = it
	}
	want := map[string]downloadclients.DownloadItemStatus{
		"hashA": downloadclients.DownloadItemStatusCompleted,
		"hashB": downloadclients.DownloadItemStatusCompleted,
		"hashC": downloadclients.DownloadItemStatusPaused,
		"hashD": downloadclients.DownloadItemStatusWarning,
		"hashE": downloadclients.DownloadItemStatusDownloading,
	}
	for hash, st := range want {
		got, ok := byID[hash]
		if !ok {
			t.Errorf("missing item %s", hash)
			continue
		}
		if got.Status != st {
			t.Errorf("%s Status = %v, want %v", hash, got.Status, st)
		}
	}
	if byID["hashD"].Message != "corrupt" {
		t.Errorf("error-arm Message = %q, want 'corrupt'", byID["hashD"].Message)
	}
}

func TestProvider_GetItems_TagsGateFiltersMissingRequired(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"hashA":{"name":"A","status":["downloading"],"tags":["sonarr","tv"]},
		"hashB":{"name":"B","status":["downloading"],"tags":["radarr"]}
	}}`)
	s := newSettings()
	s.Tags = []string{"sonarr"} // hashB missing the required tag
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 {
		t.Fatalf("len = %d, want 1 (Tags gate filtered hashB)", len(items))
	}
	if items[0].DownloadID != "hashA" {
		t.Errorf("DownloadID = %q, want hashA", items[0].DownloadID)
	}
}

func TestProvider_GetItems_PostImportTagsSkipAlreadyImported(t *testing.T) {
	doer := newScripted()
	// Both have [sonarr] (passes Tags gate). hashA also has
	// [imported], so the PostImportTags skip applies.
	queueGetTorrents(doer, `{"torrents":{
		"hashA":{"name":"A","status":["complete"],"tags":["sonarr","imported"]},
		"hashB":{"name":"B","status":["complete"],"tags":["sonarr"]}
	}}`)
	s := newSettings()
	s.PostImportTags = []string{"imported"}
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 {
		t.Fatalf("len = %d, want 1 (PostImportTags skip filtered hashA)", len(items))
	}
	if items[0].DownloadID != "hashB" {
		t.Errorf("DownloadID = %q, want hashB", items[0].DownloadID)
	}
}

func TestProvider_GetItems_HasPostImportCategoryFlag(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"hashA":{"name":"A","status":["downloading"],"tags":["sonarr"]}
	}}`)
	s := newSettings()
	s.PostImportTags = []string{"imported"}
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 || items[0].DownloadClientInfo == nil {
		t.Fatal("expected one item with DownloadClientInfo bound")
	}
	if !items[0].DownloadClientInfo.HasPostImportCategory {
		t.Errorf("HasPostImportCategory = false, want true with PostImportTags non-empty")
	}
}

func TestProvider_GetItems_CategoryFromFirstTag(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"hashA":{"name":"A","status":["downloading"],"tags":["myCat","other"]},
		"hashB":{"name":"B","status":["downloading"],"tags":[]}
	}}`)
	s := newSettings()
	s.Tags = []string{} // disable Tags gate so both pass
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, s, flood.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	byID := map[string]*downloadclients.Item{}
	for _, it := range items {
		byID[it.DownloadID] = it
	}
	if byID["hashA"].Category != "myCat" {
		t.Errorf("hashA Category = %q, want myCat", byID["hashA"].Category)
	}
	if byID["hashB"].Category != "" {
		t.Errorf("hashB Category = %q, want empty (no tags)", byID["hashB"].Category)
	}
}

func TestProvider_GetItems_EtaAndRemainingSize(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["downloading"],"tags":["sonarr"],"sizeBytes":1000,"bytesDone":300,"eta":42,"ratio":0.5}
	}}`)
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 {
		t.Fatalf("len = %d", len(items))
	}
	it := items[0]
	if it.RemainingSize != 700 {
		t.Errorf("RemainingSize = %d, want 700", it.RemainingSize)
	}
	if it.TotalSize != 1000 {
		t.Errorf("TotalSize = %d", it.TotalSize)
	}
	if it.RemainingTime == nil || *it.RemainingTime != 42*time.Second {
		t.Errorf("RemainingTime = %v, want 42s", it.RemainingTime)
	}
	if it.SeedRatio == nil || *it.SeedRatio != 0.5 {
		t.Errorf("SeedRatio = %v, want 0.5", it.SeedRatio)
	}
}

func TestProvider_GetItems_ZeroEtaOmitsRemainingTime(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"],"sizeBytes":100,"bytesDone":100,"eta":0}
	}}`)
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if items[0].RemainingTime != nil {
		t.Errorf("RemainingTime = %v, want nil when eta=0", items[0].RemainingTime)
	}
}

func TestProvider_GetItems_OutputPathRemapped(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"],"directory":"/remote/dl"}
	}}`)
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetRemotePathMapper(stubRemapper{from: "/remote/dl", to: "/local/dl"})
	items, _ := p.GetItems(context.Background())
	if items[0].OutputPath.FullPath() != "/local/dl" {
		t.Errorf("OutputPath = %q, want /local/dl (remapped)", items[0].OutputPath.FullPath())
	}
}

// stubSeedCfg is a [downloadclients.SeedConfigProvider] returning
// a canned config (and optional error) per-hash. Used by the J95
// cleanup-gate tests.
type stubSeedCfg struct {
	byHash map[string]*downloadclients.TorrentSeedConfiguration
	err    error
}

func (s *stubSeedCfg) GetSeedConfiguration(_ context.Context, infoHash string) (*downloadclients.TorrentSeedConfiguration, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.byHash[infoHash], nil
}

func ptrFloat(f float64) *float64           { return &f }
func ptrDur(d time.Duration) *time.Duration { return &d }
func ptrInt64(i int64) *int64               { return &i }

func TestProvider_GetItems_Cleanup_NoSeedCfgBindingDefaultsFalse(t *testing.T) {
	// J95: without an injected SeedConfigProvider the cleanup gate
	// degrades to "never satisfy" — preserves the pre-J95 false-
	// default posture.
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"]}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: true}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if items[0].CanMoveFiles || items[0].CanBeRemoved {
		t.Errorf("nil seedCfg binding: flags=%+v, want both false", items[0])
	}
}

func TestProvider_GetItems_Cleanup_RemoveCompletedFalseDefaults(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"]}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: false}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"h": {Ratio: ptrFloat(0.1)},
	}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanMoveFiles || items[0].CanBeRemoved {
		t.Errorf("RemoveCompletedDownloads=false: %+v", items[0])
	}
}

func TestProvider_GetItems_Cleanup_NotCompletedStatusDefaults(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["downloading"],"tags":["sonarr"]}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: true}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"h": {Ratio: ptrFloat(0.1)},
	}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanMoveFiles || items[0].CanBeRemoved {
		t.Errorf("status!=Completed: %+v", items[0])
	}
}

func TestProvider_GetItems_Cleanup_NilSeedConfigForHashDefaults(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"]}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: true}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanMoveFiles || items[0].CanBeRemoved {
		t.Errorf("nil seedConfig for hash: %+v", items[0])
	}
}

func TestProvider_GetItems_Cleanup_LookupErrorDefaults(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"]}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: true}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{err: errors.New("transient")})
	items, _ := p.GetItems(context.Background())
	if items[0].CanMoveFiles || items[0].CanBeRemoved {
		t.Errorf("transient lookup error: %+v", items[0])
	}
}

func TestProvider_GetItems_Cleanup_RatioArmFires(t *testing.T) {
	// Flood does NOT divide ratio by 1000 (unlike RQBit's C#) —
	// item.SeedRatio is taken from t.Ratio verbatim and compared
	// directly. t.Ratio=1.5 >= seedConfig.Ratio=1.0 → gate fires.
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"],"ratio":1.5}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: true}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"h": {Ratio: ptrFloat(1.0)},
	}})
	items, _ := p.GetItems(context.Background())
	if !items[0].CanMoveFiles || !items[0].CanBeRemoved {
		t.Errorf("ratio 1.5 >= 1.0 should fire: %+v", items[0])
	}
}

func TestProvider_GetItems_Cleanup_RatioArmBelowGoalDefaults(t *testing.T) {
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"],"ratio":0.4}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: true}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"h": {Ratio: ptrFloat(1.0)},
	}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanMoveFiles || items[0].CanBeRemoved {
		t.Errorf("ratio 0.4 < 1.0 should not fire: %+v", items[0])
	}
}

func TestProvider_GetItems_Cleanup_TimeArmFires(t *testing.T) {
	// DateFinished > 0 AND now - epoch(DateFinished) >= SeedTime
	// → time arm fires. Inject DateFinished=10 (10s past epoch) and
	// SeedTime=1h, default now=time.Now() (always > 10s past
	// epoch + 1h, trivially satisfies).
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"],"ratio":0.1,"dateFinished":10}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: true}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"h": {SeedTime: ptrDur(1 * time.Hour)},
	}})
	items, _ := p.GetItems(context.Background())
	if !items[0].CanMoveFiles || !items[0].CanBeRemoved {
		t.Errorf("time arm with DateFinished=10 + SeedTime=1h should fire: %+v", items[0])
	}
}

func TestProvider_GetItems_Cleanup_TimeArmSkipsWhenDateFinishedZero(t *testing.T) {
	// Flood's C# `:173` check `properties.DateFinished is > 0` skips
	// the time arm when the wire shape doesn't report a finished-at
	// timestamp. The Go port mirrors via the *t.DateFinished > 0
	// guard. No-DateFinished + ratio-only-nil → both arms fail.
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"],"ratio":0.1}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: true}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"h": {SeedTime: ptrDur(1 * time.Hour)},
	}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanMoveFiles || items[0].CanBeRemoved {
		t.Errorf("missing DateFinished should skip time arm: %+v", items[0])
	}
}

func TestProvider_GetItems_Cleanup_BothArmsNilDefaults(t *testing.T) {
	// Both seedConfig.Ratio AND .SeedTime nil → no arm fires.
	doer := newScripted()
	queueGetTorrents(doer, `{"torrents":{
		"h":{"name":"X","status":["complete"],"tags":["sonarr"],"ratio":10.0,"dateFinished":10}
	}}`)
	def := &downloadclients.DownloadClient{Name: "FL", RemoveCompletedDownloads: true}
	p := flood.New(def, newSettings(), flood.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"h": {},
	}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanMoveFiles || items[0].CanBeRemoved {
		t.Errorf("all-nil seedConfig: %+v", items[0])
	}
}

// avoid unused-import warning if ptrInt64 not used by future tests
var _ = ptrInt64

func TestProvider_GetItems_PropagatesGetTorrentsError(t *testing.T) {
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queueErr("/api/torrents", errors.New("dial"))
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, newSettings(), flood.NewProxy(doer, nil), nil)
	_, err := p.GetItems(context.Background())
	if !errors.Is(err, flood.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

type stubRemapper struct{ from, to string }

func (s stubRemapper) RemapRemoteToLocal(_ context.Context, _ string, p ospath.OsPath) (ospath.OsPath, error) {
	if p.FullPath() == s.from {
		return ospath.New(s.to), nil
	}
	return p, nil
}

func TestRegistry_RegistersFlood(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(flood.Implementation, flood.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", flood.Implementation, flood.ConfigContract)
	}
}

// --- J99 AdditionalTags composition tests ------------------------

// captureAddURLsBody pops the /api/torrents/add-urls request body and
// returns the parsed JSON map. Helper for the J99 tag-shape tests.
func captureAddURLsBody(t *testing.T, doer *scriptedDoer) map[string]any {
	t.Helper()
	for _, c := range doer.calls {
		if c.path == "/api/torrents/add-urls" {
			var body map[string]any
			if err := json.Unmarshal(c.body, &body); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			return body
		}
	}
	t.Fatal("no /api/torrents/add-urls call recorded")
	return nil
}

func tagsFromBody(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["tags"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// newSubjectWithFields builds a populated *decisionengine.Subject so
// each AdditionalTag arm can exercise its source field.
func newSubjectWithFields() *decisionengine.Subject {
	return &decisionengine.Subject{
		Series: &series.Series{
			Title:     "Big Buck Bunny",
			TitleSlug: "big-buck-bunny",
			Year:      2008,
			Network:   "C-SPAN",
		},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			ReleaseGroup: "Example-Raws",
			Quality:      quality.NewModel(quality.HDTV720p),
		},
		Languages: []language.Language{
			{ID: 1, Name: "English"},
		},
		Release: &parser.ReleaseInfo{
			Indexer:          "Torznab",
			IndexerID:        7,
			DownloadProtocol: indexers.DownloadProtocolTorrent,
			Title:            "Show.S01E02",
		},
	}
}

func runDownloadWithSubject(t *testing.T, settings flood.Settings, subject *decisionengine.Subject) map[string]any {
	t.Helper()
	doer := newScripted()
	queueAuthOnce(doer)
	doer.queue("/api/torrents/add-urls")
	p := flood.New(&downloadclients.DownloadClient{Name: "FL"}, settings, flood.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:cafebabe",
		InfoHash:     "cafebabe",
		Subject:      subject,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	return captureAddURLsBody(t, doer)
}

func TestComposeTags_NoSubjectFallsBackToSettingsTagsOnly(t *testing.T) {
	settings := newSettings()
	settings.Tags = []string{"sonarr"}
	settings.AdditionalTags = []int{flood.AdditionalTagTitleSlug, flood.AdditionalTagYear}
	body := runDownloadWithSubject(t, settings, nil)
	got := tagsFromBody(t, body)
	if len(got) != 1 || got[0] != "sonarr" {
		t.Errorf("tags=%v, want [sonarr] only (no subject)", got)
	}
}

func TestComposeTags_NoAdditionalTagsIDsEmitsSettingsTagsOnly(t *testing.T) {
	settings := newSettings()
	settings.Tags = []string{"sonarr", "tv"}
	settings.AdditionalTags = nil
	body := runDownloadWithSubject(t, settings, newSubjectWithFields())
	got := tagsFromBody(t, body)
	if len(got) != 2 || got[0] != "sonarr" || got[1] != "tv" {
		t.Errorf("tags=%v, want [sonarr, tv]", got)
	}
}

func TestComposeTags_TitleSlugArm(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{flood.AdditionalTagTitleSlug}
	body := runDownloadWithSubject(t, settings, newSubjectWithFields())
	got := tagsFromBody(t, body)
	if len(got) != 1 || got[0] != "big-buck-bunny" {
		t.Errorf("tags=%v, want [big-buck-bunny]", got)
	}
}

func TestComposeTags_QualityArm(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{flood.AdditionalTagQuality}
	body := runDownloadWithSubject(t, settings, newSubjectWithFields())
	got := tagsFromBody(t, body)
	if len(got) != 1 || got[0] != quality.HDTV720p.Name {
		t.Errorf("tags=%v, want [%s]", got, quality.HDTV720p.Name)
	}
}

func TestComposeTags_LanguagesArm(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{flood.AdditionalTagLanguages}
	subject := newSubjectWithFields()
	subject.Languages = []language.Language{
		{ID: 1, Name: "English"},
		{ID: 2, Name: "French"},
	}
	body := runDownloadWithSubject(t, settings, subject)
	got := tagsFromBody(t, body)
	if len(got) != 2 || got[0] != "English" || got[1] != "French" {
		t.Errorf("tags=%v, want [English, French]", got)
	}
}

func TestComposeTags_ReleaseGroupArm(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{flood.AdditionalTagReleaseGroup}
	body := runDownloadWithSubject(t, settings, newSubjectWithFields())
	got := tagsFromBody(t, body)
	if len(got) != 1 || got[0] != "Example-Raws" {
		t.Errorf("tags=%v, want [Example-Raws]", got)
	}
}

func TestComposeTags_YearArm(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{flood.AdditionalTagYear}
	body := runDownloadWithSubject(t, settings, newSubjectWithFields())
	got := tagsFromBody(t, body)
	if len(got) != 1 || got[0] != "2008" {
		t.Errorf("tags=%v, want [2008]", got)
	}
}

func TestComposeTags_YearZeroSkipped(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{flood.AdditionalTagYear}
	subject := newSubjectWithFields()
	subject.Series.Year = 0
	body := runDownloadWithSubject(t, settings, subject)
	got := tagsFromBody(t, body)
	if len(got) != 0 {
		t.Errorf("tags=%v, want [] (Year=0 skipped)", got)
	}
}

func TestComposeTags_IndexerArm(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{flood.AdditionalTagIndexer}
	body := runDownloadWithSubject(t, settings, newSubjectWithFields())
	got := tagsFromBody(t, body)
	if len(got) != 1 || got[0] != "Torznab" {
		t.Errorf("tags=%v, want [Torznab]", got)
	}
}

func TestComposeTags_NetworkArm(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{flood.AdditionalTagNetwork}
	body := runDownloadWithSubject(t, settings, newSubjectWithFields())
	got := tagsFromBody(t, body)
	if len(got) != 1 || got[0] != "C-SPAN" {
		t.Errorf("tags=%v, want [C-SPAN]", got)
	}
}

func TestComposeTags_UnionAndDedup(t *testing.T) {
	// Settings.Tags + AdditionalTags compose; duplicates between the
	// two halves collapse (Settings.Tags wins position order).
	settings := newSettings()
	settings.Tags = []string{"sonarr", "Torznab"} // overlap with Indexer arm
	settings.AdditionalTags = []int{
		flood.AdditionalTagTitleSlug, // big-buck-bunny
		flood.AdditionalTagIndexer,   // Torznab (dupe; skipped)
		flood.AdditionalTagYear,      // 2008
	}
	body := runDownloadWithSubject(t, settings, newSubjectWithFields())
	got := tagsFromBody(t, body)
	want := []string{"sonarr", "Torznab", "big-buck-bunny", "2008"}
	if len(got) != len(want) {
		t.Fatalf("tags=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tags[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestComposeTags_NilSeriesSkipsSeriesArms(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{
		flood.AdditionalTagTitleSlug,
		flood.AdditionalTagYear,
		flood.AdditionalTagNetwork,
		flood.AdditionalTagReleaseGroup,
	}
	subject := newSubjectWithFields()
	subject.Series = nil
	body := runDownloadWithSubject(t, settings, subject)
	got := tagsFromBody(t, body)
	// Only ReleaseGroup (PEI-sourced) survives.
	if len(got) != 1 || got[0] != "Example-Raws" {
		t.Errorf("tags=%v, want [Example-Raws] only", got)
	}
}

func TestComposeTags_UnknownIDIsSkipped(t *testing.T) {
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{999, flood.AdditionalTagTitleSlug}
	body := runDownloadWithSubject(t, settings, newSubjectWithFields())
	got := tagsFromBody(t, body)
	if len(got) != 1 || got[0] != "big-buck-bunny" {
		t.Errorf("tags=%v, want [big-buck-bunny] (unknown ID skipped)", got)
	}
}

func TestComposeTags_TorrentInfoReleaseShape(t *testing.T) {
	// Subject.Release can be either *parser.ReleaseInfo or
	// *parser.TorrentInfo. The Indexer-arm reads via subjectReleaseInfo
	// which narrows both shapes.
	settings := newSettings()
	settings.Tags = nil
	settings.AdditionalTags = []int{flood.AdditionalTagIndexer}
	subject := newSubjectWithFields()
	subject.Release = &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Indexer:          "BroadcastTheNet",
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
	}
	body := runDownloadWithSubject(t, settings, subject)
	got := tagsFromBody(t, body)
	if len(got) != 1 || got[0] != "BroadcastTheNet" {
		t.Errorf("tags=%v, want [BroadcastTheNet] (TorrentInfo release)", got)
	}
}
