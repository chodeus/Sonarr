package trakt_test

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
	"sonarr.tv/sonarr/internal/importlists/trakt"
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

func ptrStr(t *testing.T, v any) *string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	return &out
}

// traktServer routes /users/.../* and /shows/* paths to the supplied
// body. capture records request paths for assertions.
func traktServer(t *testing.T, routes map[string]string, status401 bool, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*capture = append(*capture, captured{Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"), APIKey: r.Header.Get("trakt-api-key")})
		if status401 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		for prefix, body := range routes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, "/v1/trakt_sonarr/renew") {
			_, _ = w.Write([]byte(`{"access_token":"new-token","expires_in":3600,"refresh_token":"new-refresh"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

type captured struct {
	Path   string
	Query  string
	Auth   string
	APIKey string
}

func setupBase(srvURL string) func() {
	trakt.SetRenewURLForTest(srvURL + "/v1/trakt_sonarr/renew")
	return func() {
		trakt.SetRenewURLForTest("https://auth.servarr.com/v1/trakt_sonarr/renew")
	}
}

// ------------------------------------------------------------------
// SettingsBase tests
// ------------------------------------------------------------------

func TestSettingsBase_Validate_ChainedSignInRule(t *testing.T) {
	t.Parallel()
	// AccessToken set but RefreshToken empty → SignIn failure.
	s := trakt.UserSettings{
		SettingsBase: trakt.SettingsBase{
			BaseURL:     "https://api.trakt.tv",
			AccessToken: "tok",
			AuthUser:    "user",
			Limit:       100,
		},
	}
	fails := s.Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "SignIn" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SignIn failure when only AccessToken set, got %+v", fails)
	}
}

func TestSettingsBase_Validate_LimitGreaterThanZero(t *testing.T) {
	t.Parallel()
	s := trakt.UserSettings{
		SettingsBase: trakt.SettingsBase{
			BaseURL:      "https://api.trakt.tv",
			AccessToken:  "tok",
			RefreshToken: "ref",
			Expires:      time.Now(),
			AuthUser:     "user",
			Limit:        0,
		},
	}
	fails := s.Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "Limit" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Limit failure for Limit=0, got %+v", fails)
	}
}

// ------------------------------------------------------------------
// User variant
// ------------------------------------------------------------------

func userSettings(srvURL string) trakt.UserSettings {
	return trakt.UserSettings{
		SettingsBase: trakt.SettingsBase{
			BaseURL:      srvURL,
			AccessToken:  "tok",
			RefreshToken: "ref",
			Expires:      time.Now().Add(1 * time.Hour),
			AuthUser:     "auth-user",
			Limit:        100,
		},
		TraktListType:        int(trakt.UserListTypeWatchList),
		TraktWatchedListType: int(trakt.UserWatchedListAll),
		TraktWatchSorting:    int(trakt.UserWatchSortingRank),
		Username:             "user1",
	}
}

func newUser(t *testing.T, s trakt.UserSettings, doer *http.Client) importlists.Provider {
	t.Helper()
	row := &importlists.ImportList{
		ID:             21,
		Name:           "TraktUser",
		Implementation: trakt.UserImplementation,
		Settings:       ptrStr(t, s),
	}
	p, err := trakt.NewUser(row, importlists.Dependencies{HTTPClient: doer, StatusService: &fakeStatusRecorder{}})
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	return p
}

func TestUserImport_Metadata(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{Implementation: trakt.UserImplementation, Settings: ptrStr(t, trakt.UserSettings{})}
	p, err := trakt.NewUser(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	if p.Name() != "Trakt User List" {
		t.Errorf("Name=%q", p.Name())
	}
	if p.ListType() != importlists.ListTypeTrakt {
		t.Errorf("ListType=%v want Trakt", p.ListType())
	}
	if p.MinRefreshInterval() != 12*time.Hour {
		t.Errorf("MinRefreshInterval=%v want 12h", p.MinRefreshInterval())
	}
}

const userWatchListBody = `[{"show":{"title":"Show A","year":2020,"ids":{"tvdb":100,"imdb":"tt001"}}},{"show":{"title":"Show B","year":2021,"ids":{"tvdb":200,"imdb":"tt002"}}}]`

func TestUserImport_Fetch_WatchListEndpointAndStamp(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/users/": userWatchListBody}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	p := newUser(t, userSettings(srv.URL), srv.Client())
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
	// Endpoint shape: /users/user1/watchlist/shows/rank?limit=100
	if !strings.Contains(capture[0].Path, "/users/user1/watchlist/shows/rank") {
		t.Errorf("path=%q", capture[0].Path)
	}
	if !strings.Contains(capture[0].Query, "limit=100") {
		t.Errorf("query=%q", capture[0].Query)
	}
	if got.Series[0].ImportList != "TraktUser" || got.Series[0].ImportListID != 21 {
		t.Errorf("stamp missing: %+v", got.Series[0])
	}
	if capture[0].APIKey == "" {
		t.Errorf("trakt-api-key missing")
	}
	if !strings.HasPrefix(capture[0].Auth, "Bearer ") {
		t.Errorf("auth=%q", capture[0].Auth)
	}
}

func TestUserImport_Fetch_WatchListSortingPropagates(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/users/": userWatchListBody}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	s := userSettings(srv.URL)
	s.TraktWatchSorting = int(trakt.UserWatchSortingAdded)
	p := newUser(t, s, srv.Client())
	_, _ = p.Fetch(t.Context())
	if !strings.HasSuffix(capture[0].Path, "/added") {
		t.Errorf("expected /added sort segment, got %q", capture[0].Path)
	}
}

const userWatchedBody = `[{"show":{"title":"WatchedA","ids":{"tvdb":300}, "aired_episodes": 10},"seasons":[{"number":1,"episodes":[{"plays":1},{"plays":1}]}]}, {"show":{"title":"WatchedB","ids":{"tvdb":400}, "aired_episodes": 10},"seasons":[{"number":1,"episodes":[{"plays":1},{"plays":1},{"plays":1},{"plays":1},{"plays":1},{"plays":1},{"plays":1},{"plays":1},{"plays":1},{"plays":1}]}]}]`

func TestUserImport_Fetch_WatchedListFilter_InProgress(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/users/": userWatchedBody}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	s := userSettings(srv.URL)
	s.TraktListType = int(trakt.UserListTypeWatchedList)
	s.TraktWatchedListType = int(trakt.UserWatchedListInProgress)
	p := newUser(t, s, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// WatchedA has 2 of 10 episodes (in-progress) → kept.
	// WatchedB has 10 of 10 (completed) → filtered out.
	if len(got.Series) != 1 || got.Series[0].Title != "WatchedA" {
		t.Errorf("Series=%+v want [WatchedA]", got.Series)
	}
}

func TestUserImport_Fetch_WatchedListFilter_CompletelyWatched(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/users/": userWatchedBody}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	s := userSettings(srv.URL)
	s.TraktListType = int(trakt.UserListTypeWatchedList)
	s.TraktWatchedListType = int(trakt.UserWatchedListCompletelyWatched)
	p := newUser(t, s, srv.Client())
	got, _ := p.Fetch(t.Context())
	if len(got.Series) != 1 || got.Series[0].Title != "WatchedB" {
		t.Errorf("Series=%+v want [WatchedB]", got.Series)
	}
}

func TestUserImport_Fetch_UsernameFallsBackToAuthUser(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/users/": "[]"}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	s := userSettings(srv.URL)
	s.Username = ""
	p := newUser(t, s, srv.Client())
	_, _ = p.Fetch(t.Context())
	if !strings.Contains(capture[0].Path, "/users/auth-user/") {
		t.Errorf("expected auth-user fallback, got %q", capture[0].Path)
	}
}

func TestUserImport_Test_401MapsToSignIn(t *testing.T) {
	var capture []captured
	srv := traktServer(t, nil, true, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	p := newUser(t, userSettings(srv.URL), srv.Client())
	failures, _ := p.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "SignIn" {
		t.Errorf("expected SignIn failure, got %+v", failures)
	}
}

func TestUserImport_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !importlists.Default().Has(trakt.UserImplementation, trakt.UserConfigContract) {
		t.Errorf("registry missing %s/%s", trakt.UserImplementation, trakt.UserConfigContract)
	}
}

// ------------------------------------------------------------------
// Popular variant
// ------------------------------------------------------------------

func popularSettings(srvURL string) trakt.PopularSettings {
	return trakt.PopularSettings{
		SettingsBase: trakt.SettingsBase{
			BaseURL:      srvURL,
			AccessToken:  "tok",
			RefreshToken: "ref",
			Expires:      time.Now().Add(1 * time.Hour),
			Limit:        100,
		},
		TraktListType: int(trakt.PopularPopular),
	}
}

func newPopular(t *testing.T, s trakt.PopularSettings, doer *http.Client) importlists.Provider {
	t.Helper()
	row := &importlists.ImportList{
		ID:             22,
		Name:           "TraktPopular",
		Implementation: trakt.PopularImplementation,
		Settings:       ptrStr(t, s),
	}
	p, err := trakt.NewPopular(row, importlists.Dependencies{HTTPClient: doer, StatusService: &fakeStatusRecorder{}})
	if err != nil {
		t.Fatalf("NewPopular: %v", err)
	}
	return p
}

const popularFlatBody = `[{"title":"Pop A","year":2024,"ids":{"tvdb":500,"imdb":"tt005"}},{"title":"Pop B","year":2024,"ids":{"tvdb":600,"imdb":"tt006"}}]`
const popularWrappedBody = `[{"show":{"title":"Trend A","year":2024,"ids":{"tvdb":700}}},{"show":{"title":"Trend B","year":2024,"ids":{"tvdb":800}}}]`

func TestPopularImport_Fetch_PopularReturnsFlatSeriesArray(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/shows/popular": popularFlatBody}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	p := newPopular(t, popularSettings(srv.URL), srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Series) != 2 || got.Series[0].Title != "Pop A" {
		t.Errorf("Series=%+v", got.Series)
	}
}

func TestPopularImport_Fetch_NonPopularUnwrapsShowField(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/shows/trending": popularWrappedBody}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	s := popularSettings(srv.URL)
	s.TraktListType = int(trakt.PopularTrending)
	p := newPopular(t, s, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Series) != 2 || got.Series[0].Title != "Trend A" {
		t.Errorf("Series=%+v", got.Series)
	}
}

func TestPopularImport_QueryStringIncludesFilters(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/shows/popular": popularFlatBody}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	s := popularSettings(srv.URL)
	s.Years = "2020-2024"
	s.Genres = "Drama"
	s.Rating = "70-100"
	p := newPopular(t, s, srv.Client())
	_, _ = p.Fetch(t.Context())
	q := capture[0].Query
	for _, want := range []string{"years=2020-2024", "genres=drama", "ratings=70-100", "limit=100"} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q: %s", want, q)
		}
	}
}

func TestPopularImport_Validate_YearlyListsRejected(t *testing.T) {
	t.Parallel()
	s := popularSettings("https://api.trakt.tv")
	s.TraktListType = int(trakt.PopularTopWatchedByYear)
	fails := s.Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "TraktListType" && strings.Contains(f.ErrorMessage, "Yearly") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Yearly-rejected failure, got %+v", fails)
	}
}

func TestPopularImport_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !importlists.Default().Has(trakt.PopularImplementation, trakt.PopularConfigContract) {
		t.Errorf("registry missing %s/%s", trakt.PopularImplementation, trakt.PopularConfigContract)
	}
}

// ------------------------------------------------------------------
// List variant
// ------------------------------------------------------------------

func listSettings(srvURL string) trakt.ListSettings {
	return trakt.ListSettings{
		SettingsBase: trakt.SettingsBase{
			BaseURL:      srvURL,
			AccessToken:  "tok",
			RefreshToken: "ref",
			Expires:      time.Now().Add(1 * time.Hour),
			Limit:        100,
		},
		Username: "user1",
		Listname: "My Custom List!",
	}
}

func newList(t *testing.T, s trakt.ListSettings, doer *http.Client) importlists.Provider {
	t.Helper()
	row := &importlists.ImportList{
		ID:             23,
		Name:           "TraktList",
		Implementation: trakt.ListImplementation,
		Settings:       ptrStr(t, s),
	}
	p, err := trakt.NewList(row, importlists.Dependencies{HTTPClient: doer, StatusService: &fakeStatusRecorder{}})
	if err != nil {
		t.Fatalf("NewList: %v", err)
	}
	return p
}

const listBody = `[{"show":{"title":"L A","ids":{"tvdb":900}}},{"show":{"title":"","ids":{}}}]`

func TestListImport_Fetch_SlugsListname(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/users/": listBody}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	p := newList(t, listSettings(srv.URL), srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// Empty Show entry filtered out → 1 result.
	if len(got.Series) != 1 || got.Series[0].Title != "L A" {
		t.Errorf("Series=%+v", got.Series)
	}
	if !strings.Contains(capture[0].Path, "/users/user1/lists/my-custom-list/items/show,season,episode") {
		t.Errorf("path=%q", capture[0].Path)
	}
}

func TestListImport_Validate(t *testing.T) {
	t.Parallel()
	s := trakt.ListSettings{
		SettingsBase: trakt.SettingsBase{
			BaseURL:      "https://api.trakt.tv",
			AccessToken:  "tok",
			RefreshToken: "ref",
			Expires:      time.Now(),
			Limit:        100,
		},
	}
	fails := s.Validate()
	have := map[string]bool{}
	for _, f := range fails {
		have[f.PropertyName] = true
	}
	if !have["Username"] || !have["Listname"] {
		t.Errorf("expected Username + Listname failures, got %+v", fails)
	}
}

func TestListImport_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !importlists.Default().Has(trakt.ListImplementation, trakt.ListConfigContract) {
		t.Errorf("registry missing %s/%s", trakt.ListImplementation, trakt.ListConfigContract)
	}
}

// ------------------------------------------------------------------
// Refresh-near-expiry (shared across variants — exercised via User)
// ------------------------------------------------------------------

func TestUserImport_Fetch_RefreshesNearExpiry(t *testing.T) {
	var capture []captured
	srv := traktServer(t, map[string]string{"/users/": "[]"}, false, &capture)
	defer srv.Close()
	defer setupBase(srv.URL)()
	s := userSettings(srv.URL)
	s.Expires = time.Now().Add(1 * time.Minute)
	p := newUser(t, s, srv.Client())
	if _, err := p.Fetch(t.Context()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(capture) < 2 {
		t.Fatalf("expected renew+list calls, got %+v", capture)
	}
	if !strings.Contains(capture[0].Path, "/v1/trakt_sonarr/renew") {
		t.Errorf("first call should be renew, got %q", capture[0].Path)
	}
}
