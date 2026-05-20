package rqbit_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/rqbit"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// stubSeedCfg is a [downloadclients.SeedConfigProvider] returning a
// canned config (and optional error) per-hash. Used by the J94
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

// scriptedDoer queues responses by URL path. Multiple sequential
// calls to the same path pop the head response in order.
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
	query  string
	body   []byte
}

func newScripted() *scriptedDoer { return &scriptedDoer{responses: map[string][]scriptedResp{}} }

func (s *scriptedDoer) queue(path, body string) {
	s.responses[path] = append(s.responses[path], scriptedResp{status: http.StatusOK, body: body})
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
		method: req.Method,
		path:   req.URL.Path,
		query:  req.URL.RawQuery,
		body:   body,
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

func newSettings() rqbit.Settings { return rqbit.NewSettings() }

func TestSettings_Defaults(t *testing.T) {
	s := rqbit.NewSettings()
	if s.Host != "localhost" || s.Port != 3030 || s.UrlBase != "/" {
		t.Errorf("NewSettings = %+v, want host=localhost port=3030 urlbase=/", s)
	}
}

func TestSettings_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*rqbit.Settings)
		wantBad string
	}{
		{"happy", func(*rqbit.Settings) {}, ""},
		{"empty host", func(s *rqbit.Settings) { s.Host = "" }, "Host"},
		{"port too low", func(s *rqbit.Settings) { s.Port = 0 }, "Port"},
		{"port too high", func(s *rqbit.Settings) { s.Port = 70000 }, "Port"},
		{"urlbase no slash", func(s *rqbit.Settings) { s.UrlBase = "base" }, "UrlBase"},
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
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), nil, nil)
	if p.Name() != "RQBit" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol = %v, want Torrent", p.Protocol())
	}
	if p.Definition() == nil || p.Definition().Name != "RQ" {
		t.Errorf("Definition not bound")
	}
}

func TestProxy_GetVersion_HappyPath(t *testing.T) {
	doer := newScripted()
	doer.queue("/", `{"version":"8.1.2","server":"rqbit"}`)
	proxy := rqbit.NewProxy(doer, nil)
	v, err := proxy.GetVersion(context.Background(), newSettings())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if v != "8.1.2" {
		t.Errorf("version = %q, want 8.1.2", v)
	}
}

func TestProxy_GetVersion_TransportFails(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/", errors.New("dial"))
	proxy := rqbit.NewProxy(doer, nil)
	_, err := proxy.GetVersion(context.Background(), newSettings())
	if !errors.Is(err, rqbit.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestProxy_GetTorrents_Mapping(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{
			"id":1,"info_hash":"abc","name":"Show","output_folder":"/dl/",
			"stats":{"state":"live","total_bytes":1000,"progress_bytes":400,"uploaded_bytes":200,"finished":false,
				"live":{"download_speed":{"mbps":0.5}}}
		},
		{
			"id":2,"info_hash":"def","name":"Movie","output_folder":"/dl/",
			"stats":{"state":"error","total_bytes":2000,"progress_bytes":2000,"uploaded_bytes":1000,"finished":true,"error":"boom"}
		}
	]}`)
	proxy := rqbit.NewProxy(doer, nil)
	tors, err := proxy.GetTorrents(context.Background(), newSettings())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(tors) != 2 {
		t.Fatalf("len = %d, want 2", len(tors))
	}
	if tors[0].Hash != "abc" || tors[0].TotalSize != 1000 || tors[0].RemainingSize != 600 {
		t.Errorf("first = %+v", tors[0])
	}
	if !tors[0].IsActive || tors[0].IsFinished {
		t.Errorf("first state: active=%v finished=%v, want active=true finished=false", tors[0].IsActive, tors[0].IsFinished)
	}
	// Ratio = uploaded / progress = 200/400 = 0.5
	if tors[0].Ratio < 0.49 || tors[0].Ratio > 0.51 {
		t.Errorf("first Ratio = %f, want ~0.5", tors[0].Ratio)
	}
	// DownRate = 0.5 mbps * 1048576 = 524288 bytes/sec
	if tors[0].DownRate != 524288 {
		t.Errorf("first DownRate = %d, want 524288", tors[0].DownRate)
	}
	if !tors[1].HasError || tors[1].Message != "boom" {
		t.Errorf("second error fields = %+v", tors[1])
	}
}

func TestProxy_AddTorrentFromURL_ReturnsInfoHash(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"id":1,"details":{"info_hash":"NEW-HASH","name":"X"}}`)
	proxy := rqbit.NewProxy(doer, nil)
	hash, err := proxy.AddTorrentFromURL(context.Background(), newSettings(), "magnet:?xt=urn:btih:x")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if hash != "NEW-HASH" {
		t.Errorf("hash = %q", hash)
	}
	// Verify the URL was POSTed as the body.
	for _, c := range doer.calls {
		if c.path == "/torrents" && c.method == http.MethodPost {
			if string(c.body) != "magnet:?xt=urn:btih:x" {
				t.Errorf("body = %q, want magnet URL verbatim", c.body)
			}
			if !strings.Contains(c.query, "overwrite=true") {
				t.Errorf("query = %q, want overwrite=true", c.query)
			}
		}
	}
}

func TestProxy_AddTorrentFromURL_EmptyDetailsSurfacesRPC(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"id":1,"details":null}`)
	proxy := rqbit.NewProxy(doer, nil)
	_, err := proxy.AddTorrentFromURL(context.Background(), newSettings(), "magnet:?")
	if !errors.Is(err, rqbit.ErrRPC) {
		t.Errorf("err = %v, want ErrRPC", err)
	}
}

func TestProxy_AddTorrentFromFile_HappyPath(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"id":1,"details":{"info_hash":"FILE-HASH","name":"X"}}`)
	proxy := rqbit.NewProxy(doer, nil)
	hash, err := proxy.AddTorrentFromFile(context.Background(), newSettings(), []byte("RAW-BYTES"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if hash != "FILE-HASH" {
		t.Errorf("hash = %q", hash)
	}
	for _, c := range doer.calls {
		if c.path == "/torrents" && c.method == http.MethodPost {
			if string(c.body) != "RAW-BYTES" {
				t.Errorf("body = %q, want raw bytes", c.body)
			}
		}
	}
}

func TestProxy_RemoveTorrent_Forget(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents/abc/forget", `{}`)
	proxy := rqbit.NewProxy(doer, nil)
	if err := proxy.RemoveTorrent(context.Background(), newSettings(), "abc", false); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestProxy_RemoveTorrent_Delete(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents/abc/delete", `{}`)
	proxy := rqbit.NewProxy(doer, nil)
	if err := proxy.RemoveTorrent(context.Background(), newSettings(), "abc", true); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestProvider_Test_HappyPath(t *testing.T) {
	doer := newScripted()
	doer.queue("/", `{"version":"8.1.2"}`)
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
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
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, s, rqbit.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) == 0 {
		t.Fatal("failures empty, want Port failure")
	}
	if len(doer.calls) != 0 {
		t.Errorf("proxy called despite Settings.Validate failure")
	}
}

func TestProvider_Test_VersionTooOld(t *testing.T) {
	doer := newScripted()
	doer.queue("/", `{"version":"7.9.0"}`)
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || !strings.Contains(failures[0].ErrorMessage, "7.9.0") {
		t.Errorf("failures = %+v, want one mentioning 7.9.0", failures)
	}
}

func TestProvider_Test_VersionUnparseable(t *testing.T) {
	doer := newScripted()
	doer.queue("/", `{"version":"weird-version-x"}`)
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want one entry", failures)
	}
}

func TestProvider_Test_ConnectionFailureSurfacesHost(t *testing.T) {
	doer := newScripted()
	doer.queueErr("/", errors.New("dial"))
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || failures[0].PropertyName != "Host" {
		t.Errorf("failures = %+v, want Host failure", failures)
	}
}

func TestProvider_Download_MagnetArm(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"id":1,"details":{"info_hash":"MAG"}}`)
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:m",
		InfoHash:     "m",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "MAG" {
		t.Errorf("id = %q, want MAG", id)
	}
}

func TestProvider_Download_TorrentBodyArm(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"id":1,"details":{"info_hash":"FILE"}}`)
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
	id, err := p.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		TorrentBody:  []byte("RAW"),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "FILE" {
		t.Errorf("id = %q, want FILE", id)
	}
}

func TestProvider_Download_NoBodyOrMagnet(t *testing.T) {
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(newScripted(), nil), nil)
	_, err := p.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Fatal("err = nil, want neither-body-nor-magnet error")
	}
}

func TestProvider_GetItems_StatusTable(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":50,"finished":false}},
		{"id":2,"info_hash":"b","name":"B","output_folder":"/dl/","stats":{"state":"paused","total_bytes":100,"progress_bytes":50,"finished":false}},
		{"id":3,"info_hash":"c","name":"C","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":100,"finished":true}}
	]}`)
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
	items, err := p.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("len = %d", len(items))
	}
	byID := map[string]*downloadclients.Item{}
	for _, it := range items {
		byID[it.DownloadID] = it
	}
	if byID["a"].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("a Status = %v, want Downloading", byID["a"].Status)
	}
	if byID["b"].Status != downloadclients.DownloadItemStatusPaused {
		t.Errorf("b Status = %v, want Paused", byID["b"].Status)
	}
	if byID["c"].Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("c Status = %v, want Completed", byID["c"].Status)
	}
}

func TestProvider_GetItems_SkipsEmptyPath(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"","output_folder":"","stats":{"state":"live","total_bytes":100}}
	]}`)
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 0 {
		t.Errorf("len = %d, want 0 (empty path skipped)", len(items))
	}
}

func TestProvider_GetItems_SkipsDotPrefixedPath(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"X","output_folder":".","stats":{"state":"live","total_bytes":100}}
	]}`)
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 0 {
		t.Errorf("len = %d, want 0 (dot-prefixed path skipped)", len(items))
	}
}

func TestProvider_GetItems_RemotePathMapperApplied(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"X","output_folder":"/remote/","stats":{"state":"live","total_bytes":100}}
	]}`)
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
	p.SetRemotePathMapper(stubRemapper{from: "/remote/X", to: "/local/X"})
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 || items[0].OutputPath.FullPath() != "/local/X" {
		t.Errorf("items = %+v, want OutputPath /local/X (remapped)", items)
	}
}

func TestProvider_GetItems_CleanupFlags_NoSeedConfigBindingDefaultsFalse(t *testing.T) {
	// J94 wired the cleanup-gate to require a seed-config binding.
	// Without one (composition root hasn't injected it), the gate
	// degrades to "never satisfy" — even a finished torrent with
	// RemoveCompletedDownloads=true keeps CanBeRemoved=false.
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":100,"finished":true}}
	]}`)
	def := &downloadclients.DownloadClient{Name: "RQ", RemoveCompletedDownloads: true}
	p := rqbit.New(def, newSettings(), rqbit.NewProxy(doer, nil), nil)
	items, _ := p.GetItems(context.Background())
	if len(items) != 1 {
		t.Fatalf("items=%d, want 1", len(items))
	}
	if items[0].CanBeRemoved || items[0].CanMoveFiles {
		t.Errorf("nil seedCfg binding: cleanup flags should stay false: %+v", items[0])
	}
}

func TestProvider_GetItems_CleanupFlags_RemoveCompletedFalseDefaults(t *testing.T) {
	// RemoveCompletedDownloads=false (the C# `:101` AND-arm) →
	// gate fails before consulting the seedCfg.
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":100,"finished":true}}
	]}`)
	def := &downloadclients.DownloadClient{Name: "RQ", RemoveCompletedDownloads: false}
	p := rqbit.New(def, newSettings(), rqbit.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"a": {Ratio: ptrFloat(0.5)},
	}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanBeRemoved || items[0].CanMoveFiles {
		t.Errorf("RemoveCompletedDownloads=false: %+v", items[0])
	}
}

func TestProvider_GetItems_CleanupFlags_NotFinishedDefaults(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":50,"finished":false}}
	]}`)
	def := &downloadclients.DownloadClient{Name: "RQ", RemoveCompletedDownloads: true}
	p := rqbit.New(def, newSettings(), rqbit.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"a": {Ratio: ptrFloat(0.5)},
	}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanBeRemoved || items[0].CanMoveFiles {
		t.Errorf("not-finished torrent: %+v", items[0])
	}
}

func TestProvider_GetItems_CleanupFlags_NilSeedConfigForHashDefaults(t *testing.T) {
	// seedCfg lookup returns (nil, nil) — torrent wasn't grabbed
	// via a recorded indexer. C# `:101` `seedConfig != null` guard
	// fails the gate.
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":100,"finished":true}}
	]}`)
	def := &downloadclients.DownloadClient{Name: "RQ", RemoveCompletedDownloads: true}
	p := rqbit.New(def, newSettings(), rqbit.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanBeRemoved || items[0].CanMoveFiles {
		t.Errorf("nil seedConfig for hash: %+v", items[0])
	}
}

func TestProvider_GetItems_CleanupFlags_LookupErrorDefaultsFalse(t *testing.T) {
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":100,"finished":true}}
	]}`)
	def := &downloadclients.DownloadClient{Name: "RQ", RemoveCompletedDownloads: true}
	p := rqbit.New(def, newSettings(), rqbit.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{err: errors.New("transient")})
	items, _ := p.GetItems(context.Background())
	if items[0].CanBeRemoved || items[0].CanMoveFiles {
		t.Errorf("transient lookup error: %+v", items[0])
	}
}

func TestProvider_GetItems_CleanupFlags_TimeArmFiresWhenFinishedTimeZero(t *testing.T) {
	// FinishedTime is never populated by the wire shape (C#
	// proxy at `RQbitProxy.cs:95-130` leaves it 0), so the
	// time-arm at `RQBit.cs:110` degenerates to "now since 1970
	// >= SeedTime" — trivially true for any positive SeedTime.
	// This preserves C# observable behaviour where a finished
	// torrent with a SeedTime config trivially passes the gate.
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":100,"finished":true}}
	]}`)
	def := &downloadclients.DownloadClient{Name: "RQ", RemoveCompletedDownloads: true}
	p := rqbit.New(def, newSettings(), rqbit.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"a": {SeedTime: ptrDur(2 * time.Hour)},
	}})
	items, _ := p.GetItems(context.Background())
	if !items[0].CanBeRemoved || !items[0].CanMoveFiles {
		t.Errorf("seedTime-only config with finished torrent should fire time arm: %+v", items[0])
	}
}

func TestProvider_GetItems_CleanupFlags_BothArmsNilDefaultsFalse(t *testing.T) {
	// seedConfig.Ratio = nil AND seedConfig.SeedTime = nil → neither
	// arm fires, gate stays false. Matches C# nullable-comparison
	// returning false when the right-hand side is null.
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":100,"finished":true,"uploaded_bytes":200}}
	]}`)
	def := &downloadclients.DownloadClient{Name: "RQ", RemoveCompletedDownloads: true}
	p := rqbit.New(def, newSettings(), rqbit.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"a": {}, // both fields nil
	}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanBeRemoved || items[0].CanMoveFiles {
		t.Errorf("all-nil seedConfig: %+v", items[0])
	}
}

func TestProvider_GetItems_CleanupFlags_RatioArmRequiresDivisor(t *testing.T) {
	// C# `RQBit.cs:105` divides torrent.Ratio by 1000.0 before
	// comparing — we mirror this verbatim (documented divergence).
	// proxy.go computes Ratio = UploadedBytes/ProgressBytes, so a
	// uploaded=2000, progress=100 → Ratio=20. Comparing 20/1000=0.02
	// against seedConfig.Ratio=0.01 should fire the gate.
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":100,"finished":true,"uploaded_bytes":2000}}
	]}`)
	def := &downloadclients.DownloadClient{Name: "RQ", RemoveCompletedDownloads: true}
	p := rqbit.New(def, newSettings(), rqbit.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"a": {Ratio: ptrFloat(0.01)},
	}})
	items, _ := p.GetItems(context.Background())
	if !items[0].CanBeRemoved || !items[0].CanMoveFiles {
		t.Errorf("ratio arm (20/1000 >= 0.01) should fire: %+v ratio=%v", items[0], items[0].SeedRatio)
	}
}

func TestProvider_GetItems_CleanupFlags_RatioArmDoesntFireBelowGoal(t *testing.T) {
	// uploaded=100, progress=100 → Ratio=1.0; 1.0/1000=0.001 < 0.5
	// AND no SeedTime config → both arms fail, gate stays false.
	doer := newScripted()
	doer.queue("/torrents", `{"torrents":[
		{"id":1,"info_hash":"a","name":"A","output_folder":"/dl/","stats":{"state":"live","total_bytes":100,"progress_bytes":100,"finished":true,"uploaded_bytes":100}}
	]}`)
	def := &downloadclients.DownloadClient{Name: "RQ", RemoveCompletedDownloads: true}
	p := rqbit.New(def, newSettings(), rqbit.NewProxy(doer, nil), nil)
	p.SetSeedConfigProvider(&stubSeedCfg{byHash: map[string]*downloadclients.TorrentSeedConfiguration{
		"a": {Ratio: ptrFloat(0.5)},
	}})
	items, _ := p.GetItems(context.Background())
	if items[0].CanBeRemoved || items[0].CanMoveFiles {
		t.Errorf("ratio arm should not fire (1/1000 < 0.5): %+v", items[0])
	}
}

func TestProvider_RemoveItem_DispatchesForgetOrDelete(t *testing.T) {
	t.Run("forget", func(t *testing.T) {
		doer := newScripted()
		doer.queue("/torrents/abc/forget", `{}`)
		p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
		if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "abc"}, false); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		doer := newScripted()
		doer.queue("/torrents/abc/delete", `{}`)
		p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(doer, nil), nil)
		if err := p.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "abc"}, true); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestProvider_RemoveItem_NilItem(t *testing.T) {
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(newScripted(), nil), nil)
	if err := p.RemoveItem(context.Background(), nil, false); err == nil {
		t.Fatal("err = nil, want nil-item error")
	}
}

func TestProvider_RemoveItem_EmptyID(t *testing.T) {
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(newScripted(), nil), nil)
	if err := p.RemoveItem(context.Background(), &downloadclients.Item{}, false); err == nil {
		t.Fatal("err = nil, want empty-id error")
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
			p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, s, rqbit.NewProxy(newScripted(), nil), nil)
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

func TestProvider_MarkItemAsImported_ReturnsUnsupported(t *testing.T) {
	p := rqbit.New(&downloadclients.DownloadClient{Name: "RQ"}, newSettings(), rqbit.NewProxy(newScripted(), nil), nil)
	if err := p.MarkItemAsImported(context.Background(), &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestRegistry_RegistersRQBit(t *testing.T) {
	reg := downloadclients.Default()
	if !reg.Has(rqbit.Implementation, rqbit.ConfigContract) {
		t.Errorf("registry missing (%s, %s)", rqbit.Implementation, rqbit.ConfigContract)
	}
}

type stubRemapper struct{ from, to string }

func (s stubRemapper) RemapRemoteToLocal(_ context.Context, _ string, p ospath.OsPath) (ospath.OsPath, error) {
	if p.FullPath() == s.from {
		return ospath.New(s.to), nil
	}
	return p, nil
}
