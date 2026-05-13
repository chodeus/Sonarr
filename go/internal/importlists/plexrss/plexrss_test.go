package plexrss_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/importlists/plexrss"
)

type fakeStatusRecorder struct {
	success atomic.Int32
	failure atomic.Int32
}

func (r *fakeStatusRecorder) RecordSuccess(_ context.Context, _ int) error {
	r.success.Add(1)
	return nil
}

func (r *fakeStatusRecorder) RecordFailure(_ context.Context, _ int, _ time.Duration) error {
	r.failure.Add(1)
	return nil
}

func settingsJSON(t *testing.T, s plexrss.Settings) *string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	return &out
}

const sampleFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Plex Watchlist</title>
    <item>
      <title>The Show</title>
      <category>show</category>
      <guid>tvdb://12345</guid>
    </item>
    <item>
      <title>Another Show</title>
      <category>show</category>
      <guid>imdb://tt7654321</guid>
    </item>
    <item>
      <title>TmdbShow</title>
      <category>show</category>
      <guid>tmdb://999</guid>
    </item>
    <item>
      <title>SomeMovie</title>
      <category>movie</category>
      <guid>tvdb://77</guid>
    </item>
    <item>
      <title>NoGuid</title>
      <category>show</category>
      <guid></guid>
    </item>
  </channel>
</rss>`

func newImport(t *testing.T, srvURL string, recorder *fakeStatusRecorder, doer *http.Client) importlists.Provider {
	t.Helper()
	row := &importlists.ImportList{
		ID:             7,
		Name:           "MyWatchlist",
		Implementation: plexrss.Implementation,
		Settings:       settingsJSON(t, plexrss.Settings{URL: srvURL}),
	}
	deps := importlists.Dependencies{
		HTTPClient:    doer,
		StatusService: recorder,
	}
	p, err := plexrss.New(row, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestPlexRssImport_Metadata(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{Name: "X", Implementation: plexrss.Implementation}
	p, err := plexrss.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "Plex Watchlist (RSS)" {
		t.Errorf("Name=%q", p.Name())
	}
	if p.ListType() != importlists.ListTypePlex {
		t.Errorf("ListType=%v want Plex", p.ListType())
	}
	if p.MinRefreshInterval() != 6*time.Hour {
		t.Errorf("MinRefreshInterval=%v want 6h", p.MinRefreshInterval())
	}
	if p.Definition() != row {
		t.Errorf("Definition: pointer not preserved")
	}
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	if fails := (&plexrss.Settings{}).Validate(); len(fails) == 0 || fails[0].PropertyName != "Url" {
		t.Errorf("expected Url failure for empty URL, got %+v", fails)
	}
	if fails := (&plexrss.Settings{URL: "https://rss.plex.tv/abc"}).Validate(); len(fails) != 0 {
		t.Errorf("expected no failures for set URL, got %+v", fails)
	}
}

func TestPlexRssImport_Fetch_FiltersToShowsAndParsesIDs(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleFeed))
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, rec, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.AnyFailure {
		t.Errorf("AnyFailure=true on success")
	}
	// 3 valid show entries: tvdb, imdb, tmdb. The movie + no-guid
	// entries are skipped.
	if len(got.Series) != 3 {
		t.Fatalf("Series=%d want 3: %+v", len(got.Series), got.Series)
	}
	byTitle := map[string]importlists.ItemInfo{}
	for _, s := range got.Series {
		byTitle[s.Title] = s
	}
	if byTitle["The Show"].TvdbID != 12345 {
		t.Errorf("tvdb id=%v", byTitle["The Show"].TvdbID)
	}
	if byTitle["Another Show"].ImdbID != "tt7654321" {
		t.Errorf("imdb id=%q", byTitle["Another Show"].ImdbID)
	}
	if byTitle["TmdbShow"].TmdbID != 999 {
		t.Errorf("tmdb id=%v", byTitle["TmdbShow"].TmdbID)
	}
	// ImportListID/ImportList stamping.
	for _, s := range got.Series {
		if s.ImportListID != 7 || s.ImportList != "MyWatchlist" {
			t.Errorf("stamping missing: %+v", s)
		}
	}
	if rec.success.Load() != 1 || rec.failure.Load() != 0 {
		t.Errorf("recorder success=%d failure=%d want 1/0", rec.success.Load(), rec.failure.Load())
	}
}

func TestPlexRssImport_Fetch_HTMLResponseReportsFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>Blocked</body></html>`))
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, rec, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.AnyFailure {
		t.Errorf("AnyFailure=false on HTML response")
	}
	if rec.failure.Load() != 1 {
		t.Errorf("failure=%d want 1", rec.failure.Load())
	}
}

func TestPlexRssImport_Fetch_ServerErrorReportsFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, rec, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.AnyFailure {
		t.Errorf("AnyFailure=false on 500")
	}
}

func TestPlexRssImport_Fetch_MalformedXMLReportsFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<<not-valid-xml>>`))
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, rec, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.AnyFailure {
		t.Errorf("AnyFailure=false on bad XML")
	}
}

func TestPlexRssImport_Test_401MapsToUrl(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := newImport(t, srv.URL, &fakeStatusRecorder{}, srv.Client())
	failures, _ := p.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "Url" {
		t.Errorf("expected Url failure, got %+v", failures)
	}
}

func TestPlexRssImport_Test_SuccessReturnsZeroFailures(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleFeed))
	}))
	defer srv.Close()
	p := newImport(t, srv.URL, &fakeStatusRecorder{}, srv.Client())
	failures, err := p.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
}

func TestPlexRssImport_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !importlists.Default().Has(plexrss.Implementation, plexrss.ConfigContract) {
		t.Errorf("registry missing %s/%s", plexrss.Implementation, plexrss.ConfigContract)
	}
}
