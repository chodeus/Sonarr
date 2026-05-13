package plex_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/plex"
	"sonarr.tv/sonarr/internal/series"
)

type captured struct {
	Path  string
	Query url.Values
}

// plexServer returns an httptest server that dispatches per resource
// path. sectionsBody is the body returned for /library/sections;
// 401onSections=true triggers a 401 on the sections call.
func plexServer(t *testing.T, sectionsBody string, status401OnSections bool, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*capture = append(*capture, captured{Path: r.URL.Path, Query: r.URL.Query()})
		if status401OnSections && strings.HasSuffix(r.URL.Path, "/library/sections") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/library/sections"):
			if sectionsBody != "" {
				_, _ = w.Write([]byte(sectionsBody))
			} else {
				_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[]}}`))
			}
		case strings.HasSuffix(r.URL.Path, "/refresh"):
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

func settingsJSON(t *testing.T, s plex.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makePlex(t *testing.T, srv *httptest.Server, extras func(*plex.Settings)) notifications.Provider {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	s := plex.Settings{
		Host:          u.Hostname(),
		Port:          port,
		AuthToken:     "tok",
		UpdateLibrary: true,
	}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: plex.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := plex.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    plex.Settings
		want string
	}{
		{"NoHost", plex.Settings{Port: 32400}, "Host"},
		{"BadPort", plex.Settings{Host: "h", Port: 70000}, "Port"},
		{"MapFromWithoutMapTo", plex.Settings{Host: "h", Port: 32400, MapFrom: "/a"}, "MapTo"},
		{"OK", plex.Settings{Host: "h", Port: 32400}, ""},
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

const modernSectionsBody = `{"MediaContainer":{"Directory":[
  {"key":1,"type":"show","language":"en","Location":[{"id":10,"path":"/srv/tv"}]},
  {"key":2,"type":"movie","language":"en","Location":[{"id":20,"path":"/srv/movies"}]}
]}}`

const legacySectionsBody = `{"_children":[
  {"key":1,"type":"show","language":"en","_children":[{"id":10,"path":"/srv/tv"}]}
]}`

func TestPlex_OnSeriesAdd_MatchedSectionFiresRefresh(t *testing.T) {
	var capture []captured
	srv := plexServer(t, modernSectionsBody, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	msg := notifications.SeriesAddMessage{Series: &series.Series{Title: "Show", Path: "/srv/tv/Show"}}
	if err := prov.OnSeriesAdd(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	// OnSeriesAdd only queues — the section discovery + refresh fire
	// at the next ProcessQueue tick (mirrors C# MediaServerUpdateQueue).
	if len(capture) != 0 {
		t.Fatalf("OnSeriesAdd should defer HTTP work, got %+v", capture)
	}
	if err := prov.ProcessQueue(t.Context()); err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	// Expect: GET /library/sections, GET /library/sections/1/refresh
	if len(capture) != 2 {
		t.Fatalf("calls=%d want 2: %+v", len(capture), capture)
	}
	if !strings.HasSuffix(capture[0].Path, "/library/sections") {
		t.Errorf("first call=%q", capture[0].Path)
	}
	if !strings.HasSuffix(capture[1].Path, "/library/sections/1/refresh") {
		t.Errorf("second call=%q", capture[1].Path)
	}
	if got := capture[1].Query.Get("path"); got != "/srv/tv/Show" {
		t.Errorf("refresh path=%q want /srv/tv/Show", got)
	}
}

func TestPlex_PathMapping_TranslatesRootFolder(t *testing.T) {
	var capture []captured
	srv := plexServer(t, modernSectionsBody, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, func(s *plex.Settings) {
		s.MapFrom = "/local/tv"
		s.MapTo = "/srv/tv"
	})
	msg := notifications.SeriesAddMessage{Series: &series.Series{Title: "Show", Path: "/local/tv/Show"}}
	if err := prov.OnSeriesAdd(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	if err := prov.ProcessQueue(t.Context()); err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	// The mapped rootFolder /srv/tv should match section 1's Location;
	// refresh path uses the location root + series-relative.
	if !strings.HasSuffix(capture[1].Path, "/library/sections/1/refresh") {
		t.Errorf("refresh fired against wrong section: %+v", capture)
	}
	if got := capture[1].Query.Get("path"); got != "/srv/tv/Show" {
		t.Errorf("refresh path=%q want /srv/tv/Show", got)
	}
}

func TestPlex_NoMatch_FallsBackToAllSections(t *testing.T) {
	var capture []captured
	body := `{"MediaContainer":{"Directory":[
  {"key":1,"type":"show","language":"en","Location":[{"id":10,"path":"/different/root"}]}
]}}`
	srv := plexServer(t, body, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	msg := notifications.SeriesAddMessage{Series: &series.Series{Title: "Show", Path: "/srv/tv/Show"}}
	if err := prov.OnSeriesAdd(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	if err := prov.ProcessQueue(t.Context()); err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	// One sections call + one refresh against the lone section.
	if len(capture) != 2 || !strings.HasSuffix(capture[1].Path, "/library/sections/1/refresh") {
		t.Errorf("fallback should refresh the single non-matching section, got %+v", capture)
	}
}

func TestPlex_LegacyChildrenEnvelope_Parses(t *testing.T) {
	var capture []captured
	srv := plexServer(t, legacySectionsBody, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	msg := notifications.SeriesAddMessage{Series: &series.Series{Title: "Show", Path: "/srv/tv/Show"}}
	if err := prov.OnSeriesAdd(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	if err := prov.ProcessQueue(t.Context()); err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	if !strings.HasSuffix(capture[1].Path, "/library/sections/1/refresh") {
		t.Errorf("legacy envelope should yield same refresh call, got %+v", capture)
	}
}

func TestPlex_OnSeriesDelete_OnlyWhenDeletedFiles(t *testing.T) {
	var capture []captured
	srv := plexServer(t, modernSectionsBody, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	msg := notifications.SeriesDeleteMessage{Series: &series.Series{Title: "Show", Path: "/srv/tv/Show"}, DeletedFiles: false}
	if err := prov.OnSeriesDelete(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesDelete: %v", err)
	}
	if len(capture) != 0 {
		t.Errorf("DeletedFiles=false should be no-op, got %+v", capture)
	}
}

func TestPlex_OnGrab_IsNoop(t *testing.T) {
	var capture []captured
	srv := plexServer(t, modernSectionsBody, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 0 {
		t.Errorf("OnGrab should be no-op, got %+v", capture)
	}
	if prov.SupportsOnGrab() {
		t.Error("SupportsOnGrab should be false")
	}
}

func TestPlex_Test_401MapsToAuthToken(t *testing.T) {
	var capture []captured
	srv := plexServer(t, "", true, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "AuthToken" {
		t.Errorf("expected AuthToken failure, got %+v", failures)
	}
}

func TestPlex_Test_NoSectionsReportsHostFailure(t *testing.T) {
	var capture []captured
	srv := plexServer(t, `{"MediaContainer":{"Directory":[]}}`, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "Host" {
		t.Errorf("expected Host failure for empty sections, got %+v", failures)
	}
}

func TestPlex_Test_SuccessReturnsZeroFailures(t *testing.T) {
	var capture []captured
	srv := plexServer(t, modernSectionsBody, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	failures, err := prov.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
}

func TestPlex_XPlexTokenAttachedAsQueryParam(t *testing.T) {
	var capture []captured
	srv := plexServer(t, modernSectionsBody, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	_ = prov.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: &series.Series{Path: "/srv/tv/Show"}})
	_ = prov.ProcessQueue(t.Context())
	if got := capture[0].Query.Get("X-Plex-Token"); got != "tok" {
		t.Errorf("X-Plex-Token=%q want tok", got)
	}
	if capture[0].Query.Get("X-Plex-Client-Identifier") == "" {
		t.Error("X-Plex-Client-Identifier missing")
	}
}

func TestPlex_500SurfacesSendFailedFromProcessQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	if err := prov.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: &series.Series{Path: "/srv/tv/Show"}}); err != nil {
		t.Fatalf("OnSeriesAdd should only queue, got %v", err)
	}
	err := prov.ProcessQueue(t.Context())
	if !errors.Is(err, plex.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed (drain-time failure)", err)
	}
}

func TestPlex_APIError_SurfacesTypedSentinelFromProcessQueue(t *testing.T) {
	var capture []captured
	srv := plexServer(t, `{"MediaContainer":{"error":"section not found"}}`, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	if err := prov.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: &series.Series{Path: "/srv/tv/Show"}}); err != nil {
		t.Fatalf("OnSeriesAdd should only queue, got %v", err)
	}
	err := prov.ProcessQueue(t.Context())
	var apiErr *plex.ErrAPIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "section not found") {
		t.Errorf("err=%v want ErrAPIError with message", err)
	}
}

func TestPlex_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(plex.Implementation, plex.ConfigContract) {
		t.Errorf("registry missing %s/%s", plex.Implementation, plex.ConfigContract)
	}
}

// oauthServer dispatches /api/v2/pins/{id} GETs and records calls.
// authToken="" means the pin is not yet redeemed (returns
// {"authToken":""}); 500 simulates a server failure.
func oauthServer(t *testing.T, pinID int, authToken string, status int) (*httptest.Server, *[]string) {
	t.Helper()
	calls := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.Path+"?"+r.URL.RawQuery)
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		want := "/api/v2/pins/" + strconv.Itoa(pinID)
		if r.URL.Path == want {
			_, _ = w.Write([]byte(`{"authToken":"` + authToken + `"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	plex.SetOAuthURLsForTest(srv.URL, srv.URL)
	t.Cleanup(func() {
		plex.SetOAuthURLsForTest("https://plex.tv", "https://app.plex.tv")
	})
	return srv, calls
}

func TestPlex_RequestAction_StartOAuth_BuildsPinPostURL(t *testing.T) {
	srv, _ := oauthServer(t, 0, "", 0)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "startOAuth", nil)
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	r, ok := out.(plex.PinURLResponse)
	if !ok {
		t.Fatalf("expected PinURLResponse, got %T", out)
	}
	if !r.Poll {
		t.Errorf("Poll=false")
	}
	if !strings.Contains(r.URL, "/api/v2/pins?") {
		t.Errorf("URL=%q lacks /api/v2/pins suffix", r.URL)
	}
	if !strings.Contains(r.URL, "strong=true") || !strings.Contains(r.URL, "X-Plex-Product=Sonarr") {
		t.Errorf("URL=%q missing required params", r.URL)
	}
	if r.Headers["Accept"] != "application/json" {
		t.Errorf("Headers=%v", r.Headers)
	}
}

func TestPlex_RequestAction_ContinueOAuth_BuildsSignInURL(t *testing.T) {
	srv, _ := oauthServer(t, 0, "", 0)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "continueOAuth", map[string]string{
		"callbackUrl": "http://cb/return",
		"id":          "42",
		"code":        "abc123",
	})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	r, ok := out.(plex.SignInURLResponse)
	if !ok {
		t.Fatalf("expected SignInURLResponse, got %T", out)
	}
	if r.PinID != 42 {
		t.Errorf("PinID=%d want 42", r.PinID)
	}
	if !strings.Contains(r.OauthURL, "/auth/hashBang/#!?") {
		t.Errorf("OauthURL=%q missing hashBang anchor", r.OauthURL)
	}
	if !strings.Contains(r.OauthURL, "forwardUrl=http%3A%2F%2Fcb%2Freturn") || !strings.Contains(r.OauthURL, "code=abc123") {
		t.Errorf("OauthURL=%q missing required params", r.OauthURL)
	}
}

func TestPlex_RequestAction_ContinueOAuth_MissingParams(t *testing.T) {
	srv, _ := oauthServer(t, 0, "", 0)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	cases := []map[string]string{
		{"id": "1", "code": "x"},                        // missing callbackUrl
		{"callbackUrl": "cb", "code": "x"},              // missing id
		{"callbackUrl": "cb", "id": "1"},                // missing code
		{"callbackUrl": "cb", "id": "bad", "code": "x"}, // non-int id
	}
	for i, q := range cases {
		_, err := prov.RequestAction(t.Context(), "continueOAuth", q)
		if !errors.Is(err, plex.ErrBadRequest) {
			t.Errorf("case %d: err=%v want ErrBadRequest", i, err)
		}
	}
}

func TestPlex_RequestAction_PollOAuth_Success(t *testing.T) {
	srv, calls := oauthServer(t, 99, "redeemed-token", 0)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "pollOAuth", map[string]string{"pinId": "99"})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["success"] != true {
		t.Errorf("pollOAuth=%+v want {success:true}", out)
	}
	if len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "/api/v2/pins/99?") {
		t.Errorf("expected single GET /api/v2/pins/99, got %+v", *calls)
	}
}

func TestPlex_RequestAction_PollOAuth_PendingPinReturnsFalse(t *testing.T) {
	srv, _ := oauthServer(t, 99, "", 0)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	out, _ := prov.RequestAction(t.Context(), "pollOAuth", map[string]string{"pinId": "99"})
	m := out.(map[string]any)
	if m["success"] != false {
		t.Errorf("pollOAuth pending=%+v want {success:false}", out)
	}
}

func TestPlex_RequestAction_GetOAuthToken_ReturnsAuthToken(t *testing.T) {
	srv, _ := oauthServer(t, 99, "the-token", 0)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "getOAuthToken", map[string]string{"pinId": "99"})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := out.(map[string]any)
	// Field name MUST be `authToken` (not `accessToken`) — preserves
	// the C# notifications PlexServer at `:160` which differs from
	// the importlists PlexImport at the matching call-site.
	if !ok || m["authToken"] != "the-token" {
		t.Errorf("getOAuthToken=%+v want {authToken:the-token}", out)
	}
}

func TestPlex_RequestAction_PollOrGetOAuthToken_MissingPinId(t *testing.T) {
	srv, _ := oauthServer(t, 0, "", 0)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	for _, action := range []string{"pollOAuth", "getOAuthToken"} {
		_, err := prov.RequestAction(t.Context(), action, map[string]string{})
		if !errors.Is(err, plex.ErrBadRequest) {
			t.Errorf("%s missing pinId err=%v want ErrBadRequest", action, err)
		}
		_, err = prov.RequestAction(t.Context(), action, map[string]string{"pinId": "bad"})
		if !errors.Is(err, plex.ErrBadRequest) {
			t.Errorf("%s non-int pinId err=%v want ErrBadRequest", action, err)
		}
	}
}

func TestPlex_RequestAction_PollOAuth_ServerErrorPropagates(t *testing.T) {
	srv, _ := oauthServer(t, 99, "", http.StatusInternalServerError)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	_, err := prov.RequestAction(t.Context(), "pollOAuth", map[string]string{"pinId": "99"})
	if !errors.Is(err, plex.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestPlex_RequestAction_ServersAndUnknownReturnEmpty(t *testing.T) {
	srv, _ := oauthServer(t, 0, "", 0)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	for _, action := range []string{"servers", "bogus"} {
		out, err := prov.RequestAction(t.Context(), action, nil)
		if err != nil {
			t.Errorf("%s: %v", action, err)
		}
		m, ok := out.(map[string]any)
		if !ok || len(m) != 0 {
			t.Errorf("%s should return empty map, got %T %+v", action, out, out)
		}
	}
}
