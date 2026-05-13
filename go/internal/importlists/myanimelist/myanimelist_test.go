package myanimelist_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/importlists/myanimelist"
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

func settingsJSON(t *testing.T, s myanimelist.Settings) *string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	return &out
}

const sampleListBody = `{"data":[
  {"node":{"id":1,"title":"Anime A"},"list_status":{"status":"watching","score":8}},
  {"node":{"id":2,"title":"Anime B"},"list_status":{"status":"completed","score":5}},
  {"node":{"id":3,"title":"Anime C"},"list_status":{"status":"completed","score":2}}
]}`

// malServer dispatches the animelist + renew endpoints. status401=true
// returns 401 on the list endpoint.
func malServer(t *testing.T, status401 bool, listBody string, capture *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*capture = append(*capture, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/users/@me/animelist"):
			if status401 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if listBody == "" {
				listBody = `{"data":[]}`
			}
			_, _ = w.Write([]byte(listBody))
		case strings.HasSuffix(r.URL.Path, "/oauth/myanimelist/renew"):
			_, _ = w.Write([]byte(`{"access_token":"new-token","token_type":"Bearer","expires_in":3600,"refresh_token":"new-refresh"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newImport(t *testing.T, srvURL string, settings myanimelist.Settings, recorder *fakeStatusRecorder, doer *http.Client) importlists.Provider {
	t.Helper()
	settings.BaseURL = srvURL
	myanimelist.SetRenewURLForTest(srvURL + "/oauth/myanimelist/renew")
	t.Cleanup(func() {
		myanimelist.SetRenewURLForTest("https://auth.servarr.com/oauth/myanimelist/renew")
	})
	row := &importlists.ImportList{
		ID:             9,
		Name:           "MyMAL",
		Implementation: myanimelist.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	deps := importlists.Dependencies{
		HTTPClient:    doer,
		StatusService: recorder,
	}
	p, err := myanimelist.New(row, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestMALImport_Metadata(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{Name: "X", Implementation: myanimelist.Implementation}
	p, err := myanimelist.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "MyAnimeList" {
		t.Errorf("Name=%q", p.Name())
	}
	if p.ListType() != importlists.ListTypeOther {
		t.Errorf("ListType=%v want Other", p.ListType())
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
		s    myanimelist.Settings
		want string
	}{
		{"BadBaseURL", myanimelist.Settings{BaseURL: "not-a-url", AccessToken: "t"}, "BaseUrl"},
		{"MissingAccessToken", myanimelist.Settings{BaseURL: "https://api.myanimelist.net/v2"}, "SignIn"},
		{"MinScoreTooHigh", myanimelist.Settings{BaseURL: "https://api.myanimelist.net/v2", AccessToken: "t", MinimumScore: 11}, "MinimumScore"},
		{"BadStatus", myanimelist.Settings{BaseURL: "https://api.myanimelist.net/v2", AccessToken: "t", ListStatus: 99}, "ListStatus"},
		{"OK", myanimelist.Settings{BaseURL: "https://api.myanimelist.net/v2", AccessToken: "t"}, ""},
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

func TestMALImport_Fetch_MinScoreFiltersOut(t *testing.T) {
	// Cannot t.Parallel(): newImport sets package-global renewURL
	// via SetRenewURLForTest — concurrent tests would race.
	var capture []string
	srv := malServer(t, false, sampleListBody, &capture)
	defer srv.Close()
	settings := myanimelist.Settings{
		AccessToken:  "tok",
		Expires:      time.Now().Add(1 * time.Hour),
		MinimumScore: 5,
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
	// MinimumScore=5: Anime A (8) + Anime B (5) keep, Anime C (2)
	// filters out.
	if len(got.Series) != 2 {
		t.Fatalf("Series=%d want 2: %+v", len(got.Series), got.Series)
	}
	if got.Series[0].MalID != 1 || got.Series[0].Title != "Anime A" {
		t.Errorf("first=%+v", got.Series[0])
	}
	if got.Series[0].ImportListID != 9 || got.Series[0].ImportList != "MyMAL" {
		t.Errorf("stamping missing: %+v", got.Series[0])
	}
	if rec.success.Load() != 1 || rec.failure.Load() != 0 {
		t.Errorf("recorder success=%d failure=%d", rec.success.Load(), rec.failure.Load())
	}
}

func TestMALImport_Fetch_StatusFilterPropagates(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	// Capture full URL via custom handler.
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/users/@me/animelist") {
			lastQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	settings := myanimelist.Settings{
		AccessToken: "tok",
		Expires:     time.Now().Add(1 * time.Hour),
		ListStatus:  int(myanimelist.StatusCompleted),
	}
	p := newImport(t, srv.URL, settings, &fakeStatusRecorder{}, srv.Client())
	_, _ = p.Fetch(t.Context())
	if !strings.Contains(lastQuery, "status=completed") {
		t.Errorf("query=%q want status=completed", lastQuery)
	}
	_ = capture
}

func TestMALImport_Fetch_RefreshesNearExpiry(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := malServer(t, false, `{"data":[]}`, &capture)
	defer srv.Close()
	settings := myanimelist.Settings{
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
	if !strings.Contains(capture[0], "/oauth/myanimelist/renew") {
		t.Errorf("first call should be renew, got %q", capture[0])
	}
	if !strings.HasSuffix(capture[1], "/users/@me/animelist") {
		t.Errorf("second call should be animelist, got %q", capture[1])
	}
}

func TestMALImport_Fetch_401SurfacesFailure(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := malServer(t, true, "", &capture)
	defer srv.Close()
	settings := myanimelist.Settings{
		AccessToken: "tok",
		Expires:     time.Now().Add(1 * time.Hour),
	}
	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, settings, rec, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.AnyFailure {
		t.Errorf("AnyFailure=false on 401")
	}
	if rec.failure.Load() != 1 {
		t.Errorf("failure=%d want 1", rec.failure.Load())
	}
}

func TestMALImport_Test_401MapsToSignIn(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := malServer(t, true, "", &capture)
	defer srv.Close()
	settings := myanimelist.Settings{
		AccessToken: "tok",
		Expires:     time.Now().Add(1 * time.Hour),
	}
	p := newImport(t, srv.URL, settings, &fakeStatusRecorder{}, srv.Client())
	failures, _ := p.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "SignIn" {
		t.Errorf("expected SignIn failure, got %+v", failures)
	}
}

func TestMALImport_Test_SuccessReturnsZeroFailures(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := malServer(t, false, sampleListBody, &capture)
	defer srv.Close()
	settings := myanimelist.Settings{
		AccessToken: "tok",
		Expires:     time.Now().Add(1 * time.Hour),
	}
	p := newImport(t, srv.URL, settings, &fakeStatusRecorder{}, srv.Client())
	failures, err := p.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
}

func TestMALImport_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !importlists.Default().Has(myanimelist.Implementation, myanimelist.ConfigContract) {
		t.Errorf("registry missing %s/%s", myanimelist.Implementation, myanimelist.ConfigContract)
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

func TestMALImport_Fetch_RefreshPersistsNewTokenToRepo(t *testing.T) {
	// Shares newImport global state — not parallel.
	var capture []string
	srv := malServer(t, false, `{"data":[]}`, &capture)
	defer srv.Close()
	settings := myanimelist.Settings{
		BaseURL:      srv.URL,
		AccessToken:  "old-tok",
		RefreshToken: "ref",
		Expires:      time.Now().Add(1 * time.Minute),
	}
	myanimelist.SetRenewURLForTest(srv.URL + "/oauth/myanimelist/renew")
	t.Cleanup(func() {
		myanimelist.SetRenewURLForTest("https://auth.servarr.com/oauth/myanimelist/renew")
	})
	row := &importlists.ImportList{
		ID:             31,
		Name:           "MAL",
		Implementation: myanimelist.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	repo := &fakeRepo{}
	p, err := myanimelist.New(row, importlists.Dependencies{
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
	var persisted myanimelist.Settings
	if err := json.Unmarshal([]byte(repo.calls[0].Settings), &persisted); err != nil {
		t.Fatalf("unmarshal persisted: %v", err)
	}
	if persisted.AccessToken != "new-token" {
		t.Errorf("persisted AccessToken=%q want new-token", persisted.AccessToken)
	}
	if persisted.RefreshToken != "new-refresh" {
		t.Errorf("persisted RefreshToken=%q want new-refresh", persisted.RefreshToken)
	}
}
