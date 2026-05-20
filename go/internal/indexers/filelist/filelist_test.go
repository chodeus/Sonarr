package filelist_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/filelist"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

type stubClient struct {
	getFn func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error)
	last  *httpclient.Request
}

func (s *stubClient) Execute(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	s.last = req
	return s.getFn(ctx, req)
}
func (s *stubClient) Get(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	s.last = req
	return s.getFn(ctx, req)
}
func (s *stubClient) Head(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.getFn(ctx, req)
}
func (s *stubClient) Post(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.getFn(ctx, req)
}
func (s *stubClient) DownloadFile(_ context.Context, _, _ string) error {
	return errors.New("not implemented")
}

// minimalFeed is one FileList torrent record — exercises every
// field the parser maps (id/name/size/leechers/seeders/imdb/
// freeleech/internal/upload_date).
const minimalFeed = `[{
  "id": "1234567",
  "name": "Show.S01E01.1080p.WEB-DL-GROUP",
  "size": 1234567890,
  "leechers": 5,
  "seeders": 42,
  "times_completed": 100,
  "comments": 0,
  "files": 1,
  "imdb": "tt0903747",
  "internal": true,
  "freeleech": false,
  "upload_date": "2025-05-19T12:00:00"
}]`

func mkResponse(req *httpclient.Request, body []byte) *httpclient.Response {
	h := httpclient.NewHeader()
	h.SetContentType("application/json; charset=utf-8")
	return httpclient.NewResponseFromBytes(req, h, body, http.StatusOK, "1.1")
}

// --- Settings ---

func TestSettings_Defaults(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	if s.BaseUrl != "https://filelist.io" {
		t.Errorf("BaseUrl = %q", s.BaseUrl)
	}
	if s.MinimumSeeders != 1 {
		t.Errorf("MinimumSeeders = %d", s.MinimumSeeders)
	}
	wantCats := []int{filelist.CategoryTVSD, filelist.CategoryTVHD, filelist.CategoryTV4K}
	if len(s.Categories) != 3 {
		t.Errorf("Categories = %v, want %v", s.Categories, wantCats)
	}
	for i, c := range wantCats {
		if s.Categories[i] != c {
			t.Errorf("Categories[%d] = %d, want %d", i, s.Categories[i], c)
		}
	}
}

func TestSettings_ValidateRequiresUsernameAndPasskey(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	r := s.Validate()
	if r.IsValid() {
		t.Error("default Settings (empty Username/Passkey) should not validate")
	}
	usernameFail, passkeyFail := false, false
	for _, f := range r.Errors {
		if f.PropertyName == "Username" {
			usernameFail = true
		}
		if f.PropertyName == "Passkey" {
			passkeyFail = true
		}
	}
	if !usernameFail || !passkeyFail {
		t.Errorf("expected Username AND Passkey failures; got %+v", r.Errors)
	}
}

func TestSettings_ValidateRequiresCategories(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	s.Username = "u"
	s.Passkey = "p"
	s.Categories = []int{}
	s.AnimeCategories = []int{}
	r := s.Validate()
	if r.IsValid() {
		t.Error("empty Categories AND AnimeCategories should fail")
	}
}

func TestSettings_ValidateAnimeCategoriesAlone(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	s.Username = "u"
	s.Passkey = "p"
	s.Categories = []int{}
	s.AnimeCategories = []int{filelist.CategoryAnime}
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("anime-only categories should validate; got %+v", r.Errors)
	}
}

func TestSettings_ValidateAcceptsValidConfig(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	s.Username = "u"
	s.Passkey = "p"
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("default + user/passkey should validate; got %+v", r.Errors)
	}
}

// --- Indexer ---

func TestIndexer_NameAndContract(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	if got := i.Name(); got != "FileList" {
		t.Errorf("Name = %q", got)
	}
	if got := i.ConfigContract(); got != "FileListSettings" {
		t.Errorf("ConfigContract = %q", got)
	}
}

func TestIndexer_DefaultDefinitionsShape(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Fatalf("len = %d", len(defs))
	}
	d := defs[0]
	if d.Implementation != "FileList" || d.ConfigContract != "FileListSettings" {
		t.Errorf("got %+v", d)
	}
}

func TestIndexer_TestUnboundFails(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("Test on unbound indexer should fail")
	}
}

func TestIndexer_FetchRecentURLAndAuth(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "alice"
	s.Passkey = "secret"
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 release; got %d", len(rels))
	}
	// URL shape: <BaseUrl>/api.php?action=latest-torrents&category=<csv>
	got := stub.last.URL.String()
	want := "https://filelist.io/api.php?action=latest-torrents&category=23,21,27"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	// Basic auth credentials threaded through.
	if stub.last.BasicAuthUser != "alice" || stub.last.BasicAuthPass != "secret" {
		t.Errorf("basic auth = (%q,%q), want (alice,secret)", stub.last.BasicAuthUser, stub.last.BasicAuthPass)
	}
}

func TestIndexer_FetchRecentParsesAllFields(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "secret"
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 release; got %d", len(rels))
	}
	r := rels[0]
	if r.Guid != "FileList-1234567" {
		t.Errorf("Guid = %q", r.Guid)
	}
	if r.Title != "Show.S01E01.1080p.WEB-DL-GROUP" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Size != 1234567890 {
		t.Errorf("Size = %d", r.Size)
	}
	// DownloadURL composes <BaseUrl>/download.php?id=<id>&passkey=<...>.
	if !strings.Contains(r.DownloadURL, "/download.php?") ||
		!strings.Contains(r.DownloadURL, "id=1234567") ||
		!strings.Contains(r.DownloadURL, "passkey=secret") {
		t.Errorf("DownloadURL = %q", r.DownloadURL)
	}
	if !strings.Contains(r.InfoURL, "/details.php?") ||
		!strings.Contains(r.InfoURL, "id=1234567") {
		t.Errorf("InfoURL = %q", r.InfoURL)
	}
	if r.Seeders == nil || *r.Seeders != 42 {
		t.Errorf("Seeders = %v, want 42", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 47 {
		t.Errorf("Peers = %v, want 47 (leechers+seeders)", r.Peers)
	}
	// IMDB-id normaliser produces `tt<7-digit>`.
	if r.ImdbID != "tt0903747" {
		t.Errorf("ImdbID = %q, want tt0903747", r.ImdbID)
	}
	// IndexerFlags = Internal (FreeLeech=false in fixture).
	if r.IndexerFlags != parser.IndexerFlagInternal {
		t.Errorf("IndexerFlags = %v, want Internal-only", r.IndexerFlags)
	}
}

func TestIndexer_FetchRecentEmptyCategoriesSkipsHTTP(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			t.Errorf("HTTP should not be called when categories empty; got %s", req.URL.String())
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	s.Categories = []int{}
	s.AnimeCategories = []int{}
	i.SetDefinition(d)
	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("empty categories should yield empty; got %d", len(rels))
	}
}

func TestIndexer_FetchRecentCategoryUnionDedupes(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	s.Categories = []int{filelist.CategoryTVHD, filelist.CategoryAnime}
	s.AnimeCategories = []int{filelist.CategoryAnime, filelist.CategoryAnimation}
	i.SetDefinition(d)

	if _, err := i.FetchRecent(context.Background()); err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	got := stub.last.URL.String()
	// 21,24 from Categories then 15 from AnimeCategories (24 deduped).
	want := "https://filelist.io/api.php?action=latest-torrents&category=21,24,15"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestIndexer_FetchRecentInvalidJSONErrors(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(`{not-json`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected JSON parse error")
	}
}

func TestIndexer_FetchSearchAlwaysEmpty(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)
	rels, err := i.FetchSearch(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("FetchSearch (J152) always returns empty; got %d", len(rels))
	}
}

func TestIndexer_FetchRecentNilClientErrors(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on nil HTTPClient")
	}
}

// --- Registry ---

func TestRegistry_ConstructorRegistered(t *testing.T) {
	t.Parallel()
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: filelist.Implementation, Contract: filelist.ConfigContract})
	if !ok {
		t.Fatal("FileList not registered in provider.Default()")
	}
	if _, ok := ctor().(*filelist.Settings); !ok {
		t.Errorf("Constructor returned %T", ctor())
	}
}
