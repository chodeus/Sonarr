package newznab

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/httpclient"
)

// stubClient is a tiny test double for httpclient.Client.
type stubClient struct {
	getFn func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error)
}

func (s *stubClient) Execute(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.getFn(ctx, req)
}
func (s *stubClient) Get(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
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

func mkResponse(t *testing.T, req *httpclient.Request, body []byte) *httpclient.Response {
	t.Helper()
	h := httpclient.NewHeader()
	h.SetContentType("application/xml; charset=utf-8")
	return httpclient.NewResponseFromBytes(req, h, body, http.StatusOK, "1.1")
}

// TestParseCapabilities_FixtureRoundTrip verifies the parser against the
// canonical caps fixture copied from the C# tree at
// src/NzbDrone.Core.Test/Files/Indexers/Newznab/newznab_caps.xml.
func TestParseCapabilities_FixtureRoundTrip(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "caps.xml"))
	if err != nil {
		t.Fatal(err)
	}
	caps, err := ParseCapabilities(body)
	if err != nil {
		t.Fatal(err)
	}

	// Limits 25 / 60 from the fixture.
	if caps.DefaultPageSize != 25 {
		t.Errorf("DefaultPageSize = %d want 25", caps.DefaultPageSize)
	}
	if caps.MaxPageSize != 60 {
		t.Errorf("MaxPageSize = %d want 60", caps.MaxPageSize)
	}

	// Search available but no supportedParams attr → defaults retained.
	if !equalStrings(caps.SupportedSearchParameters, []string{"q"}) {
		t.Errorf("SupportedSearchParameters = %v want [q]", caps.SupportedSearchParameters)
	}
	if !equalStrings(caps.SupportedTvSearchParameters, []string{"q", "rid", "season", "ep"}) {
		t.Errorf("SupportedTvSearchParameters = %v", caps.SupportedTvSearchParameters)
	}

	// Categories from the fixture: 5000 (TV with 8 subs), 7000 (Other), 8000 (Books).
	if len(caps.Categories) != 3 {
		t.Fatalf("Categories len = %d, want 3", len(caps.Categories))
	}
	tv := caps.Categories[0]
	if tv.ID != 5000 || tv.Name != "TV" {
		t.Errorf("first category: %+v", tv)
	}
	if len(tv.Subcategories) != 8 {
		t.Errorf("TV subcat count = %d want 8", len(tv.Subcategories))
	}
}

func TestParseCapabilities_SearchUnavailable_ClearsSupportedParams(t *testing.T) {
	xml := `<caps><searching>
		<search available="no"/>
		<tv-search available="no"/>
	</searching></caps>`
	caps, err := ParseCapabilities([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if caps.SupportedSearchParameters != nil {
		t.Errorf("Search unavailable: SupportedSearchParameters should be nil")
	}
	if caps.SupportedTvSearchParameters != nil {
		t.Errorf("TV search unavailable: SupportedTvSearchParameters should be nil")
	}
	if caps.SupportsAggregateIDSearch {
		t.Errorf("SupportsAggregateIDSearch should remain false")
	}
}

func TestParseCapabilities_SupportedParamsAndEngineParsed(t *testing.T) {
	xml := `<caps><searching>
		<search available="yes" supportedParams="q,id" searchEngine="elastic"/>
		<tv-search available="yes" supportedParams="q,rid,tvdbid,season,ep" searchEngine="postgres"/>
	</searching></caps>`
	caps, err := ParseCapabilities([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(caps.SupportedSearchParameters, []string{"q", "id"}) {
		t.Errorf("SupportedSearchParameters = %v", caps.SupportedSearchParameters)
	}
	if caps.TextSearchEngine != "elastic" {
		t.Errorf("TextSearchEngine = %q", caps.TextSearchEngine)
	}
	if !equalStrings(caps.SupportedTvSearchParameters, []string{"q", "rid", "tvdbid", "season", "ep"}) {
		t.Errorf("SupportedTvSearchParameters = %v", caps.SupportedTvSearchParameters)
	}
	if caps.TvTextSearchEngine != "postgres" {
		t.Errorf("TvTextSearchEngine = %q", caps.TvTextSearchEngine)
	}
	if !caps.SupportsAggregateIDSearch {
		t.Errorf("supportedParams attr on tv-search should set aggregate-id-search")
	}
}

func TestParseCapabilities_LimitsZero_KeepsDefaults(t *testing.T) {
	// Zero/missing limits should leave NewCapabilities defaults intact.
	caps, err := ParseCapabilities([]byte(`<caps><limits/></caps>`))
	if err != nil {
		t.Fatal(err)
	}
	if caps.DefaultPageSize != 100 || caps.MaxPageSize != 100 {
		t.Errorf("defaults clobbered: %d/%d", caps.DefaultPageSize, caps.MaxPageSize)
	}
}

func TestParseCapabilities_APIError(t *testing.T) {
	xml := `<caps><error code="100" description="Invalid API key"/></caps>`
	_, err := ParseCapabilities([]byte(xml))
	if err == nil {
		t.Fatal("expected error for API-error XML")
	}
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseCapabilities_MalformedXML(t *testing.T) {
	_, err := ParseCapabilities([]byte("not xml at all"))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

// CapabilitiesProvider.GetCapabilities tests use the stub client.

func TestCapabilitiesProvider_FetchAndCache(t *testing.T) {
	body, _ := os.ReadFile(filepath.Join("testdata", "caps.xml"))
	calls := 0
	var seenURL string
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		calls++
		seenURL = req.URL.String()
		return mkResponse(t, req, body), nil
	}}

	prov := NewCapabilitiesProviderWithTTL(stub, 1*time.Hour)
	prov.SetClock(func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) })

	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	s.ApiKey = "abc"

	caps, err := prov.GetCapabilities(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if caps.MaxPageSize != 60 {
		t.Errorf("MaxPageSize = %d want 60 (fixture)", caps.MaxPageSize)
	}
	if !strings.Contains(seenURL, "?t=caps") {
		t.Errorf("URL missing ?t=caps: %q", seenURL)
	}
	if !strings.Contains(seenURL, "apikey=abc") {
		t.Errorf("URL missing apikey: %q", seenURL)
	}

	// Second call within TTL — should be cached, no new fetch.
	if _, err := prov.GetCapabilities(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 fetch (cache hit), got %d", calls)
	}
}

func TestCapabilitiesProvider_TTLExpiresTriggersRefetch(t *testing.T) {
	body, _ := os.ReadFile(filepath.Join("testdata", "caps.xml"))
	calls := 0
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		calls++
		return mkResponse(t, req, body), nil
	}}

	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clock := &now
	prov := NewCapabilitiesProviderWithTTL(stub, 1*time.Hour)
	prov.SetClock(func() time.Time { return *clock })

	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"

	if _, err := prov.GetCapabilities(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	// Advance past TTL — second call should refetch.
	*clock = now.Add(2 * time.Hour)
	if _, err := prov.GetCapabilities(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("expected 2 fetches across TTL boundary, got %d", calls)
	}
}

func TestCapabilitiesProvider_FetchErrorReturnsDefaultsWithErr(t *testing.T) {
	stub := &stubClient{getFn: func(_ context.Context, _ *httpclient.Request) (*httpclient.Response, error) {
		return nil, errors.New("dial: connection refused")
	}}
	prov := NewCapabilitiesProvider(stub)
	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	caps, err := prov.GetCapabilities(context.Background(), s)
	if err == nil {
		t.Fatal("expected fetch error")
	}
	if caps == nil || caps.DefaultPageSize != 100 {
		t.Errorf("expected default Capabilities returned alongside error, got %+v", caps)
	}
}

func TestCapabilitiesProvider_ParseErrorFallsBackToDefaultsAndCaches(t *testing.T) {
	calls := 0
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		calls++
		return mkResponse(t, req, []byte("<html>captcha</html>")), nil
	}}
	prov := NewCapabilitiesProviderWithTTL(stub, 1*time.Hour)
	prov.SetClock(func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) })

	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	caps, err := prov.GetCapabilities(context.Background(), s)
	if err != nil {
		t.Fatalf("expected nil error on parse failure, got %v", err)
	}
	if caps.DefaultPageSize != 100 {
		t.Errorf("expected default caps, got %+v", caps)
	}
	// Second call within TTL — must be cached even though the body was bogus.
	if _, err := prov.GetCapabilities(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("parse failure should still cache (calls=%d, want 1)", calls)
	}
}

func TestCapabilitiesProvider_NilSettings(t *testing.T) {
	prov := NewCapabilitiesProvider(&stubClient{})
	_, err := prov.GetCapabilities(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil settings")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
