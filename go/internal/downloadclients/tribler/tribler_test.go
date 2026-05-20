package tribler_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/tribler"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
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

// (J84 wires RemoveItem; the Tribler arc is functionally complete.
// Per-method RemoveItem tests cover the behaviour individually.)

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

// queueDownloadsWithFiles queues a /api/downloads list + a series
// of /api/downloads/<hash>/files responses (one per torrent).
func queueDownloadsWithFiles(doer *scriptedDoer, downloadsBody string, filesByHash map[string]string) {
	doer.queue("/api/downloads", downloadsBody)
	for hash, body := range filesByHash {
		doer.queue("/api/downloads/"+hash+"/files", body)
	}
}

func TestProvider_GetItems_EmptyList(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/downloads", `{"downloads":[]}`)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len = %d, want 0", len(items))
	}
}

func TestProvider_GetItems_SkipsMagnetMetadata(t *testing.T) {
	// size==0 → still fetching magnet metadata; skip per C# :58.
	doer := newScripted()
	doer.queue("/api/downloads", `{"downloads":[
		{"name":"X","infohash":"a","status":"DOWNLOADING","size":0,"progress":0.5,"destination":"/dl"}
	]}`)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 0 {
		t.Errorf("len = %d, want 0 (size=0 skipped)", len(items))
	}
}

func TestProvider_GetItems_StatusTable(t *testing.T) {
	// One row per arm of the status table. Files responses are
	// queued for each so the OutputPath single-file path doesn't
	// blow up.
	rows := `{"downloads":[
		{"name":"Down","infohash":"d","status":"DOWNLOADING","size":100,"progress":0.5,"destination":"/dl"},
		{"name":"Meta","infohash":"m","status":"METADATA","size":100,"progress":0,"destination":"/dl"},
		{"name":"Done","infohash":"f","status":"SEEDING","size":100,"progress":1,"destination":"/dl"},
		{"name":"PausedDl","infohash":"p","status":"STOPPED","size":100,"progress":0.5,"destination":"/dl"},
		{"name":"Stopped100","infohash":"s","status":"STOPPED","size":100,"progress":1,"destination":"/dl"},
		{"name":"Errored","infohash":"e","status":"STOPPED_ON_ERROR","size":100,"progress":0.5,"destination":"/dl"},
		{"name":"WithError","infohash":"w","status":"DOWNLOADING","size":100,"progress":0.5,"destination":"/dl","error":"disk full"}
	]}`
	doer := newScripted()
	doer.queue("/api/downloads", rows)
	for _, h := range []string{"d", "m", "f", "p", "s", "e", "w"} {
		doer.queue("/api/downloads/"+h+"/files", `{"files":[{"name":"file.mkv"}]}`)
	}
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 7 {
		t.Fatalf("len = %d, want 7", len(items))
	}
	byID := map[string]*downloadclients.Item{}
	for _, it := range items {
		byID[it.DownloadID] = it
	}
	want := map[string]downloadclients.DownloadItemStatus{
		"d": downloadclients.DownloadItemStatusDownloading,
		"m": downloadclients.DownloadItemStatusQueued,
		"f": downloadclients.DownloadItemStatusCompleted,
		"p": downloadclients.DownloadItemStatusPaused, // Stopped + Progress < 1 override
		"s": downloadclients.DownloadItemStatusCompleted,
		"e": downloadclients.DownloadItemStatusFailed,
		"w": downloadclients.DownloadItemStatusWarning, // error override
	}
	for id, st := range want {
		got, ok := byID[id]
		if !ok {
			t.Errorf("missing item %s", id)
			continue
		}
		if got.Status != st {
			t.Errorf("%s Status = %v, want %v", id, got.Status, st)
		}
	}
	if byID["w"].Message != "disk full" {
		t.Errorf("error-arm Message = %q, want 'disk full'", byID["w"].Message)
	}
}

func TestProvider_GetItems_OutputPath_SingleFileFromFilesResponse(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/downloads", `{"downloads":[
		{"name":"Show","infohash":"abc","status":"SEEDING","size":100,"progress":1,"destination":"/dl"}
	]}`)
	doer.queue("/api/downloads/abc/files", `{"files":[{"name":"episode.mkv"}]}`)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 {
		t.Fatalf("len = %d", len(items))
	}
	if items[0].OutputPath.FullPath() != "/dl/episode.mkv" {
		t.Errorf("OutputPath = %q, want /dl/episode.mkv (single-file suffix)", items[0].OutputPath.FullPath())
	}
}

func TestProvider_GetItems_OutputPath_MultiFileFallsBackToTitle(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/downloads", `{"downloads":[
		{"name":"PackTitle","infohash":"abc","status":"SEEDING","size":100,"progress":1,"destination":"/dl"}
	]}`)
	doer.queue("/api/downloads/abc/files", `{"files":[{"name":"a.mkv"},{"name":"b.mkv"}]}`)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if items[0].OutputPath.FullPath() != "/dl/PackTitle" {
		t.Errorf("OutputPath = %q, want /dl/PackTitle (multi-file → title fallback)", items[0].OutputPath.FullPath())
	}
}

func TestProvider_GetItems_OutputPath_FilesErrorFallsBackToTitle(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/downloads", `{"downloads":[
		{"name":"X","infohash":"abc","status":"SEEDING","size":100,"progress":1,"destination":"/dl"}
	]}`)
	doer.queueErr("/api/downloads/abc/files", errors.New("dial"))
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if items[0].OutputPath.FullPath() != "/dl/X" {
		t.Errorf("OutputPath = %q, want /dl/X (files-error → title fallback)", items[0].OutputPath.FullPath())
	}
}

func TestProvider_GetItems_ETAClamp(t *testing.T) {
	cases := []struct {
		name string
		eta  float64
		want time.Duration
	}{
		{"negative", -10, 0},
		{"normal", 60, 60 * time.Second},
		{"over a year", 366 * 24 * 3600, 365 * 24 * time.Hour},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"downloads":[
				{"name":"X","infohash":"abc","status":"DOWNLOADING","size":100,"progress":0.5,"destination":"/dl","eta":` + ftoa(tt.eta) + `}
			]}`
			doer := newScripted()
			doer.queue("/api/downloads", body)
			doer.queue("/api/downloads/abc/files", `{"files":[{"name":"f.mkv"}]}`)
			p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
			items, _ := p.GetItems(context.Background())
			if items[0].RemainingTime == nil || *items[0].RemainingTime != tt.want {
				t.Errorf("eta=%v RemainingTime = %v, want %v", tt.eta, items[0].RemainingTime, tt.want)
			}
		})
	}
}

func TestProvider_GetItems_RemainingSizeComputed(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/downloads", `{"downloads":[
		{"name":"X","infohash":"abc","status":"DOWNLOADING","size":1000,"progress":0.4,"destination":"/dl"}
	]}`)
	doer.queue("/api/downloads/abc/files", `{"files":[{"name":"f.mkv"}]}`)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if items[0].RemainingSize != 600 {
		t.Errorf("RemainingSize = %d, want 600 (1000 * (1 - 0.4))", items[0].RemainingSize)
	}
	if items[0].TotalSize != 1000 {
		t.Errorf("TotalSize = %d, want 1000", items[0].TotalSize)
	}
}

func TestProvider_GetItems_RemotePathMapperApplied(t *testing.T) {
	doer := newScripted()
	doer.queue("/api/downloads", `{"downloads":[
		{"name":"X","infohash":"abc","status":"SEEDING","size":100,"progress":1,"destination":"/remote/dl"}
	]}`)
	doer.queue("/api/downloads/abc/files", `{"files":[{"name":"f.mkv"}]}`)
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	p.SetRemotePathMapper(triblerStubRemapper{from: "/remote/dl", to: "/local/dl"})
	items, _ := p.GetItems(context.Background())
	if items[0].OutputPath.FullPath() != "/local/dl/f.mkv" {
		t.Errorf("OutputPath = %q, want /local/dl/f.mkv (remap + file suffix)", items[0].OutputPath.FullPath())
	}
}

func TestProvider_GetItems_PropagatesGetDownloadsError(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/api/downloads", errors.New("dial"))
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	_, err := p.GetItems(context.Background())
	if !errors.Is(err, tribler.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

// ftoa is a tiny float-to-string helper for inline JSON in tests.
func ftoa(f float64) string {
	switch f {
	case 0:
		return "0"
	case -10:
		return "-10"
	case 60:
		return "60"
	default:
		// 366 * 86400 = 31622400.
		return "31622400"
	}
}

// captureDeleteDoer wraps scriptedDoer and grabs the last DELETE
// body + URL path so the RemoveItem tests can assert the wire
// shape.
type captureDeleteDoer struct {
	scripted   *scriptedDoer
	lastBody   []byte
	lastPath   string
	lastMethod string
}

func (c *captureDeleteDoer) Do(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodDelete {
		if req.Body != nil {
			body, _ := io.ReadAll(req.Body)
			c.lastBody = body
			req.Body = io.NopCloser(strings.NewReader(""))
		}
		c.lastPath = req.URL.Path
		c.lastMethod = req.Method
	}
	return c.scripted.Do(req)
}

func TestProvider_RemoveItem_DeleteDataTrue_RequestShape(t *testing.T) {
	doer := &captureDeleteDoer{scripted: newScripted()}
	doer.scripted.responses["/api/downloads/abc"] = append(doer.scripted.responses["/api/downloads/abc"], scriptedResp{
		status: http.StatusOK,
		body:   `{"removed":true,"infohash":"abc"}`,
	})
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "abc"}, true); err != nil {
		t.Fatalf("err = %v", err)
	}
	if doer.lastMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", doer.lastMethod)
	}
	if doer.lastPath != "/api/downloads/abc" {
		t.Errorf("path = %q, want /api/downloads/abc", doer.lastPath)
	}
	if !strings.Contains(string(doer.lastBody), `"remove_data":true`) {
		t.Errorf("body = %s, want it to contain remove_data:true", doer.lastBody)
	}
}

func TestProvider_RemoveItem_DeleteDataFalse(t *testing.T) {
	doer := &captureDeleteDoer{scripted: newScripted()}
	doer.scripted.responses["/api/downloads/abc"] = append(doer.scripted.responses["/api/downloads/abc"], scriptedResp{
		status: http.StatusOK,
		body:   `{"removed":true}`,
	})
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "abc"}, false); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(string(doer.lastBody), `"remove_data":false`) {
		t.Errorf("body = %s, want remove_data:false", doer.lastBody)
	}
}

func TestProvider_RemoveItem_LowercasesInfohash(t *testing.T) {
	// C# `:99` lowercases the DownloadId — the Go port mirrors so
	// case-flipped queue IDs hit the same Tribler-side identifier.
	doer := &captureDeleteDoer{scripted: newScripted()}
	doer.scripted.responses["/api/downloads/abcdef"] = append(doer.scripted.responses["/api/downloads/abcdef"], scriptedResp{
		status: http.StatusOK,
		body:   `{"removed":true}`,
	})
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABCDEF"}, false); err != nil {
		t.Fatalf("err = %v", err)
	}
	if doer.lastPath != "/api/downloads/abcdef" {
		t.Errorf("path = %q, want /api/downloads/abcdef (lowercased)", doer.lastPath)
	}
}

func TestProvider_RemoveItem_NilItem(t *testing.T) {
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(newScripted(), nil), nil)
	if err := p.RemoveItem(context.Background(), nil, false); err == nil {
		t.Fatal("err = nil, want nil-item error")
	}
}

func TestProvider_RemoveItem_EmptyDownloadID(t *testing.T) {
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(newScripted(), nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, false); err == nil {
		t.Fatal("err = nil, want empty-id error")
	}
}

func TestProvider_RemoveItem_PropagatesProxyError(t *testing.T) {
	doer := newScripted()
	doer.responses["/api/downloads/abc"] = append(doer.responses["/api/downloads/abc"], scriptedResp{status: http.StatusInternalServerError})
	p := tribler.New(&downloadclients.DownloadClient{Name: "TR"}, newSettings(), tribler.NewProxy(doer, nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "abc"}, true); !errors.Is(err, tribler.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

type triblerStubRemapper struct{ from, to string }

func (s triblerStubRemapper) RemapRemoteToLocal(_ context.Context, _ string, p ospath.OsPath) (ospath.OsPath, error) {
	if p.FullPath() == s.from {
		return ospath.New(s.to), nil
	}
	return p, nil
}

// Reference the queueDownloadsWithFiles helper so it stays linked
// even if unused this slice.
var _ = queueDownloadsWithFiles

func TestRegistry_RegistersTribler(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(tribler.Implementation, tribler.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", tribler.Implementation, tribler.ConfigContract)
	}
}
