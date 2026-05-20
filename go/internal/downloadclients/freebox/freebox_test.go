package freebox_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // Freebox auth protocol mandates HMAC-SHA1 (matches the wire spec; not a security primitive choice in test).
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/freebox"
	"sonarr.tv/sonarr/internal/indexers"
)

// scriptedDoer queues responses per-path so the multi-step
// challenge → session-token flow can be staged.
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
	method  string
	path    string
	body    []byte
	headers http.Header
}

func newScripted() *scriptedDoer { return &scriptedDoer{responses: map[string][]scriptedResp{}} }

func (s *scriptedDoer) queue(path, body string) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: http.StatusOK, body: body})
}

func (s *scriptedDoer) queueStatus(path string, status int, body string) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: status, body: body})
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
		headers: req.Header.Clone(),
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

func newSettings() freebox.Settings {
	s := freebox.NewSettings()
	s.AppId = "sonarr"
	s.AppToken = "secret-app-token"
	return s
}

func TestSettings_Defaults(t *testing.T) {
	s := freebox.NewSettings()
	if s.Host != "mafreebox.freebox.fr" || s.Port != 443 || !s.UseSsl || s.ApiUrl != "/api/v1/" {
		t.Errorf("NewSettings = %+v, want defaults", s)
	}
}

func TestSettings_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*freebox.Settings)
		wantBad string
	}{
		{"happy path", func(*freebox.Settings) {}, ""},
		{"empty host", func(s *freebox.Settings) { s.Host = "" }, "Host"},
		{"port too low", func(s *freebox.Settings) { s.Port = 0 }, "Port"},
		{"port too high", func(s *freebox.Settings) { s.Port = 70000 }, "Port"},
		{"empty ApiUrl", func(s *freebox.Settings) { s.ApiUrl = "" }, "ApiUrl"},
		{"ApiUrl missing leading slash", func(s *freebox.Settings) { s.ApiUrl = "api/v1/" }, "ApiUrl"},
		{"empty AppId", func(s *freebox.Settings) { s.AppId = "" }, "AppId"},
		{"empty AppToken", func(s *freebox.Settings) { s.AppToken = "" }, "AppToken"},
		{"Category illegal chars", func(s *freebox.Settings) { s.Category = "bad/value" }, "Category"},
		{"Category leading dot ok", func(s *freebox.Settings) { s.Category = ".sonarr-tv" }, ""},
		{"Category + Destination mutual exclusion", func(s *freebox.Settings) {
			s.Category = "sonarr"
			s.DestinationDirectory = "/dl"
		}, "Category"},
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
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, newSettings(), nil, nil)
	if p.Name() != "Freebox Download" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol = %v, want Torrent", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "FB" {
		t.Errorf("Definition not bound")
	}
}

func TestProxy_GetSessionToken_HmacShape(t *testing.T) {
	// Drive the full GET /login → POST /login/session flow and
	// verify the password posted is lowercase-hex(HMAC-SHA1(
	// challenge, AppToken)). Locks the auth invariant from C#
	// FreeboxDownloadProxy.cs:180-184.
	doer := newScripted()
	doer.queue("/api/v1/login", `{"success":true,"result":{"logged_in":false,"challenge":"CH-1"}}`)
	doer.queue("/api/v1/login/session", `{"success":true,"result":{"session_token":"SESS-A"}}`)

	proxy := freebox.NewProxy(doer, nil)
	tok, err := proxy.GetSessionToken(context.Background(), newSettings(), false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "SESS-A" {
		t.Errorf("token = %q, want SESS-A", tok)
	}

	// Inspect the /login/session POST body — verify it carries the
	// expected HMAC-SHA1 password.
	var sessionCall *scriptedCall
	for i := range doer.calls {
		if doer.calls[i].path == "/api/v1/login/session" {
			sessionCall = &doer.calls[i]
		}
	}
	if sessionCall == nil {
		t.Fatal("no /api/v1/login/session call recorded")
	}
	if sessionCall.method != http.MethodPost {
		t.Errorf("method = %q, want POST", sessionCall.method)
	}
	var body map[string]string
	_ = json.Unmarshal(sessionCall.body, &body)
	if body["app_id"] != "sonarr" {
		t.Errorf("app_id = %q, want sonarr", body["app_id"])
	}
	// Recompute the expected hash for the challenge.
	mac := hmac.New(sha1.New, []byte("secret-app-token"))
	mac.Write([]byte("CH-1"))
	want := strings.ToLower(hex.EncodeToString(mac.Sum(nil)))
	if body["password"] != want {
		t.Errorf("password = %q, want %q (HMAC-SHA1)", body["password"], want)
	}
}

func TestProxy_GetSessionToken_CachesResult(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/v1/login", `{"success":true,"result":{"challenge":"CH-1"}}`)
	doer.queue("/api/v1/login/session", `{"success":true,"result":{"session_token":"SESS-A"}}`)
	// No second login/session response queued; cache must hit.
	proxy := freebox.NewProxy(doer, nil)
	if _, err := proxy.GetSessionToken(context.Background(), newSettings(), false); err != nil {
		t.Fatalf("first err = %v", err)
	}
	if _, err := proxy.GetSessionToken(context.Background(), newSettings(), false); err != nil {
		t.Fatalf("second err = %v (cache should have served)", err)
	}
	loginCount := 0
	for _, c := range doer.calls {
		if c.path == "/api/v1/login/session" {
			loginCount++
		}
	}
	if loginCount != 1 {
		t.Errorf("session-exchange call count = %d, want 1", loginCount)
	}
}

func TestProxy_GetSessionToken_ForceBypassesCache(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/v1/login", `{"success":true,"result":{"challenge":"CH-1"}}`)
	doer.queue("/api/v1/login/session", `{"success":true,"result":{"session_token":"A"}}`)
	doer.queue("/api/v1/login", `{"success":true,"result":{"challenge":"CH-2"}}`)
	doer.queue("/api/v1/login/session", `{"success":true,"result":{"session_token":"B"}}`)
	proxy := freebox.NewProxy(doer, nil)
	first, _ := proxy.GetSessionToken(context.Background(), newSettings(), false)
	second, _ := proxy.GetSessionToken(context.Background(), newSettings(), true)
	if first == second {
		t.Errorf("force=true did not bypass cache (both = %q)", first)
	}
}

func TestProxy_GetSessionToken_AuthFailure(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/v1/login", `{"success":true,"result":{"challenge":"CH-1"}}`)
	doer.queueStatus("/api/v1/login/session", http.StatusForbidden, `{"success":false,"error_code":"invalid_token"}`)
	proxy := freebox.NewProxy(doer, nil)
	_, err := proxy.GetSessionToken(context.Background(), newSettings(), false)
	if !errors.Is(err, freebox.ErrAuth) {
		t.Errorf("err = %v, want ErrAuth", err)
	}
}

func TestProxy_GetSessionToken_NotFoundSurfacesAPIError(t *testing.T) {
	doer := newScripted()
	doer.queueStatus("/api/v1/login", http.StatusNotFound, ``)
	proxy := freebox.NewProxy(doer, nil)
	_, err := proxy.GetSessionToken(context.Background(), newSettings(), false)
	if !errors.Is(err, freebox.ErrAPI) {
		t.Errorf("err = %v, want ErrAPI on 404", err)
	}
}

func TestProxy_GetSessionToken_RPCErrorSurfaces(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/v1/login", `{"success":false,"error_code":"invalid_api_version"}`)
	proxy := freebox.NewProxy(doer, nil)
	_, err := proxy.GetSessionToken(context.Background(), newSettings(), false)
	if !errors.Is(err, freebox.ErrRPC) {
		t.Errorf("err = %v, want ErrRPC", err)
	}
	if !strings.Contains(err.Error(), "Invalid API base url") {
		t.Errorf("err = %q, want it to surface the decoded error description", err)
	}
}

func TestProxy_GetSessionToken_TransportErrorPropagates(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/api/v1/login", errors.New("dial"))
	proxy := freebox.NewProxy(doer, nil)
	_, err := proxy.GetSessionToken(context.Background(), newSettings(), false)
	if !errors.Is(err, freebox.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProvider_Test_HappyPath(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/v1/login", `{"success":true,"result":{"challenge":"CH"}}`)
	doer.queue("/api/v1/login/session", `{"success":true,"result":{"session_token":"S"}}`)
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, newSettings(), freebox.NewProxy(doer, nil), nil)
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
	s.AppToken = ""
	doer := newScripted()
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, s, freebox.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) == 0 {
		t.Fatal("failures empty, want AppToken failure")
	}
	if len(doer.calls) != 0 {
		t.Errorf("proxy called despite Settings.Validate failure: %d calls", len(doer.calls))
	}
}

func TestProvider_Test_AuthFailureSurfacesAppToken(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/v1/login", `{"success":true,"result":{"challenge":"CH"}}`)
	doer.queueStatus("/api/v1/login/session", http.StatusForbidden, `{"success":false,"error_code":"invalid_token"}`)
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, newSettings(), freebox.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || failures[0].PropertyName != "AppToken" {
		t.Errorf("failures = %+v, want AppToken failure", failures)
	}
}

func TestProvider_Test_APIErrorSurfacesApiUrl(t *testing.T) {
	doer := newScripted()
	doer.queueStatus("/api/v1/login", http.StatusNotFound, ``)
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, newSettings(), freebox.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || failures[0].PropertyName != "ApiUrl" {
		t.Errorf("failures = %+v, want ApiUrl failure", failures)
	}
}

func TestProvider_Test_TransportErrorSurfacesHost(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/api/v1/login", errors.New("dial"))
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, newSettings(), freebox.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || failures[0].PropertyName != "Host" {
		t.Errorf("failures = %+v, want Host failure", failures)
	}
}

func TestProvider_GetStatus_LocalhostFlag(t *testing.T) {
	cases := map[string]bool{
		"localhost":            true,
		"127.0.0.1":            true,
		"::1":                  true,
		"mafreebox.freebox.fr": false,
	}
	for host, want := range cases {
		t.Run(host, func(t *testing.T) {
			s := newSettings()
			s.Host = host
			p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, s, freebox.NewProxy(newScripted(), nil), nil)
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
	// J85 wires Download. GetItems + RemoveItem remain stubs.
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, newSettings(), freebox.NewProxy(newScripted(), nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, true); !errors.Is(err, freebox.ErrNotImplemented) {
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
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, newSettings(), freebox.NewProxy(newScripted(), nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

// queueAuthFlow queues the standard nonce + login responses so a
// follow-on authenticated call can find a cookie / session-token.
func queueAuthFlow(doer *scriptedDoer) {
	doer.queue("/api/v1/login", `{"success":true,"result":{"challenge":"CH-1"}}`)
	doer.queue("/api/v1/login/session", `{"success":true,"result":{"session_token":"SESS-1"}}`)
}

// captureDoer wraps scriptedDoer and grabs the body + Content-Type
// of the last add-task POST so the wire shape can be asserted.
type captureDoer struct {
	scripted     *scriptedDoer
	lastBody     []byte
	lastCT       string
	lastURL      string
	lastMethod   string
	lastSessTok  string
	settingsBody []byte
	settingsURL  string
}

func (c *captureDoer) Do(req *http.Request) (*http.Response, error) {
	body, _ := readReqBody(req)
	if req.URL.Path == "/api/v1/downloads/add" {
		c.lastBody = body
		c.lastCT = req.Header.Get("Content-Type")
		c.lastURL = req.URL.String()
		c.lastMethod = req.Method
		c.lastSessTok = req.Header.Get("X-Fbx-App-Auth")
	}
	if strings.HasPrefix(req.URL.Path, "/api/v1/downloads/") && req.Method == http.MethodPut {
		c.settingsBody = body
		c.settingsURL = req.URL.Path
	}
	// Replace body so the underlying scripted doer can read it again.
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	return c.scripted.Do(req)
}

func readReqBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	return io.ReadAll(req.Body)
}

func TestProvider_Download_TorrentBody_HappyPath_MultipartShape(t *testing.T) {
	doer := &captureDoer{scripted: newScripted()}
	queueAuthFlow(doer.scripted)
	doer.scripted.queue("/api/v1/downloads/add", `{"success":true,"result":{"id":"new-task-42"}}`)
	// The SetTorrentSettings PUT is a no-op when AddPaused=false +
	// no addFirst + no seed-ratio; for this test we make sure the
	// add returns the ID even when the PUT is skipped.

	settings := newSettings()
	settings.DestinationDirectory = "/freebox/tv"
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, settings, freebox.NewProxy(doer, nil), nil)

	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		TorrentBody:  []byte("RAW-TORRENT-BYTES"),
	})
	if err != nil {
		t.Fatalf("Download err = %v", err)
	}
	if id != "new-task-42" {
		t.Errorf("id = %q, want new-task-42", id)
	}
	if !strings.HasPrefix(doer.lastCT, "multipart/form-data;") {
		t.Errorf("Content-Type = %q, want multipart/form-data;", doer.lastCT)
	}
	body := string(doer.lastBody)
	if !strings.Contains(body, `name="download_file"`) {
		t.Errorf("body missing download_file form field: %q", body)
	}
	if !strings.Contains(body, `filename="Show.S01E01.torrent"`) {
		t.Errorf("body missing filename: %q", body)
	}
	if !strings.Contains(body, "RAW-TORRENT-BYTES") {
		t.Errorf("body missing torrent payload")
	}
	if !strings.Contains(body, `name="download_dir"`) {
		t.Errorf("body missing download_dir form-part")
	}
	wantDir := base64.StdEncoding.EncodeToString([]byte("/freebox/tv"))
	if !strings.Contains(body, wantDir) {
		t.Errorf("body missing base64(download_dir) = %q", wantDir)
	}
}

func TestProvider_Download_MagnetArm_FormShape(t *testing.T) {
	doer := &captureDoer{scripted: newScripted()}
	queueAuthFlow(doer.scripted)
	doer.scripted.queue("/api/v1/downloads/add", `{"success":true,"result":{"id":"mag-task-1"}}`)

	settings := newSettings()
	settings.DestinationDirectory = "/dl"
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, settings, freebox.NewProxy(doer, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "mag-task-1" {
		t.Errorf("id = %q", id)
	}
	if doer.lastCT != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", doer.lastCT)
	}
	form, _ := url.ParseQuery(string(doer.lastBody))
	if form.Get("download_url") != "magnet:?xt=urn:btih:abc" {
		t.Errorf("download_url = %q, want the magnet URL", form.Get("download_url"))
	}
	if form.Get("download_dir") != base64.StdEncoding.EncodeToString([]byte("/dl")) {
		t.Errorf("download_dir = %q, want base64(/dl)", form.Get("download_dir"))
	}
}

func TestProvider_Download_DirectoryFromConfigPlusCategory(t *testing.T) {
	// No DestinationDirectory set, TvCategory=sonarr → GetConfig
	// is called for the base, then base+category becomes the dir.
	doer := &captureDoer{scripted: newScripted()}
	queueAuthFlow(doer.scripted)
	doer.scripted.queue("/api/v1/downloads/config/", `{"success":true,"result":{"download_dir":"`+base64.StdEncoding.EncodeToString([]byte("/freebox/dl"))+`"}}`)
	doer.scripted.queue("/api/v1/downloads/add", `{"success":true,"result":{"id":"task"}}`)

	settings := newSettings()
	settings.Category = "sonarr"
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, settings, freebox.NewProxy(doer, nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{MagnetURL: "magnet:?abc"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	form, _ := url.ParseQuery(string(doer.lastBody))
	wantDir := base64.StdEncoding.EncodeToString([]byte("/freebox/dl/sonarr"))
	if form.Get("download_dir") != wantDir {
		t.Errorf("download_dir = %q, want base64(/freebox/dl/sonarr)", form.Get("download_dir"))
	}
}

func TestProvider_Download_AddPausedTriggersSetTorrentSettings(t *testing.T) {
	doer := &captureDoer{scripted: newScripted()}
	queueAuthFlow(doer.scripted)
	doer.scripted.queue("/api/v1/downloads/add", `{"success":true,"result":{"id":"task-99"}}`)
	doer.scripted.queue("/api/v1/downloads/task-99", `{"success":true,"result":{}}`)

	settings := newSettings()
	settings.DestinationDirectory = "/dl"
	settings.AddPaused = true
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, settings, freebox.NewProxy(doer, nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{MagnetURL: "magnet:?abc"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if doer.settingsURL != "/api/v1/downloads/task-99" {
		t.Errorf("settings PUT URL = %q, want /api/v1/downloads/task-99", doer.settingsURL)
	}
	if !strings.Contains(string(doer.settingsBody), `"status":"stopped"`) {
		t.Errorf("settings body = %s, want status:stopped", doer.settingsBody)
	}
}

func TestProvider_Download_QueueFirstPriority(t *testing.T) {
	doer := &captureDoer{scripted: newScripted()}
	queueAuthFlow(doer.scripted)
	doer.scripted.queue("/api/v1/downloads/add", `{"success":true,"result":{"id":"task-7"}}`)
	doer.scripted.queue("/api/v1/downloads/task-7", `{"success":true,"result":{}}`)

	settings := newSettings()
	settings.DestinationDirectory = "/dl"
	settings.RecentPriority = freebox.PriorityFirst
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, settings, freebox.NewProxy(doer, nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?abc",
		IsRecent:  true,
	}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(string(doer.settingsBody), `"queue_pos":"1"`) {
		t.Errorf("settings body = %s, want queue_pos:1", doer.settingsBody)
	}
}

func TestProvider_Download_SeedRatioMultipliedBy100(t *testing.T) {
	doer := &captureDoer{scripted: newScripted()}
	queueAuthFlow(doer.scripted)
	doer.scripted.queue("/api/v1/downloads/add", `{"success":true,"result":{"id":"task-8"}}`)
	doer.scripted.queue("/api/v1/downloads/task-8", `{"success":true,"result":{}}`)

	settings := newSettings()
	settings.DestinationDirectory = "/dl"
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, settings, freebox.NewProxy(doer, nil), nil)
	ratio := 2.5
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL:         "magnet:?abc",
		SeedConfiguration: &downloadclients.TorrentSeedConfiguration{Ratio: &ratio},
	}); err != nil {
		t.Fatalf("err = %v", err)
	}
	// 2.5 * 100 = 250 — verify it's in the body.
	if !strings.Contains(string(doer.settingsBody), `"stop_ratio":250`) {
		t.Errorf("settings body = %s, want stop_ratio:250 (2.5 * 100)", doer.settingsBody)
	}
}

func TestProvider_Download_NeitherBodyNorMagnet(t *testing.T) {
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, newSettings(), freebox.NewProxy(newScripted(), nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Fatal("err = nil, want neither-body-nor-magnet error")
	}
}

func TestProvider_Download_EmptyTitleOnTorrentBody(t *testing.T) {
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, newSettings(), freebox.NewProxy(newScripted(), nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{TorrentBody: []byte("x")})
	if err == nil {
		t.Fatal("err = nil, want empty-title error for torrent-body arm")
	}
}

func TestProvider_Download_EmptyIDSurfacesRPC(t *testing.T) {
	doer := newScripted()
	queueAuthFlow(doer)
	doer.queue("/api/v1/downloads/add", `{"success":true,"result":{"id":""}}`)
	settings := newSettings()
	settings.DestinationDirectory = "/dl"
	p := freebox.New(&downloadclients.DownloadClient{Name: "FB"}, settings, freebox.NewProxy(doer, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{MagnetURL: "magnet:?abc"})
	if !errors.Is(err, freebox.ErrRPC) {
		t.Errorf("err = %v, want ErrRPC", err)
	}
}

func TestRegistry_RegistersFreebox(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(freebox.Implementation, freebox.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", freebox.Implementation, freebox.ConfigContract)
	}
}
