package apprise_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/apprise"
)

type captured struct {
	Path       string
	AuthHeader string
	Body       []byte
}

func capturingServer(t *testing.T, statusCode int, body string, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*capture = append(*capture, captured{
			Path:       r.URL.Path,
			AuthHeader: r.Header.Get("Authorization"),
			Body:       b,
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

func settingsJSON(t *testing.T, s apprise.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeApprise(t *testing.T, srv *httptest.Server, extras func(*apprise.Settings)) notifications.Provider {
	t.Helper()
	s := apprise.Settings{
		ServerUrl:        srv.URL,
		ConfigurationKey: "sonarr-cfg",
		NotificationType: int(apprise.NotificationTypeInfo),
	}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: apprise.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := apprise.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequireServerAndKeyOrStateless(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    apprise.Settings
		want string
	}{
		{"NoServer", apprise.Settings{ConfigurationKey: "k"}, "ServerUrl"},
		{"NeitherKeyNorStateless", apprise.Settings{ServerUrl: "http://x"}, "ConfigurationKey"},
		{"BothKeyAndStateless", apprise.Settings{ServerUrl: "http://x", ConfigurationKey: "k", StatelessUrls: "smtp://y"}, "StatelessUrls"},
		{"BadKeyChars", apprise.Settings{ServerUrl: "http://x", ConfigurationKey: "BAD!"}, "ConfigurationKey"},
		{"TagsWithStateless", apprise.Settings{ServerUrl: "http://x", StatelessUrls: "smtp://y", Tags: []string{"t"}}, "Tags"},
		{"OKKey", apprise.Settings{ServerUrl: "http://x", ConfigurationKey: "k"}, ""},
		{"OKStateless", apprise.Settings{ServerUrl: "http://x", StatelessUrls: "smtp://y"}, ""},
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

func TestApprise_OnGrab_ConfigurationKey_PostsToNotifyPath(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makeApprise(t, srv, nil)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "ok"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if capture[0].Path != "/notify/sonarr-cfg" {
		t.Errorf("path=%q", capture[0].Path)
	}
	var p apprise.Payload
	if err := json.Unmarshal(capture[0].Body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Title != "Episode Grabbed" {
		t.Errorf("Title=%q", p.Title)
	}
	if p.Type != "info" {
		t.Errorf("Type=%q", p.Type)
	}
	if p.Urls != "" {
		t.Errorf("Urls should be empty for ConfigurationKey path, got %q", p.Urls)
	}
}

func TestApprise_OnGrab_StatelessUrls_PostsUrlsPayload(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makeApprise(t, srv, func(s *apprise.Settings) {
		s.ConfigurationKey = ""
		s.StatelessUrls = "tgram://abc/xyz"
		s.NotificationType = int(apprise.NotificationTypeWarning)
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if capture[0].Path != "/notify" {
		t.Errorf("path=%q", capture[0].Path)
	}
	var p apprise.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if p.Urls != "tgram://abc/xyz" {
		t.Errorf("Urls=%q", p.Urls)
	}
	if p.Type != "warning" {
		t.Errorf("Type=%q", p.Type)
	}
}

func TestApprise_BasicAuth(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makeApprise(t, srv, func(s *apprise.Settings) {
		s.AuthUsername = "u"
		s.AuthPassword = "p"
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !strings.HasPrefix(capture[0].AuthHeader, "Basic ") {
		t.Errorf("expected Basic auth, got %q", capture[0].AuthHeader)
	}
}

func TestApprise_Tags_JoinedComma(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makeApprise(t, srv, func(s *apprise.Settings) {
		s.Tags = []string{"t1", "t2"}
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	var p apprise.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if p.Tag != "t1,t2" {
		t.Errorf("Tag=%q", p.Tag)
	}
}

func TestApprise_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeApprise(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestApprise_Test_401MapsToAuthUsername(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusUnauthorized, "", &capture)
	defer srv.Close()
	prov := makeApprise(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "AuthUsername" {
		t.Errorf("expected AuthUsername failure, got %+v", failures)
	}
}

func TestApprise_Test_APIErrorBodySurfaces(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusBadRequest, `{"error":"bad config"}`, &capture)
	defer srv.Close()
	prov := makeApprise(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 {
		t.Fatal("expected failures")
	}
	if !strings.Contains(failures[0].ErrorMessage, "bad config") {
		t.Errorf("error message missing API body: %+v", failures)
	}
}

func TestApprise_500SurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, "", &capture)
	defer srv.Close()
	prov := makeApprise(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, apprise.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestApprise_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(apprise.Implementation, apprise.ConfigContract) {
		t.Errorf("registry missing %s/%s", apprise.Implementation, apprise.ConfigContract)
	}
}
