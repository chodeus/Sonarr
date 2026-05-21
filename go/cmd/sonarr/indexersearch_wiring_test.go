package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/indexers/broadcasthenet"
	"sonarr.tv/sonarr/internal/provider"
)

// stubRssStore captures Get / Update calls. Get returns whatever the
// caller seeded into `getReturn`; Update appends to `updates`. nil
// fields mean "no override".
type stubRssStore struct {
	mu        sync.Mutex
	getReturn map[int]string
	getErr    map[int]error
	updateErr map[int]error
	updates   []rssStoreCall
	gets      []rssStoreCall
}

type rssStoreCall struct {
	providerID int
	payload    string
}

func newStubRssStore() *stubRssStore {
	return &stubRssStore{getReturn: map[int]string{}, getErr: map[int]error{}, updateErr: map[int]error{}}
}

func (s *stubRssStore) GetLastRssSyncReleaseInfo(_ context.Context, providerID int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets = append(s.gets, rssStoreCall{providerID: providerID})
	if err, ok := s.getErr[providerID]; ok {
		return "", err
	}
	return s.getReturn[providerID], nil
}

func (s *stubRssStore) UpdateRssSyncStatus(_ context.Context, providerID int, releaseInfoJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, rssStoreCall{providerID: providerID, payload: releaseInfoJSON})
	if err, ok := s.updateErr[providerID]; ok {
		return err
	}
	return nil
}

// btnHTTPStub returns the given response body on every POST. Tracks
// call counts so callers can verify pagination short-circuited.
type btnHTTPStub struct {
	mu     sync.Mutex
	calls  int
	bodies [][]byte
	// Per-call response bodies, indexed by call number (1-based). Falls
	// back to the empty BTN feed if calls exceed the slice length.
	responses []string
}

func (b *btnHTTPStub) Execute(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return b.respond(req)
}
func (b *btnHTTPStub) Get(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return b.respond(req)
}
func (b *btnHTTPStub) Head(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return b.respond(req)
}
func (b *btnHTTPStub) Post(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return b.respond(req)
}
func (b *btnHTTPStub) DownloadFile(_ context.Context, _, _ string) error {
	return errors.New("not implemented")
}

func (b *btnHTTPStub) respond(req *httpclient.Request) (*httpclient.Response, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	b.bodies = append(b.bodies, append([]byte(nil), req.ContentData...))
	body := emptyBTNFeed
	if b.calls-1 < len(b.responses) {
		body = b.responses[b.calls-1]
	}
	h := httpclient.NewHeader()
	h.SetContentType("application/json; charset=utf-8")
	return httpclient.NewResponseFromBytes(req, h, []byte(body), 200, "1.1"), nil
}

const oneTorrentBTNFeed = `{
  "jsonrpc": "2.0",
  "result": {
    "results": 1,
    "torrents": {
      "7777": {
        "GroupID": 1,
        "TorrentID": 7777,
        "Series": "Show",
        "Category": "Episode",
        "ReleaseName": "Show.S01E01",
        "Size": 100,
        "Time": 1716120000,
        "Seeders": 1,
        "Leechers": 0,
        "Origin": "INTERNAL",
        "DownloadURL": "https://broadcasthe.net/torrents.php?action=download&id=7777"
      }
    }
  },
  "id": 1
}`

const emptyBTNFeed = `{"jsonrpc":"2.0","result":{"results":0,"torrents":null},"id":1}`

// buildBTNRow returns a definition + row pair suitable for buildAdapter.
// The row has ID=42 + Implementation="BroadcastheNet"; the definition
// carries a Settings with the ApiKey filled in.
func buildBTNRow(t *testing.T) (*indexers.Indexer, *provider.Definition) {
	t.Helper()
	enabled := true
	settings := broadcasthenet.NewSettings()
	settings.ApiKey = "test-key"
	row := &indexers.Indexer{
		ID:             42,
		Name:           "BTN-test",
		Implementation: "BroadcastheNet",
		EnableRss:      &enabled,
	}
	def := &provider.Definition{
		ID:             42,
		Name:           "BTN-test",
		Implementation: "BroadcastheNet",
		Enable:         true,
		Settings:       settings,
	}
	return row, def
}

func newTestSource(rss rssWatermarkStore, client httpclient.Client) *dbIndexerSource {
	return &dbIndexerSource{
		httpClient: client,
		rssStore:   rss,
		log:        slog.New(slog.DiscardHandler),
	}
}

// J178: FetchRecentFunc reads the persisted watermark JSON on every
// invocation and seeds the BTN indexer's LastRecentTorrentID — the
// in-indexer tier walk (J158) then uses it to build the id-watermark
// tier 1 query.
func TestBuildAdapter_BroadcastheNet_SeedsWatermarkBeforeFetch(t *testing.T) {
	t.Parallel()
	rss := newStubRssStore()
	rss.getReturn[42] = `{"guid":"BTN-555","publishDate":"2026-05-01T00:00:00Z"}`
	src := newTestSource(rss, &btnHTTPStub{responses: []string{emptyBTNFeed}})

	row, def := buildBTNRow(t)
	adapter, err := src.buildAdapter(row, def)
	if err != nil {
		t.Fatalf("buildAdapter: %v", err)
	}
	if _, err := adapter.FetchRecent(t.Context()); err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}

	rss.mu.Lock()
	defer rss.mu.Unlock()
	if len(rss.gets) != 1 || rss.gets[0].providerID != 42 {
		t.Errorf("Get call log = %+v, want 1 call for providerID=42", rss.gets)
	}
	// Validate the in-indexer watermark advanced by inspecting the
	// outbound request body. Tier 1 with lastID=555 emits
	// {"id":">=455"} (lastID - 100 = 455) — that confirms
	// SetLastRecentTorrentID fired BEFORE FetchRecent walked its tiers.
	stub := src.httpClient.(*btnHTTPStub)
	if stub.calls == 0 {
		t.Fatal("no HTTP calls observed")
	}
	var rpc map[string]any
	if err := json.Unmarshal(stub.bodies[0], &rpc); err != nil {
		t.Fatalf("first body unmarshal: %v", err)
	}
	params, _ := rpc["params"].([]any)
	if len(params) < 2 {
		t.Fatalf("params shape: %+v", rpc)
	}
	q, _ := params[1].(map[string]any)
	if q["id"] != ">=455" {
		t.Errorf("first query id = %v, want >=455 (i.e. 555-100)", q["id"])
	}
}

// J178: A successful non-empty FetchRecent writes the newest
// release's embedded ReleaseInfo back to the watermark store. The
// resulting JSON must round-trip through ParseLastRecentTorrentID to
// the same int the in-indexer fetch surfaced — that's the contract
// the next-boot seed path depends on.
func TestBuildAdapter_BroadcastheNet_CapturesWatermarkAfterFetch(t *testing.T) {
	t.Parallel()
	rss := newStubRssStore()
	// First call returns one torrent (TorrentID=7777); second returns
	// empty so pagination short-circuits.
	src := newTestSource(rss, &btnHTTPStub{responses: []string{oneTorrentBTNFeed, emptyBTNFeed}})

	row, def := buildBTNRow(t)
	adapter, err := src.buildAdapter(row, def)
	if err != nil {
		t.Fatalf("buildAdapter: %v", err)
	}
	rs, err := adapter.FetchRecent(t.Context())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("len(releases) = %d, want 1", len(rs))
	}

	rss.mu.Lock()
	defer rss.mu.Unlock()
	if len(rss.updates) != 1 {
		t.Fatalf("Update call log = %+v, want 1 call", rss.updates)
	}
	if rss.updates[0].providerID != 42 {
		t.Errorf("Update providerID = %d, want 42", rss.updates[0].providerID)
	}
	// Round-trip check: the persisted JSON parses back to 7777, which
	// is what the next-boot seed path will read.
	if id := broadcasthenet.ParseLastRecentTorrentID(rss.updates[0].payload); id != 7777 {
		t.Errorf("captured watermark id = %d, want 7777 (payload=%q)", id, rss.updates[0].payload)
	}
}

// J178: empty fetch result skips the Update call — mirrors C#
// `if (isRecent && !releases.Empty())` guard at HttpIndexerBase.cs:207.
func TestBuildAdapter_BroadcastheNet_EmptyFetchSkipsUpdate(t *testing.T) {
	t.Parallel()
	rss := newStubRssStore()
	src := newTestSource(rss, &btnHTTPStub{responses: []string{emptyBTNFeed}})

	row, def := buildBTNRow(t)
	adapter, err := src.buildAdapter(row, def)
	if err != nil {
		t.Fatalf("buildAdapter: %v", err)
	}
	if _, err := adapter.FetchRecent(t.Context()); err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}

	rss.mu.Lock()
	defer rss.mu.Unlock()
	if len(rss.updates) != 0 {
		t.Errorf("Update calls on empty fetch = %+v, want none", rss.updates)
	}
	// Seed read still fires.
	if len(rss.gets) != 1 {
		t.Errorf("Get calls on empty fetch = %d, want 1", len(rss.gets))
	}
}

// J178: nil rssStore (test wiring without an indexerstatus.Service)
// skips both the seed read and the capture write — FetchRecent must
// still succeed, just without watermark management.
func TestBuildAdapter_BroadcastheNet_NilRssStoreSkipsBothPaths(t *testing.T) {
	t.Parallel()
	src := newTestSource(nil, &btnHTTPStub{responses: []string{oneTorrentBTNFeed, emptyBTNFeed}})

	row, def := buildBTNRow(t)
	adapter, err := src.buildAdapter(row, def)
	if err != nil {
		t.Fatalf("buildAdapter: %v", err)
	}
	rs, err := adapter.FetchRecent(t.Context())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rs) != 1 {
		t.Errorf("len(releases) = %d, want 1", len(rs))
	}
	// No nil-deref panics is enough — the buildAdapter exit cleanly.
}

// J178: a Get error degrades to a logged warning + no seeding; the
// fetch path continues with the pre-existing (zero) watermark.
func TestBuildAdapter_BroadcastheNet_GetErrorDoesNotFailFetch(t *testing.T) {
	t.Parallel()
	rss := newStubRssStore()
	rss.getErr[42] = errors.New("repo borked")
	src := newTestSource(rss, &btnHTTPStub{responses: []string{emptyBTNFeed}})

	row, def := buildBTNRow(t)
	adapter, err := src.buildAdapter(row, def)
	if err != nil {
		t.Fatalf("buildAdapter: %v", err)
	}
	if _, err := adapter.FetchRecent(t.Context()); err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	// Verify the outbound first-tier query is the AGE tier (no id-tier
	// — confirms the Get error was swallowed and SetLastRecentTorrentID
	// was NOT called with a stale value).
	stub := src.httpClient.(*btnHTTPStub)
	if stub.calls == 0 {
		t.Fatal("no HTTP calls observed")
	}
	var rpc map[string]any
	if err := json.Unmarshal(stub.bodies[0], &rpc); err != nil {
		t.Fatalf("first body unmarshal: %v", err)
	}
	params, _ := rpc["params"].([]any)
	q, _ := params[1].(map[string]any)
	if q["age"] != "<=86400" {
		t.Errorf("first query (after Get error) = %v, want age=<=86400", q)
	}
}
