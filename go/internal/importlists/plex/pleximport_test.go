package plex_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/importlists/plex"
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

// fakeConfig satisfies both the configClientIdentifier and the
// plexHostsProvider narrow interfaces PlexImport.New consults
// against deps.ConfigProvider.
type fakeConfig struct {
	clientID         string
	plexTv           string
	plexTvAuth       string
	plexTvDiscover   string
	hostsOverrideSet bool
}

func (f *fakeConfig) PlexClientIdentifier() string {
	return f.clientID
}

func (f *fakeConfig) PlexHosts() (plexTv, plexTvAuth, plexTvDiscover string) {
	if !f.hostsOverrideSet {
		return "", "", ""
	}
	return f.plexTv, f.plexTvAuth, f.plexTvDiscover
}

func settingsJSON(t *testing.T, s plex.Settings) *string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	return &out
}

func newImport(t *testing.T, srvURL string, settings plex.Settings, rec *fakeStatusRecorder, doer *http.Client) (importlists.Provider, *importlists.ImportList) {
	t.Helper()
	row := &importlists.ImportList{
		ID:             5,
		Name:           "MyWatchlist",
		Implementation: plex.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	deps := importlists.Dependencies{
		HTTPClient:    doer,
		StatusService: rec,
		ConfigProvider: &fakeConfig{
			clientID:         "test-cid",
			plexTv:           srvURL,
			plexTvAuth:       srvURL,
			plexTvDiscover:   srvURL,
			hostsOverrideSet: true,
		},
	}
	p, err := plex.New(row, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, row
}

func TestPlexImport_Metadata(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{Name: "x", Implementation: plex.Implementation}
	p, err := plex.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "Plex Watchlist" {
		t.Errorf("Name: %q", p.Name())
	}
	if p.ListType() != importlists.ListTypePlex {
		t.Errorf("ListType: %v", p.ListType())
	}
	if p.MinRefreshInterval() != 6*time.Hour {
		t.Errorf("MinRefreshInterval: %v", p.MinRefreshInterval())
	}
}

func TestPlexImport_Fetch_HappyPath_SinglePage(t *testing.T) {
	t.Parallel()
	hits := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		start := r.URL.Query().Get("X-Plex-Container-Start")
		if start == "0" {
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"ratingKey":"r1","title":"Show A","year":2020,"Guid":[{"id":"tvdb://111"},{"id":"imdb://tt222"}]},
				{"ratingKey":"r2","title":"Show B","year":2021,"Guid":[{"id":"tmdb://333"}]}
			]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p, row := newImport(t, srv.URL, plex.Settings{AccessToken: "tok"}, rec, srv.Client())

	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.AnyFailure {
		t.Errorf("AnyFailure: true")
	}
	if len(res.Series) != 2 {
		t.Fatalf("Series: %d want 2", len(res.Series))
	}
	if res.Series[0].TvdbID != 111 || res.Series[0].ImdbID != "tt222" || res.Series[0].Title != "Show A" {
		t.Errorf("[0]: %+v", res.Series[0])
	}
	if res.Series[1].TmdbID != 333 || res.Series[1].Title != "Show B" {
		t.Errorf("[1]: %+v", res.Series[1])
	}
	if res.Series[0].ImportListID != row.ID || res.Series[0].ImportList != row.Name {
		t.Errorf("stamping: %+v", res.Series[0])
	}
	if rec.success.Load() != 1 || rec.failure.Load() != 0 {
		t.Errorf("recorder: %d/%d", rec.success.Load(), rec.failure.Load())
	}
}

func TestPlexImport_Fetch_MissingAccessToken_ShortCircuits(t *testing.T) {
	t.Parallel()
	calls := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p, _ := newImport(t, srv.URL, plex.Settings{}, rec, srv.Client())

	res, _ := p.Fetch(context.Background())
	if !res.AnyFailure {
		t.Errorf("AnyFailure: false want true")
	}
	if calls.Load() != 0 {
		t.Errorf("server hit %d times — expected zero", calls.Load())
	}
	if rec.failure.Load() != 1 {
		t.Errorf("RecordFailure not called")
	}
}

func TestPlexImport_Fetch_HardFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p, _ := newImport(t, srv.URL, plex.Settings{AccessToken: "t"}, rec, srv.Client())
	res, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.AnyFailure || len(res.Series) != 0 {
		t.Errorf("Fetch: %+v", res)
	}
	if rec.failure.Load() != 1 {
		t.Errorf("RecordFailure: %d", rec.failure.Load())
	}
}

func TestPlexImport_Fetch_StopsOnEmptyPage(t *testing.T) {
	t.Parallel()
	hits := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		start := r.URL.Query().Get("X-Plex-Container-Start")
		var items []any
		if start == "0" {
			items = []any{
				map[string]any{"ratingKey": "r1", "title": "A", "year": 2020, "Guid": []any{map[string]any{"id": "tvdb://1"}}},
			}
		}
		// page >0 returns empty Metadata → Fetch should stop.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MediaContainer": map[string]any{"Metadata": items},
		})
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p, _ := newImport(t, srv.URL, plex.Settings{AccessToken: "t"}, rec, srv.Client())
	res, _ := p.Fetch(context.Background())
	if len(res.Series) != 1 {
		t.Errorf("Series: %d want 1", len(res.Series))
	}
	if hits.Load() != 2 {
		t.Errorf("server hits: %d want 2 (page 0 with items + page 1 empty stops)", hits.Load())
	}
}

func TestPlexImport_Test_MissingToken(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{
		ID: 1, Name: "x", Implementation: plex.Implementation,
		Settings: settingsJSON(t, plex.Settings{}),
	}
	p, _ := plex.New(row, importlists.Dependencies{})
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 1 || failures[0].PropertyName != "SignIn" {
		t.Errorf("failures: %+v", failures)
	}
}

func TestPlexImport_Test_TransportError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	p, _ := newImport(t, srv.URL, plex.Settings{AccessToken: "t"}, &fakeStatusRecorder{}, srv.Client())
	failures, _ := p.Test(context.Background())
	if len(failures) != 1 || failures[0].PropertyName != "" {
		t.Errorf("failures: %+v", failures)
	}
	if !strings.Contains(failures[0].ErrorMessage, "Unable to connect") {
		t.Errorf("ErrorMessage: %q", failures[0].ErrorMessage)
	}
}

func TestPlexImport_RequestAction_StartOAuth(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	p, _ := newImport(t, srv.URL, plex.Settings{}, &fakeStatusRecorder{}, srv.Client())
	got, err := p.RequestAction(context.Background(), "startOAuth", nil)
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	r, ok := got.(plex.PinURLResponse)
	if !ok {
		t.Fatalf("type: %T", got)
	}
	if !r.Poll || !strings.Contains(r.URL, "/api/v2/pins") {
		t.Errorf("payload: %+v", r)
	}
}

func TestPlexImport_RequestAction_ContinueOAuth(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	p, _ := newImport(t, srv.URL, plex.Settings{}, &fakeStatusRecorder{}, srv.Client())
	got, err := p.RequestAction(context.Background(), "continueOAuth", map[string]string{
		"callbackUrl": "http://cb",
		"id":          "42",
		"code":        "ZZZ",
	})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	r, ok := got.(plex.SignInURLResponse)
	if !ok {
		t.Fatalf("type: %T", got)
	}
	if r.PinID != 42 || !strings.Contains(r.OauthURL, "code=ZZZ") {
		t.Errorf("payload: %+v", r)
	}
}

func TestPlexImport_RequestAction_ContinueOAuth_BadParams(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	p, _ := newImport(t, srv.URL, plex.Settings{}, &fakeStatusRecorder{}, srv.Client())
	cases := []map[string]string{
		{},
		{"callbackUrl": "x"},
		{"callbackUrl": "x", "id": "1"},
		{"callbackUrl": "x", "id": "notanumber", "code": "Z"},
	}
	for i, q := range cases {
		_, err := p.RequestAction(context.Background(), "continueOAuth", q)
		if !errors.Is(err, plex.ErrBadRequest) {
			t.Errorf("[%d] expected ErrBadRequest, got %v", i, err)
		}
	}
}

func TestPlexImport_RequestAction_PollOAuth(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"authToken":"abc"}`))
	}))
	defer srv.Close()
	p, _ := newImport(t, srv.URL, plex.Settings{}, &fakeStatusRecorder{}, srv.Client())
	got, err := p.RequestAction(context.Background(), "pollOAuth", map[string]string{"pinId": "1"})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, _ := got.(map[string]any)
	if m == nil || m["success"] != true {
		t.Errorf("payload: %v", got)
	}
}

func TestPlexImport_RequestAction_GetOAuthToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"authToken":"final-tok"}`))
	}))
	defer srv.Close()
	p, _ := newImport(t, srv.URL, plex.Settings{}, &fakeStatusRecorder{}, srv.Client())
	got, err := p.RequestAction(context.Background(), "getOAuthToken", map[string]string{"pinId": "1"})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, _ := got.(map[string]any)
	if m == nil || m["accessToken"] != "final-tok" {
		t.Errorf("payload: %v", got)
	}
}

func TestPlexImport_RequestAction_Unknown(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	p, _ := newImport(t, srv.URL, plex.Settings{}, &fakeStatusRecorder{}, srv.Client())
	got, err := p.RequestAction(context.Background(), "unknownAction", nil)
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok || len(m) != 0 {
		t.Errorf("expected {}, got %v", got)
	}
}

// Slice 4.AN closes sub-arc I3b by registering against the package-
// global Default Registry; the (PlexImport, PlexListSettings) pair
// must be reachable.
func TestRegister_DefaultRegistered(t *testing.T) {
	t.Parallel()
	if !importlists.Default().Has(plex.Implementation, plex.ConfigContract) {
		t.Errorf("Default registry missing (%s, %s) — init() did not run",
			plex.Implementation, plex.ConfigContract)
	}
}
