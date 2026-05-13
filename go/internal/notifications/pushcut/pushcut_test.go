package pushcut_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/pushcut"
	"sonarr.tv/sonarr/internal/series"
)

type captured struct {
	Path      string
	APIKey    string
	BodyBytes []byte
}

func capturingServer(t *testing.T, statusCode int, body string, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*capture = append(*capture, captured{
			Path:      r.URL.Path,
			APIKey:    r.Header.Get("API-Key"),
			BodyBytes: b,
		})
		if statusCode > 0 {
			w.WriteHeader(statusCode)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
}

func settingsJSON(t *testing.T, s pushcut.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makePushcut(t *testing.T, srv *httptest.Server, extras func(*pushcut.Settings)) notifications.Provider {
	t.Helper()
	s := pushcut.Settings{
		ApiKey:           "key-xyz",
		NotificationName: "Sonarr",
	}
	if extras != nil {
		extras(&s)
	}
	// Override base URL via test seam. We bypass the constant by
	// routing all requests through a custom transport that rewrites
	// scheme+host (pushcut precedent matches the slack approach —
	// the base URL is variable so swap it directly).
	def := &notifications.Notification{
		Implementation: pushcut.Implementation,
		Settings:       settingsJSON(t, s),
	}
	pushcut.SetAPIBaseURLForTest(srv.URL)
	t.Cleanup(func() { pushcut.SetAPIBaseURLForTest("https://api.pushcut.io") })
	p, err := pushcut.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresApiKeyAndNotificationName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    pushcut.Settings
		want string
	}{
		{"NoApi", pushcut.Settings{NotificationName: "n"}, "ApiKey"},
		{"NoName", pushcut.Settings{ApiKey: "x"}, "NotificationName"},
		{"OK", pushcut.Settings{ApiKey: "x", NotificationName: "n"}, ""},
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

func TestPushcut_OnGrab_PostsJSONPayload(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, func(s *pushcut.Settings) {
		s.TimeSensitive = true
		s.MetadataLinks = []int{int(notifications.MetadataLinkImdb)}
	})
	msg := notifications.GrabMessage{
		Series:  &series.Series{ImdbID: "tt001"},
		Message: "Show.S01E01",
	}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls = %d", len(capture))
	}
	if capture[0].APIKey != "key-xyz" {
		t.Errorf("API-Key=%q", capture[0].APIKey)
	}
	if capture[0].Path != "/v1/notifications/Sonarr" {
		t.Errorf("path=%q", capture[0].Path)
	}
	var p pushcut.Payload
	if err := json.Unmarshal(capture[0].BodyBytes, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Title != "Episode Grabbed" {
		t.Errorf("Title=%q", p.Title)
	}
	if p.Text != "Show.S01E01" {
		t.Errorf("Text=%q", p.Text)
	}
	if p.IsTimeSensitive == nil || !*p.IsTimeSensitive {
		t.Errorf("IsTimeSensitive=%v", p.IsTimeSensitive)
	}
	if len(p.Actions) != 1 || p.Actions[0].URL != "https://www.imdb.com/title/tt001" {
		t.Errorf("Actions=%+v", p.Actions)
	}
}

func TestPushcut_OnSeriesAdd_ComposesBody(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, nil)
	msg := notifications.SeriesAddMessage{Series: &series.Series{Title: "Show"}}
	if err := prov.OnSeriesAdd(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	var p pushcut.Payload
	_ = json.Unmarshal(capture[0].BodyBytes, &p)
	if p.Text != "Show added to library" {
		t.Errorf("Text=%q", p.Text)
	}
}

func TestPushcut_OnHealthIssue_UsesBrandedTitle(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, nil)
	if err := prov.OnHealthIssue(t.Context(), notifications.HealthCheckMessage{
		Check: healthcheck.HealthCheck{Message: "boom"},
	}); err != nil {
		t.Fatalf("OnHealthIssue: %v", err)
	}
	var p pushcut.Payload
	_ = json.Unmarshal(capture[0].BodyBytes, &p)
	if p.Title != "Sonarr - Health Check Failure" {
		t.Errorf("Title=%q", p.Title)
	}
}

func TestPushcut_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makePushcut(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestPushcut_Test_403MapsToAPIKey(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusForbidden, "", &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "API Key" {
		t.Errorf("expected API Key failure, got %+v", failures)
	}
}

func TestPushcut_Test_BadRequestWithErrorBodyMapsToUrl(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusBadRequest, `{"error":"notification not found"}`, &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "Url" {
		t.Errorf("expected Url failure, got %+v", failures)
	}
}

func TestPushcut_OnGrab_500SurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, "", &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, pushcut.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestPushcut_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(pushcut.Implementation, pushcut.ConfigContract) {
		t.Errorf("registry missing %s/%s", pushcut.Implementation, pushcut.ConfigContract)
	}
}

const seriesImagesPosterFanart = `[{"coverType":1,"url":"/MediaCover/9/poster.jpg","remoteUrl":"https://cdn/poster.jpg"},{"coverType":3,"url":"/MediaCover/9/fanart.jpg","remoteUrl":"https://cdn/fanart.jpg"}]`

func TestPushcut_OnGrab_IncludesPosterWhenEnabled(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, func(s *pushcut.Settings) {
		s.IncludePoster = true
	})
	msg := notifications.GrabMessage{
		Series:  &series.Series{Title: "Show", Images: seriesImagesPosterFanart},
		Message: "Show.S01E01",
	}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	var p pushcut.Payload
	if err := json.Unmarshal(capture[0].BodyBytes, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Image != "https://cdn/poster.jpg" {
		t.Errorf("Image=%q want cdn/poster.jpg", p.Image)
	}
}

func TestPushcut_OnGrab_OmitsPosterWhenDisabled(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, nil) // IncludePoster default false
	msg := notifications.GrabMessage{
		Series:  &series.Series{Images: seriesImagesPosterFanart},
		Message: "x",
	}
	_ = prov.OnGrab(t.Context(), msg)
	var p pushcut.Payload
	_ = json.Unmarshal(capture[0].BodyBytes, &p)
	if p.Image != "" {
		t.Errorf("Image=%q want empty (IncludePoster off)", p.Image)
	}
}

func TestPushcut_OnGrab_NoPosterWhenSeriesHasNoCoverEntry(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, func(s *pushcut.Settings) { s.IncludePoster = true })
	// Banner only — no poster row.
	msg := notifications.GrabMessage{
		Series:  &series.Series{Images: `[{"coverType":2,"remoteUrl":"https://cdn/banner.jpg"}]`},
		Message: "x",
	}
	_ = prov.OnGrab(t.Context(), msg)
	var p pushcut.Payload
	_ = json.Unmarshal(capture[0].BodyBytes, &p)
	if p.Image != "" {
		t.Errorf("Image=%q want empty (no poster in series)", p.Image)
	}
}

func TestPushcut_OnGrab_BadImagesBlobNoFailure(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makePushcut(t, srv, func(s *pushcut.Settings) { s.IncludePoster = true })
	msg := notifications.GrabMessage{
		Series:  &series.Series{Images: "not valid json"},
		Message: "x",
	}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab should not fail on bad Images blob: %v", err)
	}
	var p pushcut.Payload
	_ = json.Unmarshal(capture[0].BodyBytes, &p)
	if p.Image != "" {
		t.Errorf("Image=%q want empty fallback on bad blob", p.Image)
	}
}
