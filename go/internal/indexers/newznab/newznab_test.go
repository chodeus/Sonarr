package newznab

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/provider"
	"sonarr.tv/sonarr/internal/series"
)

// (helper removed — newTestIndexer ergonomics are inlined in each test below
// because each test wants its own response-body matrix.)

func TestNewznab_NameAndConfigContract(t *testing.T) {
	i := NewIndexer(nil, nil)
	if i.Name() != "Newznab" {
		t.Errorf("Name = %q", i.Name())
	}
	if i.ConfigContract() != "NewznabSettings" {
		t.Errorf("ConfigContract = %q", i.ConfigContract())
	}
}

func TestNewznab_MessageNil(t *testing.T) {
	i := NewIndexer(nil, nil)
	if i.Message() != nil {
		t.Errorf("Message should be nil for the class itself")
	}
}

func TestNewznab_DefaultDefinitions(t *testing.T) {
	i := NewIndexer(nil, nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 9 {
		t.Errorf("DefaultDefinitions count = %d, want 9", len(defs))
	}
	for _, d := range defs {
		if d.Implementation != "Newznab" {
			t.Errorf("def %q wrong impl: %q", d.Name, d.Implementation)
		}
		if d.ConfigContract != "NewznabSettings" {
			t.Errorf("def %q wrong contract: %q", d.Name, d.ConfigContract)
		}
		if d.Enable {
			t.Errorf("def %q should default Enable=false", d.Name)
		}
		s, ok := d.Settings.(*Settings)
		if !ok {
			t.Errorf("def %q Settings type = %T", d.Name, d.Settings)
			continue
		}
		if s.BaseUrl == "" {
			t.Errorf("def %q has empty BaseUrl", d.Name)
		}
	}
	// Spot-check the special-cased entries.
	for _, d := range defs {
		if d.Name == "Tabula Rasa" {
			s := d.Settings.(*Settings)
			if s.ApiPath != "/api/v1/api" {
				t.Errorf("Tabula Rasa ApiPath = %q, want /api/v1/api", s.ApiPath)
			}
		}
		if d.Name == "NZBFinder.ws" {
			s := d.Settings.(*Settings)
			if len(s.Categories) != 3 || s.Categories[2] != 5045 {
				t.Errorf("NZBFinder.ws categories = %v", s.Categories)
			}
		}
	}
}

func TestNewznab_DefinitionAndSetDefinition(t *testing.T) {
	i := NewIndexer(nil, nil)
	if i.Definition() != nil {
		t.Errorf("unbound: Definition should be nil")
	}
	d := &provider.Definition{Name: "X"}
	i.SetDefinition(d)
	if i.Definition() != d {
		t.Errorf("SetDefinition: got %p, want %p", i.Definition(), d)
	}
}

func TestNewznab_Test_NoDefinitionFails(t *testing.T) {
	i := NewIndexer(nil, nil)
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("expected invalid when no Definition")
	}
}

func TestNewznab_Test_InvalidSettingsFails(t *testing.T) {
	i := NewIndexer(nil, nil)
	settings := NewSettings()
	settings.BaseUrl = "" // invalid
	i.SetDefinition(&provider.Definition{Settings: settings})
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("expected invalid settings")
	}
	if !hasFailure(r, "BaseUrl") {
		t.Errorf("expected BaseUrl failure: %v", r)
	}
}

func TestNewznab_Test_NoCapsProviderWarns(t *testing.T) {
	// Settings valid but no caps provider — surfaces as a warning, not a
	// hard error. Test passes IsValid because warnings don't block.
	settings := NewSettings()
	settings.BaseUrl = "http://nzbs2.org"
	i := NewIndexer(nil, nil)
	i.SetDefinition(&provider.Definition{Settings: settings})
	r := i.Test(context.Background())
	if !r.IsValid() {
		t.Errorf("expected valid (warnings only): %v", r)
	}
	if !r.HasWarnings() {
		t.Error("expected warning")
	}
}

func TestNewznab_Test_CapsValidationGate(t *testing.T) {
	body, _ := os.ReadFile(filepath.Join("testdata", "caps.xml"))
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		return mkResponse(t, req, body), nil
	}}
	caps := NewCapabilitiesProvider(stub)

	settings := NewSettings()
	settings.BaseUrl = "http://nzbs2.org"
	i := NewIndexer(stub, caps)
	i.SetDefinition(&provider.Definition{Settings: settings})
	r := i.Test(context.Background())
	// caps.xml advertises search + tv-search both available with no
	// supportedParams attr — defaults preserve {q,rid,season,ep} for
	// tv-search. validateSearchModeSupported sees "q" in basic search →
	// returns true.
	if !r.IsValid() {
		t.Errorf("caps fixture should validate: %v", r)
	}
}

func TestNewznab_Test_CapsRejectedWhenNoSearchMode(t *testing.T) {
	// XML body that advertises NEITHER basic search nor a workable tv-search
	// → Test should fail with "no compatible search parameters".
	xml := `<caps><searching>
		<search available="no"/>
		<tv-search available="yes" supportedParams="title"/>
	</searching></caps>`
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		return mkResponse(t, req, []byte(xml)), nil
	}}
	caps := NewCapabilitiesProvider(stub)

	settings := NewSettings()
	settings.BaseUrl = "http://nzbs2.org"
	i := NewIndexer(stub, caps)
	i.SetDefinition(&provider.Definition{Settings: settings})
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Errorf("expected invalid for caps without compatible params: %v", r)
	}
}

func TestNewznab_RequestAction_NewznabCategoriesNoCaps(t *testing.T) {
	settings := NewSettings()
	settings.BaseUrl = "http://nzbs2.org"
	i := NewIndexer(nil, nil)
	i.SetDefinition(&provider.Definition{Settings: settings})

	// No caps provider → returns empty options, no error.
	got, err := i.RequestAction("newznabCategories", nil)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("RequestAction returned %T", got)
	}
	opts, ok := m["options"].([]any)
	if !ok || len(opts) != 0 {
		t.Errorf("expected empty options, got %v", m["options"])
	}
}

func TestNewznab_RequestAction_NewznabCategoriesFromCaps(t *testing.T) {
	body, _ := os.ReadFile(filepath.Join("testdata", "caps.xml"))
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		return mkResponse(t, req, body), nil
	}}
	caps := NewCapabilitiesProvider(stub)

	settings := NewSettings()
	settings.BaseUrl = "http://nzbs2.org"
	i := NewIndexer(stub, caps)
	i.SetDefinition(&provider.Definition{Settings: settings})

	got, err := i.RequestAction("newznabCategories", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	opts := m["options"].([]map[string]any)
	if len(opts) == 0 {
		t.Error("expected options from caps")
	}
	// Sanity-check one parent + one subcat from the fixture.
	sawParent, sawSub := false, false
	for _, o := range opts {
		if o["value"] == 5000 && o["name"] == "TV" {
			sawParent = true
		}
		if o["value"] == 5040 && o["name"] == "TV - HD" {
			sawSub = true
		}
	}
	if !sawParent {
		t.Error("missing TV parent category")
	}
	if !sawSub {
		t.Error("missing TV - HD subcategory")
	}
}

func TestNewznab_RequestAction_UnknownStageReturnsNil(t *testing.T) {
	i := NewIndexer(nil, nil)
	got, err := i.RequestAction("somethingElse", nil)
	if err != nil {
		t.Errorf("unknown stage should not error: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestNewznab_FetchRecent_NoBindingFails(t *testing.T) {
	i := NewIndexer(nil, nil)
	_, err := i.FetchRecent(context.Background())
	if err == nil {
		t.Fatal("expected error for unbound indexer")
	}
}

func TestNewznab_FetchRecent_HappyPath(t *testing.T) {
	feedBody, _ := os.ReadFile(filepath.Join("testdata", "newznab_nzb_su.xml"))
	capsBody, _ := os.ReadFile(filepath.Join("testdata", "caps.xml"))

	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		if strings.Contains(req.URL.String(), "?t=caps") {
			return mkResponse(t, req, capsBody), nil
		}
		return mkResponse(t, req, feedBody), nil
	}}
	caps := NewCapabilitiesProvider(stub)

	settings := NewSettings()
	settings.BaseUrl = "http://nzb.example/"
	settings.ApiKey = "key"
	i := NewIndexer(stub, caps)
	i.SetDefinition(&provider.Definition{
		ID:             7,
		Name:           "Test",
		Implementation: "Newznab",
		ConfigContract: "NewznabSettings",
		Settings:       settings,
	})

	releases, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) == 0 {
		t.Errorf("expected releases, got none")
	}
	if len(releases) != 100 {
		// The feed has 100 unique items keyed by guid; deduping should
		// retain all of them.
		t.Errorf("expected 100 releases, got %d", len(releases))
	}
	if releases[0].IndexerID != 7 {
		t.Errorf("IndexerID = %d, want 7", releases[0].IndexerID)
	}
	if releases[0].Indexer != "Test" {
		t.Errorf("Indexer name = %q", releases[0].Indexer)
	}
}

func TestNewznab_FetchRecent_DedupsByGuid(t *testing.T) {
	// Feed body with two duplicate guids should yield one release.
	body := []byte(`<?xml version="1.0"?>
		<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
		<item><title>A</title><guid>same</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="100" type="application/x-nzb"/></item>
		<item><title>B</title><guid>same</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="y" length="100" type="application/x-nzb"/></item>
	</channel></rss>`)
	capsBody, _ := os.ReadFile(filepath.Join("testdata", "caps.xml"))

	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		if strings.Contains(req.URL.String(), "?t=caps") {
			return mkResponse(t, req, capsBody), nil
		}
		return mkResponse(t, req, body), nil
	}}
	caps := NewCapabilitiesProvider(stub)

	settings := NewSettings()
	settings.BaseUrl = "http://x"
	i := NewIndexer(stub, caps)
	i.SetDefinition(&provider.Definition{Settings: settings})

	releases, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 {
		t.Errorf("expected 1 release after dedup, got %d", len(releases))
	}
}

func TestNewznab_FetchSearch_HappyPath(t *testing.T) {
	feedBody, _ := os.ReadFile(filepath.Join("testdata", "newznab_nzb_su.xml"))
	capsBody, _ := os.ReadFile(filepath.Join("testdata", "caps.xml"))

	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		if strings.Contains(req.URL.String(), "?t=caps") {
			return mkResponse(t, req, capsBody), nil
		}
		return mkResponse(t, req, feedBody), nil
	}}
	caps := NewCapabilitiesProvider(stub)

	settings := NewSettings()
	settings.BaseUrl = "http://x"
	i := NewIndexer(stub, caps)
	i.SetDefinition(&provider.Definition{Settings: settings})

	criteria := &indexersearch.SingleEpisodeCriteria{
		CriteriaBase: indexersearch.CriteriaBase{
			Series:      &series.Series{Title: "Show", TvRageID: 10},
			SceneTitles: []string{"Show"},
		},
		SeasonNumber:  1,
		EpisodeNumber: 1,
	}
	releases, err := i.FetchSearch(context.Background(), criteria)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) == 0 {
		t.Errorf("expected releases from search, got none")
	}
}

func TestNewznab_FetchSearch_FetchErrorReturnsAccumulated(t *testing.T) {
	// First call (caps) succeeds; subsequent feed calls fail. The fetch
	// loop swallows per-page errors and returns whatever was collected.
	capsBody, _ := os.ReadFile(filepath.Join("testdata", "caps.xml"))
	calls := 0
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		calls++
		if strings.Contains(req.URL.String(), "?t=caps") {
			return mkResponse(t, req, capsBody), nil
		}
		return nil, errors.New("dial failed")
	}}
	caps := NewCapabilitiesProvider(stub)

	settings := NewSettings()
	settings.BaseUrl = "http://x"
	i := NewIndexer(stub, caps)
	i.SetDefinition(&provider.Definition{Settings: settings})

	criteria := &indexersearch.SingleEpisodeCriteria{
		CriteriaBase: indexersearch.CriteriaBase{
			Series:      &series.Series{Title: "Show", TvRageID: 10},
			SceneTitles: []string{"Show"},
		},
		SeasonNumber:  1,
		EpisodeNumber: 1,
	}
	releases, err := i.FetchSearch(context.Background(), criteria)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Errorf("expected 0 releases when all feed calls fail, got %d", len(releases))
	}
	if calls < 2 {
		t.Errorf("expected at least 2 attempted calls, got %d", calls)
	}
}

func TestNewznab_validateSearchModeSupported(t *testing.T) {
	cases := []struct {
		name string
		caps *Capabilities
		want bool
	}{
		{
			name: "basic search supports q",
			caps: &Capabilities{SupportedSearchParameters: []string{"q"}},
			want: true,
		},
		{
			name: "tv-search has q + season + ep",
			caps: &Capabilities{SupportedTvSearchParameters: []string{"q", "season", "ep"}},
			want: true,
		},
		{
			name: "tv-search has tvdbid + season + ep",
			caps: &Capabilities{SupportedTvSearchParameters: []string{"tvdbid", "season", "ep"}},
			want: true,
		},
		{
			name: "tv-search has rid only (no season/ep)",
			caps: &Capabilities{SupportedTvSearchParameters: []string{"rid"}},
			want: false,
		},
		{
			name: "tv-search has season + ep but no q/id",
			caps: &Capabilities{SupportedTvSearchParameters: []string{"season", "ep"}},
			want: false,
		},
		{
			name: "no params at all",
			caps: &Capabilities{},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validateSearchModeSupported(c.caps)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// Provider interface assertion + registry constructor tests.

func TestNewznab_ImplementsProviderInterface(t *testing.T) {
	var _ provider.Provider = (*Indexer)(nil)
}

func TestNewznab_RegistryConstructorWired(t *testing.T) {
	// init() should have replaced the nil constructor seeded by Default()
	// with the real Constructor function.
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: "Newznab", Contract: "NewznabSettings"})
	if !ok {
		t.Fatal("Newznab not registered in provider.Default()")
	}
	if ctor == nil {
		t.Fatal("Newznab constructor is nil after init()")
	}
	got := ctor()
	if _, ok := got.(*Settings); !ok {
		t.Errorf("Constructor returned %T, want *Settings", got)
	}
}

// mkResponse helper override for status codes (defaults to 200).
func mkResponseWithStatus(t *testing.T, req *httpclient.Request, status int, body []byte) *httpclient.Response {
	t.Helper()
	h := httpclient.NewHeader()
	h.SetContentType("application/xml")
	return httpclient.NewResponseFromBytes(req, h, body, status, "1.1")
}

func TestNewznab_FetchRecent_HTTPClientErrorPropagatedThroughFetch(t *testing.T) {
	// httpclient.Client.Get returning non-nil error → per-page skip,
	// but the fetch as a whole still returns nil error (it's accumulated).
	capsBody, _ := os.ReadFile(filepath.Join("testdata", "caps.xml"))
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		if strings.Contains(req.URL.String(), "?t=caps") {
			return mkResponse(t, req, capsBody), nil
		}
		return mkResponseWithStatus(t, req, http.StatusInternalServerError, nil), errors.New("HTTP 500")
	}}
	caps := NewCapabilitiesProvider(stub)

	settings := NewSettings()
	settings.BaseUrl = "http://x"
	i := NewIndexer(stub, caps)
	i.SetDefinition(&provider.Definition{Settings: settings})

	releases, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Errorf("expected 0 releases on transport error, got %d", len(releases))
	}
}
