package simkl_test

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
	"sonarr.tv/sonarr/internal/importlists/simkl"
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

func settingsJSON(t *testing.T, s simkl.Settings) *string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	return &out
}

const sampleShowsBody = `{"shows":[
  {"show":{"title":"Show A","year":2020,"ids":{"simkl":1,"tvdb":"100","imdb":"tt001"}}},
  {"show":{"title":"Show B","year":2021,"ids":{"simkl":2,"tvdb":"200","imdb":"tt002"}}}
]}`

const sampleAnimeBody = `{"anime":[
  {"show":{"title":"Anime TV","ids":{"tvdb":"300","mal":"500"}},"anime_type":"tv"},
  {"show":{"title":"Anime OVA","ids":{"tvdb":"301","mal":"501"}},"anime_type":"ova"},
  {"show":{"title":"Anime Movie","ids":{"tvdb":"302","mal":"502"}},"anime_type":"movie"},
  {"show":{"title":"Anime NoTvdb","ids":{"tvdb":"","mal":"503"}},"anime_type":"tv"}
]}`

// simklServer dispatches the all-items + renew endpoints. status401
// returns 401 on /sync/all-items.
func simklServer(t *testing.T, status401 bool, body string, capture *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*capture = append(*capture, r.URL.Path)
		switch {
		case strings.Contains(r.URL.Path, "/sync/all-items/"):
			if status401 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if body == "" {
				body = `{"shows":[]}`
			}
			_, _ = w.Write([]byte(body))
		case strings.HasSuffix(r.URL.Path, "/v1/simkl_sonarr/renew"):
			_, _ = w.Write([]byte(`{"access_token":"new-token","expires_in":3600,"refresh_token":"new-refresh"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newImport(t *testing.T, srvURL string, settings simkl.Settings, recorder *fakeStatusRecorder, doer *http.Client) importlists.Provider {
	t.Helper()
	settings.BaseURL = srvURL
	simkl.SetRenewURLForTest(srvURL + "/v1/simkl_sonarr/renew")
	t.Cleanup(func() {
		simkl.SetRenewURLForTest("https://auth.servarr.com/v1/simkl_sonarr/renew")
	})
	row := &importlists.ImportList{
		ID:             11,
		Name:           "MySimkl",
		Implementation: simkl.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	deps := importlists.Dependencies{
		HTTPClient:    doer,
		StatusService: recorder,
	}
	p, err := simkl.New(row, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSimklImport_Metadata(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{Name: "X", Implementation: simkl.Implementation}
	p, err := simkl.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "Simkl User" {
		t.Errorf("Name=%q", p.Name())
	}
	if p.ListType() != importlists.ListTypeSimkl {
		t.Errorf("ListType=%v want Simkl", p.ListType())
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
	cases := []struct {
		name string
		s    simkl.Settings
		want string
	}{
		{"BadBaseURL", simkl.Settings{BaseURL: "not-a-url", AccessToken: "t"}, "BaseUrl"},
		{"MissingAccessToken", simkl.Settings{BaseURL: "https://api.simkl.com"}, "SignIn"},
		{"BadListType", simkl.Settings{BaseURL: "https://api.simkl.com", AccessToken: "t", ListType: 99}, "ListType"},
		{"BadShowType", simkl.Settings{BaseURL: "https://api.simkl.com", AccessToken: "t", ShowType: 99}, "ShowType"},
		{"OK", simkl.Settings{BaseURL: "https://api.simkl.com", AccessToken: "t", ListType: int(simkl.ListTypeWatching), ShowType: int(simkl.ShowTypeShows)}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fails := c.s.Validate()
			if c.want == "" {
				if len(fails) != 0 {
					t.Errorf("expected no failures, got %+v", fails)
				}
				return
			}
			found := false
			for _, f := range fails {
				if f.PropertyName == c.want {
					found = true
				}
			}
			if !found {
				t.Errorf("missing %s failure; got %+v", c.want, fails)
			}
		})
	}
}

func TestSimklImport_Fetch_ShowsList(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := simklServer(t, false, sampleShowsBody, &capture)
	defer srv.Close()
	settings := simkl.Settings{
		AccessToken: "tok",
		Expires:     time.Now().Add(1 * time.Hour),
		ListType:    int(simkl.ListTypeWatching),
		ShowType:    int(simkl.ShowTypeShows),
	}
	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, settings, rec, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.AnyFailure {
		t.Errorf("AnyFailure=true on success")
	}
	if len(got.Series) != 2 {
		t.Fatalf("Series=%d want 2: %+v", len(got.Series), got.Series)
	}
	if got.Series[0].TvdbID != 100 || got.Series[0].ImdbID != "tt001" {
		t.Errorf("first=%+v", got.Series[0])
	}
	if rec.success.Load() != 1 || rec.failure.Load() != 0 {
		t.Errorf("recorder success=%d failure=%d", rec.success.Load(), rec.failure.Load())
	}
	// Verify the path segments match ShowType=Shows + ListType=Watching.
	want := "/sync/all-items/shows/watching"
	found := false
	for _, c := range capture {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected path %q, got %+v", want, capture)
	}
}

func TestSimklImport_Fetch_AnimeListFiltersByType(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := simklServer(t, false, sampleAnimeBody, &capture)
	defer srv.Close()
	settings := simkl.Settings{
		AccessToken: "tok",
		Expires:     time.Now().Add(1 * time.Hour),
		ListType:    int(simkl.ListTypeWatching),
		ShowType:    int(simkl.ShowTypeAnime),
	}
	p := newImport(t, srv.URL, settings, &fakeStatusRecorder{}, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// Expected:
	//   Anime TV (tv, tvdb=300) ✓
	//   Anime OVA (ova, tvdb=301) ✓
	//   Anime Movie (movie) -- filtered out (unsupported)
	//   Anime NoTvdb (tv, tvdb="") -- filtered out (no tvdb id)
	if len(got.Series) != 2 {
		t.Fatalf("Series=%d want 2: %+v", len(got.Series), got.Series)
	}
	byTitle := map[string]importlists.ItemInfo{}
	for _, s := range got.Series {
		byTitle[s.Title] = s
	}
	if byTitle["Anime TV"].TvdbID != 300 || byTitle["Anime TV"].MalID != 500 {
		t.Errorf("Anime TV=%+v", byTitle["Anime TV"])
	}
	if byTitle["Anime OVA"].TvdbID != 301 {
		t.Errorf("Anime OVA=%+v", byTitle["Anime OVA"])
	}
}

func TestSimklImport_Fetch_RefreshesNearExpiry(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := simklServer(t, false, `{"shows":[]}`, &capture)
	defer srv.Close()
	settings := simkl.Settings{
		AccessToken:  "old-token",
		RefreshToken: "ref",
		Expires:      time.Now().Add(1 * time.Minute),
	}
	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, settings, rec, srv.Client())
	if _, err := p.Fetch(t.Context()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(capture) < 2 {
		t.Fatalf("calls=%d want >=2: %+v", len(capture), capture)
	}
	if !strings.Contains(capture[0], "/v1/simkl_sonarr/renew") {
		t.Errorf("first call should be renew, got %q", capture[0])
	}
}

func TestSimklImport_Fetch_NoRefreshTokenSkipsRenew(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := simklServer(t, false, `{"shows":[]}`, &capture)
	defer srv.Close()
	settings := simkl.Settings{
		AccessToken: "tok",
		// No refresh token, no expiry → never call renew.
	}
	p := newImport(t, srv.URL, settings, &fakeStatusRecorder{}, srv.Client())
	if _, err := p.Fetch(t.Context()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for _, c := range capture {
		if strings.Contains(c, "renew") {
			t.Errorf("did not expect renew call: %+v", capture)
		}
	}
}

func TestSimklImport_Fetch_401SurfacesFailure(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := simklServer(t, true, "", &capture)
	defer srv.Close()
	settings := simkl.Settings{AccessToken: "tok", Expires: time.Now().Add(1 * time.Hour)}
	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, settings, rec, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.AnyFailure {
		t.Errorf("AnyFailure=false on 401")
	}
}

func TestSimklImport_Test_401MapsToSignIn(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := simklServer(t, true, "", &capture)
	defer srv.Close()
	settings := simkl.Settings{AccessToken: "tok", Expires: time.Now().Add(1 * time.Hour)}
	p := newImport(t, srv.URL, settings, &fakeStatusRecorder{}, srv.Client())
	failures, _ := p.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "SignIn" {
		t.Errorf("expected SignIn failure, got %+v", failures)
	}
}

func TestSimklImport_Test_SuccessReturnsZeroFailures(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := simklServer(t, false, sampleShowsBody, &capture)
	defer srv.Close()
	settings := simkl.Settings{AccessToken: "tok", Expires: time.Now().Add(1 * time.Hour)}
	p := newImport(t, srv.URL, settings, &fakeStatusRecorder{}, srv.Client())
	failures, err := p.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
}

func TestSimklImport_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !importlists.Default().Has(simkl.Implementation, simkl.ConfigContract) {
		t.Errorf("registry missing %s/%s", simkl.Implementation, simkl.ConfigContract)
	}
}

// fakeRepo records Update calls + the persisted Settings JSON.
type fakeRepo struct {
	calls []recordedUpdate
}

type recordedUpdate struct {
	ID       int
	Settings string
}

func (r *fakeRepo) Update(_ context.Context, def *importlists.ImportList) error {
	settings := ""
	if def.Settings != nil {
		settings = *def.Settings
	}
	r.calls = append(r.calls, recordedUpdate{ID: def.ID, Settings: settings})
	return nil
}

func TestSimklImport_Fetch_RefreshPersistsNewTokenToRepo(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := simklServer(t, false, `{"shows":[]}`, &capture)
	defer srv.Close()
	settings := simkl.Settings{
		BaseURL:      srv.URL,
		AccessToken:  "old-tok",
		RefreshToken: "ref",
		Expires:      time.Now().Add(1 * time.Minute),
		ListType:     int(simkl.ListTypeWatching),
		ShowType:     int(simkl.ShowTypeShows),
	}
	simkl.SetRenewURLForTest(srv.URL + "/v1/simkl_sonarr/renew")
	t.Cleanup(func() {
		simkl.SetRenewURLForTest("https://auth.servarr.com/v1/simkl_sonarr/renew")
	})
	row := &importlists.ImportList{
		ID:             32,
		Name:           "Simkl",
		Implementation: simkl.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	repo := &fakeRepo{}
	p, err := simkl.New(row, importlists.Dependencies{
		HTTPClient:      srv.Client(),
		StatusService:   &fakeStatusRecorder{},
		ImportListsRepo: repo,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Fetch(t.Context()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(repo.calls) != 1 {
		t.Fatalf("repo.Update calls=%d, want 1", len(repo.calls))
	}
	var persisted simkl.Settings
	if err := json.Unmarshal([]byte(repo.calls[0].Settings), &persisted); err != nil {
		t.Fatalf("unmarshal persisted: %v", err)
	}
	if persisted.AccessToken != "new-token" {
		t.Errorf("persisted AccessToken=%q want new-token", persisted.AccessToken)
	}
}

func TestSimklImport_RequestAction_StartOAuth_BuildsAuthorizeURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := newImport(t, srv.URL, simkl.Settings{
		AccessToken: "tok", RefreshToken: "ref",
		Expires: time.Now().Add(time.Hour),
	}, &fakeStatusRecorder{}, srv.Client())
	out, err := p.RequestAction(t.Context(), "startOAuth", map[string]string{"callbackUrl": "http://cb/x"})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m := out.(map[string]any)
	u, _ := m["OauthUrl"].(string)
	if !strings.HasPrefix(u, "https://simkl.com/oauth/authorize?") {
		t.Errorf("OauthUrl=%q", u)
	}
	if !strings.Contains(u, "response_type=code") || !strings.Contains(u, "state=http%3A%2F%2Fcb%2Fx") || !strings.Contains(u, "client_id=3281c139") {
		t.Errorf("OauthUrl missing required params: %s", u)
	}
}

func TestSimklImport_RequestAction_GetOAuthToken_ResolvesAuthUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/settings" {
			_, _ = w.Write([]byte(`{"account":{"id":42}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := newImport(t, srv.URL, simkl.Settings{
		AccessToken: "tok", RefreshToken: "ref",
		Expires: time.Now().Add(time.Hour),
	}, &fakeStatusRecorder{}, srv.Client())
	out, err := p.RequestAction(t.Context(), "getOAuthToken", map[string]string{
		"access_token":  "new-tok",
		"expires_in":    "3600",
		"refresh_token": "new-ref",
	})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m := out.(map[string]any)
	if m["accessToken"] != "new-tok" || m["refreshToken"] != "new-ref" {
		t.Errorf("tuple wrong: %+v", m)
	}
	if m["authUser"] != "42" {
		t.Errorf("authUser=%v want \"42\" (stringified account.id)", m["authUser"])
	}
}

func TestSimklImport_RequestAction_GetOAuthToken_UserSettingsFailureFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/settings" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := newImport(t, srv.URL, simkl.Settings{
		AccessToken: "tok", RefreshToken: "ref",
		Expires: time.Now().Add(time.Hour),
	}, &fakeStatusRecorder{}, srv.Client())
	out, err := p.RequestAction(t.Context(), "getOAuthToken", map[string]string{
		"access_token":  "x",
		"expires_in":    "3600",
		"refresh_token": "x",
	})
	if err != nil {
		t.Fatalf("user-settings failure should NOT propagate: %v", err)
	}
	if m := out.(map[string]any); m["authUser"] != "" {
		t.Errorf("authUser=%v want empty fallback", m["authUser"])
	}
}

func TestSimklImport_RequestAction_BadRequestCases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := newImport(t, srv.URL, simkl.Settings{
		AccessToken: "tok", RefreshToken: "ref",
		Expires: time.Now().Add(time.Hour),
	}, &fakeStatusRecorder{}, srv.Client())
	if _, err := p.RequestAction(t.Context(), "startOAuth", map[string]string{}); !errors.Is(err, simkl.ErrBadRequest) {
		t.Errorf("startOAuth missing callback: err=%v", err)
	}
	cases := []map[string]string{
		{"expires_in": "3600", "refresh_token": "x"},
		{"access_token": "x", "refresh_token": "x"},
		{"access_token": "x", "expires_in": "3600"},
		{"access_token": "x", "expires_in": "bad", "refresh_token": "x"},
	}
	for i, q := range cases {
		_, err := p.RequestAction(t.Context(), "getOAuthToken", q)
		if !errors.Is(err, simkl.ErrBadRequest) {
			t.Errorf("case %d: err=%v want ErrBadRequest", i, err)
		}
	}
}
