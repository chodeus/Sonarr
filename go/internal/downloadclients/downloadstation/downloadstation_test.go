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
	"sonarr.tv/sonarr/internal/ospath"
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
// call can find them. The V2 (DownloadStation2) task API is
// intentionally absent — taskAPIVersion falls back to V1.
func queueAPIDiscovery(doer *scriptedDoer, authMax int) {
	body := `{"success":true,"data":{
		"SYNO.API.Auth":{"path":"auth.cgi","maxVersion":` +
		intToStr(authMax) + `,"minVersion":1},
		"SYNO.DownloadStation.Task":{"path":"DownloadStation/task.cgi","maxVersion":3,"minVersion":1}
	}}`
	doer.queue("/webapi/query.cgi", body)
}

// queueAPIDiscoveryV2 queues a SYNO.API.Info response that
// advertises BOTH task APIs — DSM 7+ shape. taskAPIVersion picks
// V2 in this scenario.
func queueAPIDiscoveryV2(doer *scriptedDoer) {
	body := `{"success":true,"data":{
		"SYNO.API.Auth":{"path":"auth.cgi","maxVersion":7,"minVersion":1},
		"SYNO.DownloadStation.Task":{"path":"DownloadStation/task.cgi","maxVersion":3,"minVersion":1},
		"SYNO.DownloadStation2.Task":{"path":"DownloadStation2/task.cgi","maxVersion":2,"minVersion":1}
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
	// Test now drives ValidateVersion (which re-resolves API info
	// — cache hit, no extra round-trip) + TestGetTorrents (which
	// fires the task-list call).
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[]}}`)
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

func TestProvider_Download_TorrentBody_HappyPath_MultipartShape(t *testing.T) {
	// J78 wires the torrent-file arm through Proxy.AddTaskFromData
	// (multipart POST). The capture doer grabs the create-POST
	// body so the multipart envelope can be asserted.
	doer := newMultipartCapture()
	queueAPIDiscovery(doer.scripted, 3)
	doer.scripted.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID"}}`)
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{}}`)
	// Post-add list returns the new BT task; lookup matches the
	// extension-less ReleaseTitle per C# Path.GetFileNameWithoutExtension.
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"bt_42","title":"Show.S01E01","type":"BT","status":"downloading","additional":{"detail":{"uri":"Show.S01E01"}}}
	]}}`)

	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		TorrentBody:  []byte("RAW-TORRENT-PAYLOAD"),
		InfoHash:     "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "bt_42" {
		t.Errorf("id = %q, want bt_42", id)
	}
	if !strings.HasPrefix(doer.lastCreateContentType, "multipart/form-data;") {
		t.Errorf("create Content-Type = %q, want multipart/form-data;...", doer.lastCreateContentType)
	}
	body := string(doer.lastCreateBody)
	if !strings.Contains(body, "RAW-TORRENT-PAYLOAD") {
		t.Errorf("multipart body missing torrent payload bytes")
	}
	if !strings.Contains(body, `filename="Show.S01E01.torrent"`) {
		t.Errorf("multipart body missing filename=%q", "Show.S01E01.torrent")
	}
	if !strings.Contains(body, `name="_sid"`) {
		t.Errorf("multipart body missing _sid form-field")
	}
}

func TestProvider_Download_TorrentBody_EmptyTitleErrors(t *testing.T) {
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(newScripted(), nil, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{TorrentBody: []byte("x")})
	if err == nil {
		t.Fatal("err = nil, want empty-title error")
	}
}

func TestProvider_Download_TorrentBody_NoMatchSurfacesError(t *testing.T) {
	doer := newMultipartCapture()
	queueAPIDiscovery(doer.scripted, 3)
	doer.scripted.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID"}}`)
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{}}`)
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[]}}`)

	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		TorrentBody:  []byte("x"),
	})
	if err == nil {
		t.Fatal("err = nil, want no-matching-task error")
	}
}

func TestProvider_Download_TorrentBody_DuplicateMatchErrors(t *testing.T) {
	doer := newMultipartCapture()
	queueAPIDiscovery(doer.scripted, 3)
	doer.scripted.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID"}}`)
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{}}`)
	// Two BT tasks both matching the URI — mirrors C# SingleOrDefault
	// strictness.
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"a","title":"X","type":"BT","status":"downloading","additional":{"detail":{"uri":"X"}}},
		{"id":"b","title":"X","type":"BT","status":"downloading","additional":{"detail":{"uri":"X"}}}
	]}}`)
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		TorrentBody:  []byte("x"),
	})
	if err == nil {
		t.Fatal("err = nil, want duplicate-match error")
	}
}

// queueAuthAndDiscovery stages the prerequisite calls for any
// auth-required Provider call: API discovery + login.
func queueAuthAndDiscovery(doer *scriptedDoer) {
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID"}}`)
}

func TestProvider_Download_MagnetHappyPath(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	// create call returns success.
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{}}`)
	// post-add list lookup returns the new task.
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"dbid_42","title":"Show.S01E01","size":100,"type":"BT","status":"downloading","additional":{"detail":{"uri":"magnet:?xt=urn:btih:dead"}}}
	]}}`)

	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		MagnetURL:    "magnet:?xt=urn:btih:dead",
		InfoHash:     "dead",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "dbid_42" {
		t.Errorf("id = %q, want dbid_42", id)
	}
}

func TestProvider_Download_MagnetNoMatchSurfacesError(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{}}`)
	// post-add list returns an unrelated task.
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"x","title":"Other","type":"BT","status":"downloading","additional":{"detail":{"uri":"magnet:?xt=urn:btih:other"}}}
	]}}`)
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{MagnetURL: "magnet:?xt=urn:btih:dead"})
	if err == nil {
		t.Fatal("err = nil, want no-matching-task error")
	}
}

func TestProvider_Download_NeitherBodyNorMagnet(t *testing.T) {
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(newScripted(), nil, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Fatal("err = nil, want neither-body-nor-magnet error")
	}
}

func TestProvider_GetItems_BasicStatusTable(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"a","title":"A","type":"BT","status":"downloading","size":100,"additional":{"detail":{"destination":"downloads"},"transfer":{"size_downloaded":"50","size_uploaded":"10","speed_download":"10"}}},
		{"id":"b","title":"B","type":"BT","status":"paused","size":100,"additional":{"detail":{"destination":"downloads"},"transfer":{"size_downloaded":"50","size_uploaded":"0","speed_download":"0"}}},
		{"id":"c","title":"C","type":"BT","status":"finished","size":100,"additional":{"detail":{"destination":"downloads"},"transfer":{"size_downloaded":"100","size_uploaded":"200","speed_download":"0"}}},
		{"id":"d","title":"D","type":"BT","status":"error","size":100,"status_extra":{"error_detail":"corrupt"},"additional":{"detail":{"destination":"downloads"},"transfer":{}}},
		{"id":"e","title":"E","type":"BT","status":"waiting","size":100,"additional":{"detail":{"destination":"downloads"},"transfer":{"size_downloaded":"100","size_uploaded":"0","speed_download":"0"}}},
		{"id":"skip","title":"NotBT","type":"http","status":"downloading","additional":{"detail":{"destination":"downloads"},"transfer":{}}}
	]}}`)
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("len = %d, want 5 (skip non-BT)", len(items))
	}
	byID := map[string]*downloadclients.Item{}
	for _, it := range items {
		byID[it.DownloadID] = it
	}
	want := map[string]downloadclients.DownloadItemStatus{
		"a": downloadclients.DownloadItemStatusDownloading,
		"b": downloadclients.DownloadItemStatusPaused,
		"c": downloadclients.DownloadItemStatusCompleted,
		"d": downloadclients.DownloadItemStatusFailed,
		"e": downloadclients.DownloadItemStatusCompleted, // waiting + size==100 + remaining==0 → Completed
	}
	for id, st := range want {
		got, ok := byID[id]
		if !ok {
			t.Errorf("missing %s", id)
			continue
		}
		if got.Status != st {
			t.Errorf("%s Status = %v, want %v", id, got.Status, st)
		}
	}
	if byID["d"].Message != "corrupt" {
		t.Errorf("error-arm Message = %q, want 'corrupt'", byID["d"].Message)
	}
	// Verify ETA + ratio on 'a'.
	if byID["a"].RemainingTime == nil || *byID["a"].RemainingTime != 5*time.Second {
		t.Errorf("a.RemainingTime = %v, want 5s (remaining=50, speed=10)", byID["a"].RemainingTime)
	}
	if byID["a"].SeedRatio == nil || *byID["a"].SeedRatio != 0.2 {
		t.Errorf("a.SeedRatio = %v, want 0.2 (uploaded=10/downloaded=50)", byID["a"].SeedRatio)
	}
	// Verify completed 'c' has OutputPath = destination + title.
	if byID["c"].OutputPath.FullPath() != "/downloads/C" {
		t.Errorf("c.OutputPath = %q, want /downloads/C", byID["c"].OutputPath.FullPath())
	}
}

func TestProvider_GetItems_TvDirectoryGate(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"a","title":"InDir","type":"BT","status":"downloading","additional":{"detail":{"destination":"downloads/tv/show"},"transfer":{}}},
		{"id":"b","title":"OutOfDir","type":"BT","status":"downloading","additional":{"detail":{"destination":"downloads/movies/x"},"transfer":{}}}
	]}}`)
	s := newSettings()
	s.TvDirectory = "downloads/tv"
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, downloadstation.NewProxy(doer, nil, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 || items[0].DownloadID != "a" {
		t.Errorf("items = %+v, want only a (TvDirectory gate filters b)", items)
	}
}

func TestProvider_GetItems_TvCategoryGate(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"a","title":"InCat","type":"BT","status":"downloading","additional":{"detail":{"destination":"downloads/sonarr/show"},"transfer":{}}},
		{"id":"b","title":"OutOfCat","type":"BT","status":"downloading","additional":{"detail":{"destination":"downloads/radarr/x"},"transfer":{}}}
	]}}`)
	s := newSettings()
	s.TvCategory = "sonarr"
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, downloadstation.NewProxy(doer, nil, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 || items[0].DownloadID != "a" {
		t.Errorf("items = %+v, want only a (TvCategory gate filters b)", items)
	}
}

func TestProvider_GetItems_RemoveCompletedFlagsFinished(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"a","title":"A","type":"BT","status":"finished","size":100,"additional":{"detail":{"destination":"downloads"},"transfer":{"size_downloaded":"100"}}}
	]}}`)
	def := &downloadclients.DownloadClient{Name: "DS", RemoveCompletedDownloads: true}
	p := downloadstation.New(def, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 || !items[0].CanBeRemoved || !items[0].CanMoveFiles {
		t.Errorf("items = %+v, want both cleanup flags true on finished+RCD", items)
	}
}

func TestProvider_RemoveItem_HappyPath(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{}}`)
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "dbid_42"}, false); err != nil {
		t.Fatalf("err = %v", err)
	}
	// Verify the delete query included id + force_complete.
	for _, c := range doer.calls {
		if c.path == "/webapi/DownloadStation/task.cgi" && c.query.Get("method") == "delete" {
			if c.query.Get("id") != "dbid_42" {
				t.Errorf("id = %q, want dbid_42", c.query.Get("id"))
			}
			if c.query.Get("force_complete") != "false" {
				t.Errorf("force_complete = %q, want false", c.query.Get("force_complete"))
			}
		}
	}
}

func TestProvider_RemoveItem_NilItem(t *testing.T) {
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(newScripted(), nil, nil), nil)
	if err := p.RemoveItem(context.Background(), nil, false); err == nil {
		t.Fatal("err = nil, want nil-item error")
	}
}

func TestProvider_RemoveItem_EmptyID(t *testing.T) {
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(newScripted(), nil, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, false); err == nil {
		t.Fatal("err = nil, want empty-ID error")
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

// ---- DSM 7+ V1/V2 selector (5.J90) -------------------------------

func TestProxy_TaskAPIVersion_V1Default(t *testing.T) {
	// Discovery advertises only V1 → selector picks V1.
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[]}}`)
	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	if _, err := p.GetItems(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, c := range doer.calls {
		if c.path == "/webapi/DownloadStation2/task.cgi" {
			t.Errorf("V2 path called despite discovery only advertising V1: %v", c)
		}
	}
}

func TestProxy_TaskAPIVersion_V2WhenAdvertised_GetItemsTwoCallMerge(t *testing.T) {
	// DSM 7+ shape: V2 advertised → GetItems makes TWO V2 list
	// calls (detail + transfer) and merges by ID.
	doer := newScripted()
	queueAPIDiscoveryV2(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	// detail call:
	doer.queue("/webapi/DownloadStation2/task.cgi", `{"success":true,"data":{"total":1,"task":[
		{"id":"42","title":"X","type":"BT","status":4,"size":100,"additional":{"detail":{"destination":"downloads"}}}
	]}}`)
	// transfer call:
	doer.queue("/webapi/DownloadStation2/task.cgi", `{"success":true,"data":{"total":1,"task":[
		{"id":"42","title":"X","type":"BT","status":4,"size":100,"additional":{"transfer":{"size_downloaded":"50","size_uploaded":"10","speed_download":"5"}}}
	]}}`)

	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len = %d, want 1", len(items))
	}
	// Verify the V2 path was used + the transfer values merged in.
	v2Calls := 0
	for _, c := range doer.calls {
		if c.path == "/webapi/DownloadStation2/task.cgi" {
			v2Calls++
		}
	}
	if v2Calls != 2 {
		t.Errorf("V2 path calls = %d, want 2 (detail + transfer)", v2Calls)
	}
	if items[0].DownloadID != "42" {
		t.Errorf("DownloadID = %q, want 42", items[0].DownloadID)
	}
	// Status=4 is Downloading in the int-shape V2 numeric enum,
	// which lands at TaskStatusDownloading in the merged Task.
	if items[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("Status = %v, want Downloading", items[0].Status)
	}
}

func TestProxy_TaskAPIVersion_V2_RemoveTaskUsesV2Path(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryV2(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/DownloadStation2/task.cgi", `{"success":true,"data":{}}`)

	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "x"}, false); err != nil {
		t.Fatalf("err = %v", err)
	}
	// V2 delete uses query method=delete + version=2; V1 uses version=1.
	v2Calls := 0
	v1Calls := 0
	for _, c := range doer.calls {
		switch c.path {
		case "/webapi/DownloadStation2/task.cgi":
			v2Calls++
			if c.query.Get("version") != "2" {
				t.Errorf("V2 delete version = %q, want 2", c.query.Get("version"))
			}
		case "/webapi/DownloadStation/task.cgi":
			v1Calls++
		}
	}
	if v2Calls != 1 || v1Calls != 0 {
		t.Errorf("calls v2=%d v1=%d, want 1/0 (V2 dispatch)", v2Calls, v1Calls)
	}
}

func TestProxy_TaskAPIVersion_V2_AddURLUsesV2PathAndParams(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryV2(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	// 1. V2 create.
	doer.queue("/webapi/DownloadStation2/task.cgi", `{"success":true,"data":{}}`)
	// 2-3. V2 post-add list (detail + transfer).
	doer.queue("/webapi/DownloadStation2/task.cgi", `{"success":true,"data":{"total":1,"task":[
		{"id":"new","title":"X","type":"BT","status":4,"additional":{"detail":{"uri":"magnet:?abc"}}}
	]}}`)
	doer.queue("/webapi/DownloadStation2/task.cgi", `{"success":true,"data":{"total":1,"task":[
		{"id":"new","title":"X","type":"BT","status":4,"additional":{"transfer":{"size_downloaded":"0"}}}
	]}}`)

	p := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "new" {
		t.Errorf("id = %q, want new", id)
	}
	// Verify the create call used the V2 path AND included the
	// V2-specific query params (type=url, create_list=false).
	for _, c := range doer.calls {
		if c.path == "/webapi/DownloadStation2/task.cgi" && c.query.Get("method") == "create" {
			if c.query.Get("type") != "url" {
				t.Errorf("V2 create type = %q, want url", c.query.Get("type"))
			}
			if c.query.Get("create_list") != "false" {
				t.Errorf("V2 create create_list = %q, want false", c.query.Get("create_list"))
			}
			if c.query.Get("url") != "magnet:?abc" {
				t.Errorf("V2 create url = %q, want magnet:?abc", c.query.Get("url"))
			}
		}
	}
}

// ---- UsenetDownloadStation half (5.J77) -------------------------

func TestUsenetIdentity(t *testing.T) {
	p := downloadstation.NewUsenet(&downloadclients.DownloadClient{Name: "UDS"}, newSettings(), nil, nil)
	if p.Name() != "Download Station" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolUsenet {
		t.Errorf("Protocol = %v, want Usenet", p.Protocol())
	}
}

func TestUsenet_Download_HappyPath_MultipartShape(t *testing.T) {
	doer := newMultipartCapture()
	queueAPIDiscovery(doer.scripted, 3)
	doer.scripted.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID"}}`)
	// create POST response.
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{}}`)
	// post-add list.
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"dbid_99","title":"Show.S01E01","type":"NZB","status":"downloading","additional":{"detail":{"uri":"Show.S01E01.nzb"}}}
	]}}`)

	p := downloadstation.NewUsenet(&downloadclients.DownloadClient{Name: "UDS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		NzbBody:      []byte("FAKE-NZB-PAYLOAD"),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "dbid_99" {
		t.Errorf("id = %q, want dbid_99", id)
	}
	// Verify the create call was a multipart POST.
	if doer.lastCreateContentType == "" || !strings.HasPrefix(doer.lastCreateContentType, "multipart/form-data;") {
		t.Errorf("create Content-Type = %q, want multipart/form-data;...", doer.lastCreateContentType)
	}
	if !strings.Contains(string(doer.lastCreateBody), "FAKE-NZB-PAYLOAD") {
		t.Errorf("multipart body missing NZB payload bytes")
	}
	if !strings.Contains(string(doer.lastCreateBody), `name="file"`) {
		t.Errorf("multipart body missing file form-field")
	}
	if !strings.Contains(string(doer.lastCreateBody), `filename="Show.S01E01.nzb"`) {
		t.Errorf("multipart body missing filename")
	}
	if !strings.Contains(string(doer.lastCreateBody), `name="_sid"`) {
		t.Errorf("multipart body missing _sid form-field")
	}
}

func TestUsenet_Download_EmptyBody(t *testing.T) {
	p := downloadstation.NewUsenet(&downloadclients.DownloadClient{Name: "UDS"}, newSettings(), downloadstation.NewProxy(newScripted(), nil, nil), nil)
	if _, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"}); err == nil {
		t.Fatal("err = nil, want empty-body error")
	}
}

func TestUsenet_Download_NoMatchSurfacesError(t *testing.T) {
	doer := newMultipartCapture()
	queueAPIDiscovery(doer.scripted, 3)
	doer.scripted.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID"}}`)
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{}}`)
	doer.scripted.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[]}}`)
	p := downloadstation.NewUsenet(&downloadclients.DownloadClient{Name: "UDS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X", NzbBody: []byte("x")})
	if err == nil {
		t.Fatal("err = nil, want no-matching-task error")
	}
}

func TestUsenet_GetItems_FiltersToNZB(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"n","title":"NzbItem","type":"NZB","status":"downloading","size":100,"additional":{"detail":{"destination":"downloads"},"transfer":{"size_downloaded":"50","speed_download":"10"}}},
		{"id":"b","title":"BtItem","type":"BT","status":"downloading","additional":{"detail":{"destination":"downloads"},"transfer":{}}}
	]}}`)
	p := downloadstation.NewUsenet(&downloadclients.DownloadClient{Name: "UDS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 || items[0].DownloadID != "n" {
		t.Errorf("items = %+v, want only NZB", items)
	}
	if items[0].SeedRatio != nil {
		t.Errorf("SeedRatio = %v, want nil for usenet", items[0].SeedRatio)
	}
}

func TestUsenet_GetItems_CleanupFlagsAlwaysTrue(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[
		{"id":"x","title":"X","type":"NZB","status":"downloading","additional":{"detail":{"destination":"downloads"},"transfer":{}}}
	]}}`)
	// Use RemoveCompletedDownloads=false to confirm the Usenet
	// half ignores it (always sets the flags true, unlike the
	// torrent half).
	def := &downloadclients.DownloadClient{Name: "UDS", RemoveCompletedDownloads: false}
	p := downloadstation.NewUsenet(def, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 || !items[0].CanBeRemoved || !items[0].CanMoveFiles {
		t.Errorf("items = %+v, want cleanup flags true regardless of RCD", items)
	}
}

func TestUsenet_RemoveItem_HappyPath(t *testing.T) {
	doer := newScripted()
	queueAuthAndDiscovery(doer)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{}}`)
	p := downloadstation.NewUsenet(&downloadclients.DownloadClient{Name: "UDS"}, newSettings(), downloadstation.NewProxy(doer, nil, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "dbid_42"}, false); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestUsenet_MarkItemAsImported_ReturnsUnsupported(t *testing.T) {
	p := downloadstation.NewUsenet(&downloadclients.DownloadClient{Name: "UDS"}, newSettings(), downloadstation.NewProxy(newScripted(), nil, nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestRegistry_RegistersUsenetDownloadStation(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(downloadstation.UsenetImplementation, downloadstation.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", downloadstation.UsenetImplementation, downloadstation.ConfigContract)
	}
}

// multipartCaptureDoer is a thin scriptedDoer wrapper that grabs
// the body + Content-Type of the multipart create POST so the
// shape can be asserted.
type multipartCaptureDoer struct {
	scripted              *scriptedDoer
	lastCreateBody        []byte
	lastCreateContentType string
}

func newMultipartCapture() *multipartCaptureDoer {
	return &multipartCaptureDoer{scripted: newScripted()}
}

func (m *multipartCaptureDoer) Do(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && req.URL.Path == "/webapi/DownloadStation/task.cgi" {
		body, _ := io.ReadAll(req.Body)
		m.lastCreateBody = body
		m.lastCreateContentType = req.Header.Get("Content-Type")
		// Reset body for downstream pseudo-handling — scriptedDoer
		// doesn't read the body, but if it did this would be needed.
		req.Body = io.NopCloser(strings.NewReader(""))
	}
	return m.scripted.Do(req)
}

// --- J120 FileStation proxy tests --------------------------------

// queueAPIDiscoveryWithFileStation queues a SYNO.API.Info response
// advertising auth + DownloadStation.Task + SYNO.FileStation.List
// so callers exercising the J120 GetFileInfo / GetSharedFolderMapping
// paths can authenticate + discover the FileStation API in one
// round-trip.
func queueAPIDiscoveryWithFileStation(doer *scriptedDoer) {
	body := `{"success":true,"data":{
		"SYNO.API.Auth":{"path":"auth.cgi","maxVersion":3,"minVersion":1},
		"SYNO.DownloadStation.Task":{"path":"DownloadStation/task.cgi","maxVersion":3,"minVersion":1},
		"SYNO.FileStation.List":{"path":"FileStation/file_share.cgi","maxVersion":2,"minVersion":1}
	}}`
	doer.queue("/webapi/query.cgi", body)
}

func TestProxy_GetFileInfo_HappyPath(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"SID"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":true,"name":"data","path":"/data","additional":{"real_path":"/volume1/data"}}
	]}}`)

	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	info, err := proxy.GetFileInfo(context.Background(), newSettings(), "/data")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if info.Name != "data" || info.Path != "/data" || !info.IsDir {
		t.Errorf("info = %+v, want name=data path=/data isdir=true", info)
	}
	if _, ok := info.Additional["real_path"]; !ok {
		t.Errorf("Additional missing real_path: %+v", info.Additional)
	}
}

func TestProxy_GetFileInfo_RequestShape(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[{"name":"x","path":"/x"}]}}`)

	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	if _, err := proxy.GetFileInfo(context.Background(), newSettings(), "/data"); err != nil {
		t.Fatal(err)
	}
	var fs *scriptedCall
	for i := range doer.calls {
		if doer.calls[i].path == "/webapi/FileStation/file_share.cgi" {
			fs = &doer.calls[i]
		}
	}
	if fs == nil {
		t.Fatal("no FileStation call recorded")
	}
	// Match C# request shape: api=SYNO.FileStation.List, version=2,
	// method=getinfo, path=["/data"], additional=["real_path"].
	if fs.query.Get("api") != "SYNO.FileStation.List" {
		t.Errorf("api = %q, want SYNO.FileStation.List", fs.query.Get("api"))
	}
	if fs.query.Get("version") != "2" {
		t.Errorf("version = %q, want 2", fs.query.Get("version"))
	}
	if fs.query.Get("method") != "getinfo" {
		t.Errorf("method = %q, want getinfo", fs.query.Get("method"))
	}
	if got := fs.query.Get("path"); got != `["/data"]` {
		t.Errorf("path = %q, want [\"/data\"]", got)
	}
	if got := fs.query.Get("additional"); got != `["real_path"]` {
		t.Errorf("additional = %q, want [\"real_path\"]", got)
	}
}

func TestProxy_GetFileInfo_EmptyFilesArrayErrors(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[]}}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	_, err := proxy.GetFileInfo(context.Background(), newSettings(), "/data")
	if err == nil {
		t.Fatal("err = nil, want non-empty-files error")
	}
}

func TestProxy_GetSharedFolderMapping_HappyPath(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":true,"name":"data","path":"/data","additional":{"real_path":"/volume1/data"}}
	]}}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	mapping, err := proxy.GetSharedFolderMapping(context.Background(), newSettings(), "/data")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if mapping.SharedFolder != "/data" {
		t.Errorf("SharedFolder = %q, want /data", mapping.SharedFolder)
	}
	if mapping.PhysicalPath != "/volume1/data" {
		t.Errorf("PhysicalPath = %q, want /volume1/data", mapping.PhysicalPath)
	}
	if got, want := mapping.String(), "/data -> /volume1/data"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestProxy_GetSharedFolderMapping_MissingRealPathErrors(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":true,"name":"data","path":"/data","additional":{}}
	]}}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	_, err := proxy.GetSharedFolderMapping(context.Background(), newSettings(), "/data")
	if err == nil {
		t.Fatal("err = nil, want missing-real_path error")
	}
}

// --- J122 GetStatus OutputRootFolders projection -----------------

func TestProvider_GetStatus_J122_TvDirectoryResolved(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":true,"name":"data","path":"/data","additional":{"real_path":"/volume1/data"}}
	]}}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	s := newSettings()
	s.TvDirectory = "data/tv"
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, proxy, nil)
	info, err := prov.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !info.IsLocalhost && newSettings().Host == "localhost" {
		t.Errorf("IsLocalhost should propagate for host=localhost")
	}
	if len(info.OutputRootFolders) != 1 {
		t.Fatalf("OutputRootFolders = %v, want 1 entry", info.OutputRootFolders)
	}
	if got := info.OutputRootFolders[0].FullPath(); got != "/volume1/data/tv" {
		t.Errorf("OutputRootFolders[0] = %q, want /volume1/data/tv", got)
	}
}

func TestProvider_GetStatus_J122_EmptyTvDirectoryDegradesToIsLocalhost(t *testing.T) {
	doer := newScripted()
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	s := newSettings()
	s.TvDirectory = ""
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, proxy, nil)
	info, err := prov.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders = %v, want empty (no TvDirectory)", info.OutputRootFolders)
	}
}

func TestProvider_GetStatus_J122_ResolverFailureDegrades(t *testing.T) {
	// FileStation getinfo returns nothing → resolver fails → GetStatus
	// degrades to IsLocalhost-only rather than surfacing an error.
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":false,"error":{"code":408}}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	s := newSettings()
	s.TvDirectory = "data/tv"
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, proxy, nil)
	info, err := prov.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want nil (resolver failure non-fatal)", err)
	}
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders = %v, want empty on resolver failure", info.OutputRootFolders)
	}
}

func TestProvider_GetStatus_J122_RelativeTvDirectoryGetsLeadingSlash(t *testing.T) {
	// C# `:124-126` prefixes a leading `/` because DSM returns paths
	// without it. The Go port mirrors so users who configured
	// TvDirectory as `data/tv` (no leading slash) still resolve.
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":true,"name":"data","path":"/data","additional":{"real_path":"/volume1/data"}}
	]}}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	s := newSettings()
	s.TvDirectory = "data/tv" // no leading slash
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, proxy, nil)
	info, err := prov.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := info.OutputRootFolders[0].FullPath(); got != "/volume1/data/tv" {
		t.Errorf("OutputRootFolders[0] = %q, want /volume1/data/tv", got)
	}
}

// --- J123 TestOutputPath wire-up ---------------------------------

// queueAllArmsPass queues API discovery + auth + version-info +
// empty task-list (passes the first 4 Test arms unchanged).
func queueAllArmsPass(doer *scriptedDoer) {
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	// Test() ALSO calls GetTasks (task list). Queue an empty list.
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true,"data":{"tasks":[]}}`)
}

func TestProvider_TestOutputPath_J123_HappyPathOnValidDir(t *testing.T) {
	doer := newScripted()
	queueAllArmsPass(doer)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":true,"name":"tv","path":"/data/tv","additional":{"real_path":"/volume1/data/tv"}}
	]}}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	s := newSettings()
	s.TvDirectory = "data/tv"
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, proxy, nil)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want none", failures)
	}
}

func TestProvider_TestOutputPath_J123_EmptyAdditionalSurfacesSharedFolderMissing(t *testing.T) {
	doer := newScripted()
	queueAllArmsPass(doer)
	// FileStation returns a file row WITHOUT an "additional" field —
	// signals that the SharedFolder doesn't exist.
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":true,"name":"tv","path":"/data/tv"}
	]}}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	s := newSettings()
	s.TvDirectory = "missing-shared/tv"
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, proxy, nil)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].PropertyName != "TvDirectory" {
		t.Fatalf("failures = %+v, want one TvDirectory failure", failures)
	}
	if !strings.Contains(failures[0].ErrorMessage, "Shared folder") {
		t.Errorf("ErrorMessage = %q, want shared-folder failure", failures[0].ErrorMessage)
	}
	if !strings.Contains(failures[0].ErrorMessage, "missing-shared") {
		t.Errorf("ErrorMessage = %q, want first-path-component in message", failures[0].ErrorMessage)
	}
}

func TestProvider_TestOutputPath_J123_NotADirectorySurfacesFolderMissing(t *testing.T) {
	doer := newScripted()
	queueAllArmsPass(doer)
	// File-not-folder: isdir=false with non-empty additional.
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":false,"name":"thing","path":"/data/thing","additional":{"real_path":"/volume1/data/thing"}}
	]}}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	s := newSettings()
	s.TvDirectory = "data/thing"
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, proxy, nil)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].PropertyName != "TvDirectory" {
		t.Fatalf("failures = %+v, want one TvDirectory failure", failures)
	}
	if !strings.Contains(failures[0].ErrorMessage, "Folder") || !strings.Contains(failures[0].ErrorMessage, "does not exist") {
		t.Errorf("ErrorMessage = %q, want folder-missing failure", failures[0].ErrorMessage)
	}
}

func TestProvider_TestOutputPath_J123_EmptyTvDirectorySkipsProbe(t *testing.T) {
	// No TvDirectory configured → no FileStation call. The earlier
	// arms still pass.
	doer := newScripted()
	queueAllArmsPass(doer)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	s := newSettings()
	s.TvDirectory = ""
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, proxy, nil)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want none (TvDirectory empty -> skip probe)", failures)
	}
	// Confirm no /webapi/FileStation/file_share.cgi call landed.
	for _, c := range doer.calls {
		if c.path == "/webapi/FileStation/file_share.cgi" {
			t.Errorf("FileStation probe fired despite empty TvDirectory; calls=%+v", doer.calls)
			break
		}
	}
}

func TestProvider_TestOutputPath_J123_FileStationFailureDegradesSilent(t *testing.T) {
	// FileStation transport-error → degrade to silent (no failure
	// surfaced). Same posture as the C# `:346-356` catch-arms.
	doer := newScripted()
	queueAllArmsPass(doer)
	doer.queueErr("/webapi/FileStation/file_share.cgi", errors.New("transient"))
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	s := newSettings()
	s.TvDirectory = "data/tv"
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, s, proxy, nil)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want none (silent degradation on FileStation error)", failures)
	}
}

// --- J124 RemoveItem deleteData disk arm wire-up -----------------

// stubDSDisk is a hand-rolled [downloadclients.DiskProvider] used by
// the J124 RemoveItem-deleteData tests.
type stubDSDisk struct {
	called bool
	path   string
	err    error
}

func (s *stubDSDisk) DeleteFolder(path string, _ bool) error {
	s.called = true
	s.path = path
	return s.err
}

func TestProvider_RemoveItem_J124_DeleteDataResolvesAndCallsDisk(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":true,"name":"data","path":"/data","additional":{"real_path":"/volume1/data"}}
	]}}`)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), proxy, nil)
	disk := &stubDSDisk{}
	prov.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "task1",
		OutputPath: ospath.New("/data/tv/Show"),
	}
	if err := prov.RemoveItem(context.Background(), item, true); err != nil {
		t.Fatal(err)
	}
	if !disk.called {
		t.Errorf("DiskProvider.DeleteFolder not called")
	}
	if disk.path != "/volume1/data/tv/Show" {
		t.Errorf("disk path = %q, want /volume1/data/tv/Show (resolved via SharedFolder)", disk.path)
	}
	// Confirm RemoveTask still fired.
	saw := 0
	for _, c := range doer.calls {
		if c.path == "/webapi/DownloadStation/task.cgi" {
			saw++
		}
	}
	if saw != 1 {
		t.Errorf("DownloadStation/task.cgi fired %d times, want 1", saw)
	}
}

func TestProvider_RemoveItem_J124_DeleteDataFalseSkipsDisk(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), proxy, nil)
	disk := &stubDSDisk{}
	prov.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "task1",
		OutputPath: ospath.New("/data/tv/Show"),
	}
	if err := prov.RemoveItem(context.Background(), item, false); err != nil {
		t.Fatal(err)
	}
	if disk.called {
		t.Errorf("DiskProvider fired despite deleteData=false")
	}
}

func TestProvider_RemoveItem_J124_DeleteDataNoDiskProviderWarns(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), proxy, nil)
	// no SetDiskProvider — degraded warn path.
	item := &downloadclients.Item{
		DownloadID: "task1",
		OutputPath: ospath.New("/data/tv/Show"),
	}
	if err := prov.RemoveItem(context.Background(), item, true); err != nil {
		t.Errorf("nil DiskProvider must not error: %v", err)
	}
	saw := 0
	for _, c := range doer.calls {
		if c.path == "/webapi/DownloadStation/task.cgi" {
			saw++
		}
	}
	if saw != 1 {
		t.Errorf("task.cgi removal fired %d times, want 1 (nil-disk arm still removes from DSM)", saw)
	}
}

func TestProvider_RemoveItem_J124_DeleteDataEmptyOutputPathSkipsDisk(t *testing.T) {
	doer := newScripted()
	queueAPIDiscovery(doer, 3)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), proxy, nil)
	disk := &stubDSDisk{}
	prov.SetDiskProvider(disk)
	if err := prov.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "task1"}, true); err != nil {
		t.Fatal(err)
	}
	if disk.called {
		t.Errorf("DiskProvider fired on empty OutputPath")
	}
}

func TestProvider_RemoveItem_J124_DiskErrorDoesNotAbortRemoveTask(t *testing.T) {
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi", `{"success":true,"data":{"files":[
		{"isdir":true,"name":"data","path":"/data","additional":{"real_path":"/volume1/data"}}
	]}}`)
	doer.queue("/webapi/DownloadStation/task.cgi", `{"success":true}`)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	prov := downloadstation.New(&downloadclients.DownloadClient{Name: "DS"}, newSettings(), proxy, nil)
	disk := &stubDSDisk{err: errors.New("permission denied")}
	prov.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "task1",
		OutputPath: ospath.New("/data/tv/Show"),
	}
	if err := prov.RemoveItem(context.Background(), item, true); err != nil {
		t.Errorf("disk-arm failure must not abort RemoveTask: %v", err)
	}
	if !disk.called {
		t.Errorf("disk arm not called")
	}
	saw := 0
	for _, c := range doer.calls {
		if c.path == "/webapi/DownloadStation/task.cgi" {
			saw++
		}
	}
	if saw != 1 {
		t.Errorf("DSM remove fired %d times, want 1 (disk failure non-fatal)", saw)
	}
}
