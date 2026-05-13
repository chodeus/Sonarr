package ntfy_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/ntfy"
)

type captured struct {
	Path       string
	RawQuery   string
	AuthHeader string
}

func capturingServer(t *testing.T, statusCode int, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*capture = append(*capture, captured{
			Path:       r.URL.Path,
			RawQuery:   r.URL.RawQuery,
			AuthHeader: r.Header.Get("Authorization"),
		})
		if statusCode > 0 {
			w.WriteHeader(statusCode)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func settingsJSON(t *testing.T, s ntfy.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeNtfy(t *testing.T, srv *httptest.Server, extras func(*ntfy.Settings)) notifications.Provider {
	t.Helper()
	s := ntfy.Settings{
		ServerUrl: srv.URL,
		Topics:    []string{"sonarr"},
		Priority:  int(ntfy.PriorityDefault),
	}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: ntfy.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := ntfy.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresTopicsAndPriority(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    ntfy.Settings
		want string
	}{
		{"NoTopics", ntfy.Settings{Priority: 3}, "Topics"},
		{"PriorityOutOfRange", ntfy.Settings{Topics: []string{"a"}, Priority: 6}, "Priority"},
		{"InvalidTopicReserved", ntfy.Settings{Topics: []string{"announcements"}, Priority: 3}, "Topics"},
		{"InvalidTopicChars", ntfy.Settings{Topics: []string{"bad topic!"}, Priority: 3}, "Topics"},
		{"OK", ntfy.Settings{Topics: []string{"sonarr"}, Priority: 3}, ""},
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

func TestSettings_UserNamePasswordPairing(t *testing.T) {
	t.Parallel()
	s := ntfy.Settings{Topics: []string{"t"}, Priority: 3, Password: "p"}
	fails := s.Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "UserName" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected UserName failure when Password is set without AccessToken, got %+v", fails)
	}
}

func TestSettings_AccessTokenSkipsCredPairing(t *testing.T) {
	t.Parallel()
	s := ntfy.Settings{Topics: []string{"t"}, Priority: 3, AccessToken: "tok", Password: "p"}
	fails := s.Validate()
	for _, f := range fails {
		if f.PropertyName == "UserName" {
			t.Errorf("AccessToken should suppress UserName/Password pairing; got %+v", fails)
		}
	}
}

func TestNtfy_OnGrab_PerTopicPost(t *testing.T) {
	t.Parallel()
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeNtfy(t, srv, func(s *ntfy.Settings) {
		s.Topics = []string{"alpha", "beta"}
		s.Tags = []string{"tag1", "tag2"}
		s.ClickUrl = "https://click"
	})
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "ok"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 2 {
		t.Fatalf("calls = %d", len(capture))
	}
	paths := []string{capture[0].Path, capture[1].Path}
	if paths[0] != "/alpha" || paths[1] != "/beta" {
		t.Errorf("paths = %v", paths)
	}
	q, _ := url.ParseQuery(capture[0].RawQuery)
	if q.Get("title") != "Sonarr - Episode Grabbed" {
		t.Errorf("title = %q", q.Get("title"))
	}
	if q.Get("priority") != "3" {
		t.Errorf("priority = %q", q.Get("priority"))
	}
	if q.Get("tags") != "tag1,tag2" {
		t.Errorf("tags = %q", q.Get("tags"))
	}
	if q.Get("click") != "https://click" {
		t.Errorf("click = %q", q.Get("click"))
	}
}

func TestNtfy_AccessToken_SetsBearerHeader(t *testing.T) {
	t.Parallel()
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeNtfy(t, srv, func(s *ntfy.Settings) {
		s.AccessToken = "abc"
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if capture[0].AuthHeader != "Bearer abc" {
		t.Errorf("auth = %q", capture[0].AuthHeader)
	}
}

func TestNtfy_BasicAuth_FallbackToUserPass(t *testing.T) {
	t.Parallel()
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeNtfy(t, srv, func(s *ntfy.Settings) {
		s.UserName = "u"
		s.Password = "p"
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !strings.HasPrefix(capture[0].AuthHeader, "Basic ") {
		t.Errorf("expected Basic auth, got %q", capture[0].AuthHeader)
	}
}

func TestNtfy_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeNtfy(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestNtfy_OnHealthRestored_PrefixesMessage(t *testing.T) {
	t.Parallel()
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeNtfy(t, srv, nil)
	if err := prov.OnHealthRestored(t.Context(), notifications.HealthCheckMessage{
		Check: healthcheck.HealthCheck{Message: "disk-ok"},
	}); err != nil {
		t.Fatalf("OnHealthRestored: %v", err)
	}
	q, _ := url.ParseQuery(capture[0].RawQuery)
	if got := q.Get("message"); got != "The following issue is now resolved: disk-ok" {
		t.Errorf("message = %q", got)
	}
}

func TestNtfy_Test_AuthFailureMapsByCredentialType(t *testing.T) {
	t.Parallel()
	var capture []captured
	srv := capturingServer(t, http.StatusUnauthorized, &capture)
	defer srv.Close()
	prov := makeNtfy(t, srv, func(s *ntfy.Settings) {
		s.AccessToken = "bad-token"
	})
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "AccessToken" {
		t.Errorf("expected AccessToken failure, got %+v", failures)
	}

	var capture2 []captured
	srv2 := capturingServer(t, http.StatusForbidden, &capture2)
	defer srv2.Close()
	prov2 := makeNtfy(t, srv2, func(s *ntfy.Settings) {
		s.UserName = "u"
		s.Password = "p"
	})
	failures, _ = prov2.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "UserName" {
		t.Errorf("expected UserName failure, got %+v", failures)
	}
}

func TestNtfy_SendFailedSurfacesError(t *testing.T) {
	t.Parallel()
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeNtfy(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, ntfy.ErrSendFailed) {
		t.Errorf("err = %v, want ErrSendFailed", err)
	}
}

func TestNtfy_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(ntfy.Implementation, ntfy.ConfigContract) {
		t.Errorf("registry missing %s/%s", ntfy.Implementation, ntfy.ConfigContract)
	}
}
