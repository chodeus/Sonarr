package gotify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/gotify"
	"sonarr.tv/sonarr/internal/series"
)

type capturedRequest struct {
	Path  string
	Query string
	Body  []byte
}

func capturingServer(t *testing.T, statusCode int, capture *[]capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*capture = append(*capture, capturedRequest{Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})
		if statusCode > 0 {
			w.WriteHeader(statusCode)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func settingsJSON(t *testing.T, s gotify.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeGotify(t *testing.T, srv *httptest.Server, extras func(*gotify.Settings)) notifications.Provider {
	t.Helper()
	s := gotify.Settings{
		Server:                srv.URL,
		AppToken:              "token-xyz",
		Priority:              int(gotify.PriorityNormal),
		PreferredMetadataLink: int(notifications.MetadataLinkTvdb),
	}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: gotify.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := gotify.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresServerAndAppToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    gotify.Settings
		want string
	}{
		{"MissingServer", gotify.Settings{AppToken: "x"}, "Server"},
		{"BadServerScheme", gotify.Settings{Server: "ftp://x", AppToken: "x"}, "Server"},
		{"MissingAppToken", gotify.Settings{Server: "https://gotify"}, "AppToken"},
		{"OK", gotify.Settings{Server: "https://gotify", AppToken: "x"}, ""},
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

func TestSettings_PreferredMetadataLinkMustBeSelected(t *testing.T) {
	t.Parallel()
	s := gotify.Settings{
		Server:                "https://gotify",
		AppToken:              "x",
		MetadataLinks:         []int{int(notifications.MetadataLinkImdb)},
		PreferredMetadataLink: int(notifications.MetadataLinkTvdb),
	}
	fails := s.Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "PreferredMetadataLink" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected PreferredMetadataLink failure, got %+v", fails)
	}
}

func TestGotify_OnGrab_PostsPlainTextBody(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeGotify(t, srv, nil)
	msg := notifications.GrabMessage{
		Series:  &series.Series{Title: "Show"},
		Message: "Show.S01E01",
	}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls = %d", len(capture))
	}
	if got := capture[0].Path; got != "/message" {
		t.Errorf("path = %q", got)
	}
	if got := capture[0].Query; got != "token=token-xyz" {
		t.Errorf("query = %q", got)
	}
	var p gotify.Payload
	if err := json.Unmarshal(capture[0].Body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Title != "Episode Grabbed" {
		t.Errorf("Title = %q", p.Title)
	}
	if !strings.Contains(p.Message, "Show.S01E01") {
		t.Errorf("Message missing body: %q", p.Message)
	}
	if p.Priority != int(gotify.PriorityNormal) {
		t.Errorf("Priority = %d", p.Priority)
	}
	if p.Extras == nil || p.Extras.ClientDisplay == nil || p.Extras.ClientDisplay.ContentType != "text/plain" {
		t.Errorf("expected text/plain content type, got %+v", p.Extras)
	}
}

func TestGotify_MetadataLinks_RenderAsMarkdownAndSetClickURL(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeGotify(t, srv, func(s *gotify.Settings) {
		s.MetadataLinks = []int{
			int(notifications.MetadataLinkImdb),
			int(notifications.MetadataLinkTvdb),
		}
		s.PreferredMetadataLink = int(notifications.MetadataLinkTvdb)
	})
	msg := notifications.GrabMessage{
		Series: &series.Series{Title: "Show", ImdbID: "tt100", TvdbID: 7},
	}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	var p gotify.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if !strings.Contains(p.Message, "[IMDb](https://www.imdb.com/title/tt100)") {
		t.Errorf("missing IMDb link: %q", p.Message)
	}
	if !strings.Contains(p.Message, "[TVDb](http://www.thetvdb.com/?tab=series&id=7)") {
		t.Errorf("missing TVDb link: %q", p.Message)
	}
	if p.Extras == nil || p.Extras.ClientDisplay == nil || p.Extras.ClientDisplay.ContentType != "text/markdown" {
		t.Errorf("expected markdown content type, got %+v", p.Extras)
	}
	if p.Extras.ClientNotification == nil || p.Extras.ClientNotification.Click == nil {
		t.Fatalf("expected click set, got %+v", p.Extras)
	}
	if got := p.Extras.ClientNotification.Click.URL; got != "http://www.thetvdb.com/?tab=series&id=7" {
		t.Errorf("Click.URL = %q", got)
	}
}

func TestGotify_NilSeries_SkipsMetadataLinks(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeGotify(t, srv, func(s *gotify.Settings) {
		s.MetadataLinks = []int{int(notifications.MetadataLinkImdb)}
		s.PreferredMetadataLink = int(notifications.MetadataLinkImdb)
	})
	if err := prov.OnHealthIssue(t.Context(), notifications.HealthCheckMessage{
		Check: healthcheck.HealthCheck{Message: "boom"},
	}); err != nil {
		t.Fatalf("OnHealthIssue: %v", err)
	}
	var p gotify.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if strings.Contains(p.Message, "IMDb") {
		t.Errorf("expected no metadata link without series, got %q", p.Message)
	}
	if p.Extras == nil || p.Extras.ClientDisplay == nil || p.Extras.ClientDisplay.ContentType != "text/plain" {
		t.Errorf("expected text/plain when no markdown content, got %+v", p.Extras)
	}
}

func TestGotify_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeGotify(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false (C# parity)")
	}
}

func TestGotify_OnHealthRestored_PrefixesResolvedMessage(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeGotify(t, srv, nil)
	if err := prov.OnHealthRestored(t.Context(), notifications.HealthCheckMessage{
		Check: healthcheck.HealthCheck{Message: "disk-ok"},
	}); err != nil {
		t.Fatalf("OnHealthRestored: %v", err)
	}
	var p gotify.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if !strings.Contains(p.Message, "The following issue is now resolved: disk-ok") {
		t.Errorf("missing resolved prefix: %q", p.Message)
	}
}

func TestGotify_Test_SuccessRoundTrip(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeGotify(t, srv, nil)
	failures, err := prov.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
	var p gotify.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if p.Title != "Test Notification" {
		t.Errorf("Title = %q", p.Title)
	}
	if !strings.Contains(p.Message, "This is a test message from Sonarr") {
		t.Errorf("missing body: %q", p.Message)
	}
}

func TestGotify_Test_WithMetadataLinks_AppendsSonarrLink(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeGotify(t, srv, func(s *gotify.Settings) {
		s.MetadataLinks = []int{int(notifications.MetadataLinkImdb)}
		s.PreferredMetadataLink = int(notifications.MetadataLinkImdb)
	})
	failures, _ := prov.Test(t.Context())
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
	var p gotify.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if !strings.Contains(p.Message, "[Sonarr.tv](https://sonarr.tv)") {
		t.Errorf("missing Sonarr.tv link: %q", p.Message)
	}
	if p.Extras.ClientNotification == nil || p.Extras.ClientNotification.Click == nil ||
		p.Extras.ClientNotification.Click.URL != "https://sonarr.tv" {
		t.Errorf("missing Sonarr.tv click: %+v", p.Extras)
	}
	if p.Extras.ClientDisplay.ContentType != "text/markdown" {
		t.Errorf("expected markdown, got %q", p.Extras.ClientDisplay.ContentType)
	}
}

func TestGotify_Test_WithIncludeSeriesPoster_SetsImage(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeGotify(t, srv, func(s *gotify.Settings) {
		s.IncludeSeriesPoster = true
	})
	_, _ = prov.Test(t.Context())
	var p gotify.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if p.Extras.ClientNotification == nil || p.Extras.ClientNotification.BigImageURL == "" {
		t.Errorf("expected BigImageURL set, got %+v", p.Extras)
	}
}

func TestGotify_UnauthorizedSurfacesSentinel(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, http.StatusUnauthorized, &capture)
	defer srv.Close()
	prov := makeGotify(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, gotify.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestGotify_500SurfacesSendFailed(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeGotify(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, gotify.ErrSendFailed) {
		t.Errorf("err = %v, want ErrSendFailed", err)
	}
}

func TestGotify_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(gotify.Implementation, gotify.ConfigContract) {
		t.Errorf("registry missing %s/%s", gotify.Implementation, gotify.ConfigContract)
	}
}
