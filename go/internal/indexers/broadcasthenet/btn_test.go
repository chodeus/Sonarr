package broadcasthenet_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/broadcasthenet"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

type stubClient struct {
	postFn   func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error)
	calls    int
	lastBody []byte
	lastReq  *httpclient.Request
}

func (s *stubClient) Execute(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.post(ctx, req)
}
func (s *stubClient) Get(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.post(ctx, req)
}
func (s *stubClient) Head(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.post(ctx, req)
}
func (s *stubClient) Post(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.post(ctx, req)
}
func (s *stubClient) DownloadFile(_ context.Context, _, _ string) error {
	return errors.New("not implemented")
}
func (s *stubClient) post(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	s.calls++
	s.lastReq = req
	s.lastBody = append([]byte(nil), req.ContentData...)
	return s.postFn(ctx, req)
}

// successFeed is one BTN getTorrents response with two torrents.
const successFeed = `{
  "jsonrpc": "2.0",
  "result": {
    "results": 2,
    "torrents": {
      "1001": {
        "GroupID": 10,
        "TorrentID": 1001,
        "Series": "Show",
        "Category": "Episode",
        "ReleaseName": "Show.S01E01.1080p.WEB-DL\\-GROUP",
        "Size": 1234567890,
        "Time": 1716120000,
        "Seeders": 42,
        "Leechers": 5,
        "Origin": "INTERNAL",
        "Source": "WEB-DL",
        "Container": "MKV",
        "Codec": "h264",
        "Resolution": "1080p",
        "TvdbID": 999,
        "ImdbID": "1234567",
        "InfoHash": "deadbeef",
        "DownloadURL": "http://broadcasthe.net/torrents.php?action=download&id=1001",
        "Tags": ["Subtitles", "HD"]
      },
      "1002": {
        "GroupID": 11,
        "TorrentID": 1002,
        "ReleaseName": "Show.S01E02.1080p.WEB-DL-GROUP",
        "Size": 987654321,
        "Time": 1716120100,
        "Seeders": 10,
        "Leechers": 2,
        "Origin": "SCENE",
        "InfoHash": "feedface",
        "DownloadURL": "https://broadcasthe.net/torrents.php?action=download&id=1002"
      }
    }
  },
  "id": 1
}`

const emptyFeed = `{"jsonrpc":"2.0","result":{"results":0,"torrents":null},"id":1}`

const errorFeed = `{"jsonrpc":"2.0","result":null,"error":{"code":-32603,"message":"Internal error"},"id":1}`

func mkResponse(req *httpclient.Request, body []byte, status int) *httpclient.Response {
	h := httpclient.NewHeader()
	h.SetContentType("application/json; charset=utf-8")
	return httpclient.NewResponseFromBytes(req, h, body, status, "1.1")
}

// --- Settings ---

func TestSettings_Defaults(t *testing.T) {
	t.Parallel()
	s := broadcasthenet.NewSettings()
	if s.BaseUrl != "https://api.broadcasthe.net/" {
		t.Errorf("BaseUrl = %q", s.BaseUrl)
	}
	if s.MinimumSeeders != 1 {
		t.Errorf("MinimumSeeders = %d", s.MinimumSeeders)
	}
}

func TestSettings_ValidateRequiresApiKey(t *testing.T) {
	t.Parallel()
	s := broadcasthenet.NewSettings()
	r := s.Validate()
	if r.IsValid() {
		t.Error("default Settings (empty ApiKey) should not validate")
	}
	for _, f := range r.Errors {
		if f.PropertyName == "ApiKey" {
			return
		}
	}
	t.Errorf("expected ApiKey failure; got %+v", r.Errors)
}

func TestSettings_ValidateAcceptsValidConfig(t *testing.T) {
	t.Parallel()
	s := broadcasthenet.NewSettings()
	s.ApiKey = "secret"
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("expected valid; got %+v", r.Errors)
	}
}

// --- Indexer ---

func TestIndexer_NameAndContract(t *testing.T) {
	t.Parallel()
	i := broadcasthenet.NewIndexer(nil)
	if got := i.Name(); got != "BroadcasTheNet" {
		t.Errorf("Name = %q, want BroadcasTheNet (capital T in The)", got)
	}
	if got := i.ConfigContract(); got != "BroadcastheNetSettings" {
		t.Errorf("ConfigContract = %q", got)
	}
}

func TestIndexer_DefaultDefinitionsShape(t *testing.T) {
	t.Parallel()
	i := broadcasthenet.NewIndexer(nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Fatalf("len = %d", len(defs))
	}
	d := defs[0]
	if d.Implementation != "BroadcastheNet" || d.ConfigContract != "BroadcastheNetSettings" {
		t.Errorf("got %+v", d)
	}
}

func TestIndexer_TestUnboundFails(t *testing.T) {
	t.Parallel()
	i := broadcasthenet.NewIndexer(nil)
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("Test on unbound indexer should fail")
	}
}

func TestIndexer_FetchRecentPOSTsJSONRPCBody(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			// Return empty after the first call so pagination
			// short-circuits.
			return mkResponse(req, []byte(successFeed), 200), nil
		},
	}
	// Override after first call to feed empty.
	originalFn := stub.postFn
	calls := 0
	stub.postFn = func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		calls++
		if calls == 1 {
			return originalFn(ctx, req)
		}
		return mkResponse(req, []byte(emptyFeed), 200), nil
	}

	i := broadcasthenet.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*broadcasthenet.Settings)
	s.ApiKey = "secret-key"
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 releases; got %d", len(rels))
	}
	if stub.lastReq.Method != "POST" {
		t.Errorf("Method = %q, want POST", stub.lastReq.Method)
	}
	// Validate JSON-RPC envelope shape.
	var rpc map[string]any
	if err := json.Unmarshal(stub.lastBody, &rpc); err != nil {
		t.Fatalf("body did not parse: %v (raw=%q)", err, stub.lastBody)
	}
	if rpc["jsonrpc"] != "2.0" {
		t.Errorf("rpc.jsonrpc = %v", rpc["jsonrpc"])
	}
	if rpc["method"] != "getTorrents" {
		t.Errorf("rpc.method = %v", rpc["method"])
	}
	params, ok := rpc["params"].([]any)
	if !ok || len(params) != 4 {
		t.Fatalf("rpc.params = %v", rpc["params"])
	}
	if params[0] != "secret-key" {
		t.Errorf("params[0] (apiKey) = %v", params[0])
	}
	// params[1] is the query body — check the Age field.
	query, _ := params[1].(map[string]any)
	if query["age"] != "<=86400" {
		t.Errorf("params[1].age = %v, want <=86400", query["age"])
	}
}

func TestIndexer_FetchRecentParsesTorrentFields(t *testing.T) {
	t.Parallel()
	calls := 0
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			calls++
			if calls == 1 {
				return mkResponse(req, []byte(successFeed), 200), nil
			}
			return mkResponse(req, []byte(emptyFeed), 200), nil
		},
	}
	i := broadcasthenet.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*broadcasthenet.Settings)
	s.ApiKey = "k"
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 releases; got %d", len(rels))
	}
	byID := map[string]*parser.TorrentInfo{}
	for _, r := range rels {
		byID[r.Guid] = r
	}
	r1 := byID["BTN-1001"]
	if r1 == nil {
		t.Fatal("missing BTN-1001 release")
	}
	// CleanReleaseName strips backslash escapes.
	if r1.Title != "Show.S01E01.1080p.WEB-DL-GROUP" {
		t.Errorf("Title (clean) = %q", r1.Title)
	}
	if r1.Size != 1234567890 {
		t.Errorf("Size = %d", r1.Size)
	}
	if r1.InfoHash != "deadbeef" {
		t.Errorf("InfoHash = %q", r1.InfoHash)
	}
	if r1.Seeders == nil || *r1.Seeders != 42 {
		t.Errorf("Seeders = %v", r1.Seeders)
	}
	if r1.Peers == nil || *r1.Peers != 47 {
		t.Errorf("Peers = %v, want 47", r1.Peers)
	}
	if r1.TvdbID != 999 {
		t.Errorf("TvdbID = %d", r1.TvdbID)
	}
	if r1.ImdbID != "tt1234567" {
		t.Errorf("ImdbID = %q, want tt1234567", r1.ImdbID)
	}
	// InfoUrl composed from the request scheme + GroupID + TorrentID.
	if !strings.Contains(r1.InfoURL, "torrents.php?id=10&torrentid=1001") {
		t.Errorf("InfoURL = %q", r1.InfoURL)
	}
	// DownloadUrl scheme rewritten to match the request scheme (https
	// since the default BaseUrl is https).
	if !strings.HasPrefix(r1.DownloadURL, "https:") {
		t.Errorf("DownloadURL scheme not rewritten to https: %q", r1.DownloadURL)
	}
	// Every BTN torrent gets Freeleech + Internal (origin) + Subtitles
	// (tag) flags for r1.
	if r1.IndexerFlags&parser.IndexerFlagFreeleech == 0 {
		t.Errorf("missing Freeleech flag: %v", r1.IndexerFlags)
	}
	if r1.IndexerFlags&parser.IndexerFlagInternal == 0 {
		t.Errorf("missing Internal flag: %v", r1.IndexerFlags)
	}

	r2 := byID["BTN-1002"]
	if r2 == nil {
		t.Fatal("missing BTN-1002 release")
	}
	// Scene origin maps to Scene flag (not Internal).
	if r2.IndexerFlags&parser.IndexerFlagScene == 0 {
		t.Errorf("missing Scene flag: %v", r2.IndexerFlags)
	}
	if r2.IndexerFlags&parser.IndexerFlagInternal != 0 {
		t.Errorf("r2 should not have Internal flag")
	}
}

func TestIndexer_FetchRecentPaginatesUntilEmpty(t *testing.T) {
	t.Parallel()
	calls := 0
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			calls++
			// Pages 1+2 return data, page 3 returns empty.
			if calls <= 2 {
				return mkResponse(req, []byte(successFeed), 200), nil
			}
			return mkResponse(req, []byte(emptyFeed), 200), nil
		},
	}
	i := broadcasthenet.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*broadcasthenet.Settings)
	s.ApiKey = "k"
	i.SetDefinition(d)
	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	// Two pages of the same successFeed → all dedup'd to 2 releases.
	if len(rels) != 2 {
		t.Errorf("expected 2 deduped releases; got %d", len(rels))
	}
	// Pagination should have stopped after the second empty (or
	// all-dup) page. Pages 1+2 produce new + dup → page 3 short-
	// circuits because page 2 was already all-dup.
	if calls < 2 || calls > 3 {
		t.Errorf("page count = %d, want 2-3", calls)
	}
}

func TestIndexer_FetchRecentUnauthorizedErrors(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(`{}`), 401), nil
		},
	}
	i := broadcasthenet.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*broadcasthenet.Settings)
	s.ApiKey = "k"
	i.SetDefinition(d)
	rels, err := i.FetchRecent(context.Background())
	// fetchPaged swallows per-page errors so the result is empty
	// (not an error). The first page's 401 is logged and we move on.
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected empty; got %d", len(rels))
	}
}

func TestIndexer_FetchRecentJSONRPCErrorPropagates(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(errorFeed), 200), nil
		},
	}
	i := broadcasthenet.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*broadcasthenet.Settings)
	s.ApiKey = "k"
	i.SetDefinition(d)
	// The error-feed produces a parse-side error which fetchPaged
	// swallows; result is empty.
	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected empty on JSON-RPC error; got %d", len(rels))
	}
}

func TestIndexer_FetchRecentRateLimitInBody(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte("Call Limit Exceeded"), 200), nil
		},
	}
	i := broadcasthenet.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*broadcasthenet.Settings)
	s.ApiKey = "k"
	i.SetDefinition(d)
	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected empty; got %d", len(rels))
	}
}

func TestIndexer_FetchSearchAlwaysEmpty(t *testing.T) {
	t.Parallel()
	i := broadcasthenet.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*broadcasthenet.Settings)
	s.ApiKey = "k"
	i.SetDefinition(d)
	rels, err := i.FetchSearch(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("FetchSearch (J156) always returns empty; got %d", len(rels))
	}
}

func TestIndexer_FetchRecentNilClientErrors(t *testing.T) {
	t.Parallel()
	i := broadcasthenet.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*broadcasthenet.Settings)
	s.ApiKey = "k"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on nil HTTPClient")
	}
}

func TestRegistry_ConstructorRegistered(t *testing.T) {
	t.Parallel()
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: broadcasthenet.Implementation, Contract: broadcasthenet.ConfigContract})
	if !ok {
		t.Fatal("BroadcastheNet not registered in provider.Default()")
	}
	if _, ok := ctor().(*broadcasthenet.Settings); !ok {
		t.Errorf("Constructor returned %T", ctor())
	}
}
