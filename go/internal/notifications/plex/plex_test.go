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
	if got := capture[0].Query.Get("X-Plex-Token"); got != "tok" {
		t.Errorf("X-Plex-Token=%q want tok", got)
	}
	if capture[0].Query.Get("X-Plex-Client-Identifier") == "" {
		t.Error("X-Plex-Client-Identifier missing")
	}
}

func TestPlex_500SurfacesSendFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	err := prov.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: &series.Series{Path: "/srv/tv/Show"}})
	if !errors.Is(err, plex.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestPlex_APIError_SurfacesTypedSentinel(t *testing.T) {
	var capture []captured
	srv := plexServer(t, `{"MediaContainer":{"error":"section not found"}}`, false, &capture)
	defer srv.Close()
	prov := makePlex(t, srv, nil)
	err := prov.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: &series.Series{Path: "/srv/tv/Show"}})
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
