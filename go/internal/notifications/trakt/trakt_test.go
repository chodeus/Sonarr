package trakt_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/trakt"
	"sonarr.tv/sonarr/internal/series"
)

type captured struct {
	Path       string
	Auth       string
	APIVersion string
	APIKey     string
	Body       []byte
}

func capturingServer(t *testing.T, routes map[string]int, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*capture = append(*capture, captured{
			Path:       r.URL.Path,
			Auth:       r.Header.Get("Authorization"),
			APIVersion: r.Header.Get("trakt-api-version"),
			APIKey:     r.Header.Get("trakt-api-key"),
			Body:       body,
		})
		if code, ok := routes[r.URL.Path]; ok && code > 0 {
			w.WriteHeader(code)
			return
		}
		// Default OK responses with mock bodies for known endpoints.
		switch {
		case r.URL.Path == "/users/settings":
			_, _ = w.Write([]byte(`{"user":{"ids":{"slug":"sonarruser"}}}`))
		case strings.Contains(r.URL.Path, "/v1/trakt_sonarr/renew"):
			_, _ = w.Write([]byte(`{"access_token":"new-token","token_type":"bearer","expires_in":3600,"refresh_token":"new-refresh","scope":"public"}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
}

func settingsJSON(t *testing.T, s trakt.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeTrakt(t *testing.T, srv *httptest.Server, extras func(*trakt.Settings)) notifications.Provider {
	t.Helper()
	s := trakt.Settings{
		AccessToken:  "tok",
		RefreshToken: "ref",
		Expires:      time.Now().Add(1 * time.Hour),
	}
	if extras != nil {
		extras(&s)
	}
	trakt.SetURLsForTest(srv.URL, srv.URL+"/v1/trakt_sonarr/renew")
	t.Cleanup(func() {
		trakt.SetURLsForTest("https://api.trakt.tv", "https://auth.servarr.com/v1/trakt_sonarr/renew")
	})
	def := &notifications.Notification{
		Implementation: trakt.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := trakt.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    trakt.Settings
		want string
	}{
		{"NoAccessToken", trakt.Settings{RefreshToken: "r", Expires: time.Now()}, "AccessToken"},
		{"NoRefreshToken", trakt.Settings{AccessToken: "t", Expires: time.Now()}, "RefreshToken"},
		{"NoExpires", trakt.Settings{AccessToken: "t", RefreshToken: "r"}, "Expires"},
		{"OK", trakt.Settings{AccessToken: "t", RefreshToken: "r", Expires: time.Now()}, ""},
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

func TestTrakt_OnSeriesAdd_PostsCollectionPayload(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	msg := notifications.SeriesAddMessage{Series: &series.Series{Title: "Show", Year: 2024, TvdbID: 7, ImdbID: "tt001"}}
	if err := prov.OnSeriesAdd(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls=%d", len(capture))
	}
	if capture[0].Path != "/sync/collection" {
		t.Errorf("path=%q", capture[0].Path)
	}
	if capture[0].Auth != "Bearer tok" {
		t.Errorf("auth=%q", capture[0].Auth)
	}
	if capture[0].APIVersion != "2" || capture[0].APIKey == "" {
		t.Errorf("trakt headers missing: api-version=%q api-key=%q", capture[0].APIVersion, capture[0].APIKey)
	}
	var p trakt.CollectShowsPayload
	if err := json.Unmarshal(capture[0].Body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Shows) != 1 || p.Shows[0].Title != "Show" {
		t.Errorf("Shows=%+v", p.Shows)
	}
	if p.Shows[0].IDs.Tvdb != 7 || p.Shows[0].IDs.Imdb != "tt001" {
		t.Errorf("IDs=%+v", p.Shows[0].IDs)
	}
}

func TestTrakt_OnSeriesDelete_PostsRemovePath(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	msg := notifications.SeriesDeleteMessage{Series: &series.Series{Title: "Show"}}
	if err := prov.OnSeriesDelete(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesDelete: %v", err)
	}
	if capture[0].Path != "/sync/collection/remove" {
		t.Errorf("path=%q", capture[0].Path)
	}
}

func TestTrakt_OnDownload_RefreshesNearExpiry(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	// Expires in 1 minute — inside the 5-minute refresh window.
	prov := makeTrakt(t, srv, func(s *trakt.Settings) {
		s.Expires = time.Now().Add(1 * time.Minute)
	})
	msg := notifications.DownloadMessage{
		Series:      &series.Series{Title: "Show"},
		EpisodeFile: &episodefiles.File{RelativePath: "S01/E01.mkv"},
	}
	if err := prov.OnDownload(t.Context(), msg); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	// Expect renew call + addToCollection call.
	if len(capture) < 2 {
		t.Fatalf("calls=%d want >=2: %+v", len(capture), capture)
	}
	if !strings.Contains(capture[0].Path, "/v1/trakt_sonarr/renew") {
		t.Errorf("first call should be renew, got %q", capture[0].Path)
	}
	// Second call should now carry the new bearer token.
	if capture[1].Auth != "Bearer new-token" {
		t.Errorf("expected refreshed bearer, got %q", capture[1].Auth)
	}
}

func TestTrakt_OnSeriesAdd_NoRefreshWhenExpiryFarOut(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	msg := notifications.SeriesAddMessage{Series: &series.Series{Title: "Show"}}
	_ = prov.OnSeriesAdd(t.Context(), msg)
	if len(capture) != 1 {
		t.Fatalf("calls=%d want 1 (no refresh): %+v", len(capture), capture)
	}
	if strings.Contains(capture[0].Path, "renew") {
		t.Errorf("did not expect renew call: %+v", capture[0])
	}
}

func TestTrakt_OnGrab_IsNoop(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{}); err != nil {
		t.Errorf("OnGrab: %v", err)
	}
	if len(capture) != 0 {
		t.Errorf("OnGrab should be no-op, got %+v", capture)
	}
	if prov.SupportsOnGrab() {
		t.Error("SupportsOnGrab should be false")
	}
}

func TestTrakt_OnRename_IsNoop(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename: %v", err)
	}
	if len(capture) != 0 {
		t.Errorf("OnRename should be no-op, got %+v", capture)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestTrakt_Test_401MapsToTokenProperty(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, map[string]int{"/users/settings": http.StatusUnauthorized}, &capture)
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "Token" {
		t.Errorf("expected Token failure, got %+v", failures)
	}
}

func TestTrakt_Test_SuccessReturnsZeroFailures(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	failures, err := prov.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
}

func TestTrakt_500SurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, map[string]int{"/sync/collection": http.StatusInternalServerError}, &capture)
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	msg := notifications.SeriesAddMessage{Series: &series.Series{Title: "Show"}}
	err := prov.OnSeriesAdd(t.Context(), msg)
	if !errors.Is(err, trakt.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestTrakt_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(trakt.Implementation, trakt.ConfigContract) {
		t.Errorf("registry missing %s/%s", trakt.Implementation, trakt.ConfigContract)
	}
}

// fakeRepo records Update calls + the AccessToken/RefreshToken from
// the def.Settings JSON at the time of each call. Used to verify
// the persister fires post-refresh and that the row-side Settings
// JSON carries the refreshed token triple.
type fakeRepo struct {
	calls []recordedUpdate
}

type recordedUpdate struct {
	ID       int
	Settings string
}

func (r *fakeRepo) Update(_ context.Context, def *notifications.Notification) error {
	r.calls = append(r.calls, recordedUpdate{ID: def.ID, Settings: def.Settings})
	return nil
}

func TestTrakt_RefreshPersistsNewTokenToRepo(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	trakt.SetURLsForTest(srv.URL, srv.URL+"/v1/trakt_sonarr/renew")
	t.Cleanup(func() {
		trakt.SetURLsForTest("https://api.trakt.tv", "https://auth.servarr.com/v1/trakt_sonarr/renew")
	})

	// Settings expire in 1 minute → inside the 5-minute refresh window.
	s := trakt.Settings{
		AccessToken:  "old-token",
		RefreshToken: "old-refresh",
		Expires:      time.Now().Add(1 * time.Minute),
	}
	def := &notifications.Notification{
		ID:             42,
		Implementation: trakt.Implementation,
		Settings:       settingsJSON(t, s),
	}
	repo := &fakeRepo{}
	p, err := trakt.New(def, notifications.Dependencies{
		HTTPClient:        srv.Client(),
		NotificationsRepo: repo,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Fire OnSeriesAdd → triggers refresh → triggers persister.
	if err := p.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: &series.Series{Title: "Show"}}); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}

	if len(repo.calls) != 1 {
		t.Fatalf("repo Update calls = %d, want 1", len(repo.calls))
	}
	got := repo.calls[0]
	if got.ID != 42 {
		t.Errorf("repo update fired against ID=%d, want 42", got.ID)
	}
	var persisted trakt.Settings
	if err := json.Unmarshal([]byte(got.Settings), &persisted); err != nil {
		t.Fatalf("unmarshal persisted: %v", err)
	}
	if persisted.AccessToken != "new-token" {
		t.Errorf("persisted AccessToken=%q want new-token", persisted.AccessToken)
	}
	if persisted.RefreshToken != "new-refresh" {
		t.Errorf("persisted RefreshToken=%q want new-refresh", persisted.RefreshToken)
	}
	if persisted.Expires.Before(time.Now()) {
		t.Errorf("persisted Expires should be in the future, got %v", persisted.Expires)
	}
}

func TestTrakt_RefreshSkipsPersisterWhenNotWired(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	// Use makeTrakt which doesn't wire a repo — refresh should still
	// succeed in-memory without panicking.
	prov := makeTrakt(t, srv, func(s *trakt.Settings) {
		s.Expires = time.Now().Add(1 * time.Minute)
	})
	if err := prov.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: &series.Series{Title: "Show"}}); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	// The refresh + collection POST should both have fired.
	if len(capture) < 2 {
		t.Errorf("expected refresh + collection calls, got %d", len(capture))
	}
}

func TestTrakt_RefreshSkipsPersisterWhenRowIDZero(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	trakt.SetURLsForTest(srv.URL, srv.URL+"/v1/trakt_sonarr/renew")
	t.Cleanup(func() {
		trakt.SetURLsForTest("https://api.trakt.tv", "https://auth.servarr.com/v1/trakt_sonarr/renew")
	})

	s := trakt.Settings{
		AccessToken:  "tok",
		RefreshToken: "ref",
		Expires:      time.Now().Add(1 * time.Minute),
	}
	def := &notifications.Notification{
		// ID = 0 — the C# `Definition.Id > 0` guard at `:153` skips
		// the persistence call.
		Implementation: trakt.Implementation,
		Settings:       settingsJSON(t, s),
	}
	repo := &fakeRepo{}
	p, err := trakt.New(def, notifications.Dependencies{
		HTTPClient:        srv.Client(),
		NotificationsRepo: repo,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: &series.Series{Title: "Show"}}); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	if len(repo.calls) != 0 {
		t.Errorf("repo Update should not fire when row ID == 0, got %d", len(repo.calls))
	}
}

func TestTrakt_RequestAction_StartOAuth_BuildsAuthorizeURL(t *testing.T) {
	srv := capturingServer(t, nil, &[]captured{})
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "startOAuth", map[string]string{"callbackUrl": "http://example.com/cb"})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", out)
	}
	urlStr, _ := m["OauthUrl"].(string)
	if !strings.HasPrefix(urlStr, "https://trakt.tv/oauth/authorize?") {
		t.Errorf("OauthUrl=%q", urlStr)
	}
	if !strings.Contains(urlStr, "response_type=code") || !strings.Contains(urlStr, "redirect_uri=https%3A%2F%2Fauth.servarr.com") || !strings.Contains(urlStr, "state=http%3A%2F%2Fexample.com%2Fcb") || !strings.Contains(urlStr, "client_id=d44ba57") {
		t.Errorf("OauthUrl missing required params: %s", urlStr)
	}
}

func TestTrakt_RequestAction_StartOAuth_MissingCallback(t *testing.T) {
	srv := capturingServer(t, nil, &[]captured{})
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	_, err := prov.RequestAction(t.Context(), "startOAuth", map[string]string{})
	if !errors.Is(err, trakt.ErrBadRequest) {
		t.Errorf("err=%v want ErrBadRequest", err)
	}
}

func TestTrakt_RequestAction_GetOAuthToken_ResolvesTuple(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	before := time.Now()
	out, err := prov.RequestAction(t.Context(), "getOAuthToken", map[string]string{
		"access_token":  "new-tok",
		"expires_in":    "3600",
		"refresh_token": "new-ref",
	})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out)
	}
	if m["accessToken"] != "new-tok" {
		t.Errorf("accessToken=%v", m["accessToken"])
	}
	if m["refreshToken"] != "new-ref" {
		t.Errorf("refreshToken=%v", m["refreshToken"])
	}
	if m["authUser"] != "sonarruser" {
		t.Errorf("authUser=%v want sonarruser (fetched via GetUserName)", m["authUser"])
	}
	expires, ok := m["expires"].(time.Time)
	if !ok {
		t.Fatalf("expires not time.Time: %T", m["expires"])
	}
	want := before.Add(3600 * time.Second)
	if expires.Before(want.Add(-2*time.Second)) || expires.After(want.Add(5*time.Second)) {
		t.Errorf("expires=%v not within window around %v", expires, want)
	}
	// Verify /users/settings was called with the supplied token.
	var settingsCall *captured
	for i := range capture {
		if capture[i].Path == "/users/settings" {
			settingsCall = &capture[i]
			break
		}
	}
	if settingsCall == nil {
		t.Fatalf("getOAuthToken should call /users/settings, captures=%+v", capture)
	}
	if settingsCall.Auth != "Bearer new-tok" {
		t.Errorf("Authorization=%q want Bearer new-tok", settingsCall.Auth)
	}
}

func TestTrakt_RequestAction_GetOAuthToken_MissingFields(t *testing.T) {
	srv := capturingServer(t, nil, &[]captured{})
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	cases := []map[string]string{
		{"expires_in": "3600", "refresh_token": "x"},                     // missing access_token
		{"access_token": "x", "refresh_token": "x"},                      // missing expires_in
		{"access_token": "x", "expires_in": "3600"},                      // missing refresh_token
		{"access_token": "x", "expires_in": "bad", "refresh_token": "x"}, // non-int expires_in
		{"access_token": "x", "expires_in": "0", "refresh_token": "x"},   // zero expires_in
	}
	for i, q := range cases {
		_, err := prov.RequestAction(t.Context(), "getOAuthToken", q)
		if !errors.Is(err, trakt.ErrBadRequest) {
			t.Errorf("case %d: err=%v want ErrBadRequest", i, err)
		}
	}
}

func TestTrakt_RequestAction_UnknownActionReturnsEmpty(t *testing.T) {
	srv := capturingServer(t, nil, &[]captured{})
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "bogusAction", nil)
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || len(m) != 0 {
		t.Errorf("unknown action should return empty map, got %T %+v", out, out)
	}
}

func TestTrakt_RequestAction_GetOAuthToken_UserSettingsErrorFallsBack(t *testing.T) {
	srv := capturingServer(t, map[string]int{"/users/settings": http.StatusInternalServerError}, &[]captured{})
	defer srv.Close()
	prov := makeTrakt(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "getOAuthToken", map[string]string{
		"access_token":  "x",
		"expires_in":    "3600",
		"refresh_token": "x",
	})
	if err != nil {
		t.Fatalf("user-settings failure should NOT propagate: %v", err)
	}
	m := out.(map[string]any)
	if m["authUser"] != "" {
		t.Errorf("authUser=%v want empty fallback", m["authUser"])
	}
}
