package mailgun_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/mailgun"
)

type captured struct {
	Path       string
	AuthHeader string
	Form       url.Values
}

func capturingServer(t *testing.T, statusCode int, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		*capture = append(*capture, captured{
			Path:       r.URL.Path,
			AuthHeader: r.Header.Get("Authorization"),
			Form:       vals,
		})
		if statusCode > 0 {
			w.WriteHeader(statusCode)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func settingsJSON(t *testing.T, s mailgun.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeMailgun(t *testing.T, srv *httptest.Server) notifications.Provider {
	t.Helper()
	s := mailgun.Settings{
		ApiKey:       "key-xyz",
		From:         "sonarr@example.com",
		SenderDomain: "example.com",
		Recipients:   []string{"a@x.com", "b@x.com"},
	}
	mailgun.SetAPIBaseURLsForTest(srv.URL, srv.URL)
	t.Cleanup(func() {
		mailgun.SetAPIBaseURLsForTest("https://api.eu.mailgun.net/v3", "https://api.mailgun.net/v3")
	})
	def := &notifications.Notification{
		Implementation: mailgun.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := mailgun.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresApiKeyFromAndRecipients(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    mailgun.Settings
		want string
	}{
		{"NoApi", mailgun.Settings{From: "x", Recipients: []string{"a"}}, "ApiKey"},
		{"NoFrom", mailgun.Settings{ApiKey: "x", Recipients: []string{"a"}}, "From"},
		{"NoRecipients", mailgun.Settings{ApiKey: "x", From: "x"}, "Recipients"},
		{"OK", mailgun.Settings{ApiKey: "x", From: "x", Recipients: []string{"a"}}, ""},
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

func TestMailgun_OnGrab_FormPost(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeMailgun(t, srv)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if capture[0].Path != "/example.com/messages" {
		t.Errorf("path=%q", capture[0].Path)
	}
	if !strings.HasPrefix(capture[0].AuthHeader, "Basic ") {
		t.Errorf("expected Basic auth, got %q", capture[0].AuthHeader)
	}
	if capture[0].Form.Get("from") != "sonarr@example.com" {
		t.Errorf("from=%q", capture[0].Form.Get("from"))
	}
	tos := capture[0].Form["to"]
	if len(tos) != 2 || tos[0] != "a@x.com" || tos[1] != "b@x.com" {
		t.Errorf("to=%v", tos)
	}
	if capture[0].Form.Get("subject") != "Episode Grabbed" {
		t.Errorf("subject=%q", capture[0].Form.Get("subject"))
	}
	if capture[0].Form.Get("text") != "Show" {
		t.Errorf("text=%q", capture[0].Form.Get("text"))
	}
}

func TestMailgun_OnEpisodeFileDelete_AppendsDeletedSuffix(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeMailgun(t, srv)
	if err := prov.OnEpisodeFileDelete(t.Context(), notifications.EpisodeDeleteMessage{Message: "Show.S01E01"}); err != nil {
		t.Fatalf("OnEpisodeFileDelete: %v", err)
	}
	if got := capture[0].Form.Get("text"); got != "Show.S01E01 deleted." {
		t.Errorf("text=%q", got)
	}
}

func TestMailgun_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeMailgun(t, srv)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestMailgun_InvalidAPIKeySurfacesSentinel(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusUnauthorized, &capture)
	defer srv.Close()
	prov := makeMailgun(t, srv)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, mailgun.ErrInvalidAPIKey) {
		t.Errorf("err=%v want ErrInvalidAPIKey", err)
	}
}

func TestMailgun_500SurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeMailgun(t, srv)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, mailgun.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestMailgun_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(mailgun.Implementation, mailgun.ConfigContract) {
		t.Errorf("registry missing %s/%s", mailgun.Implementation, mailgun.ConfigContract)
	}
}
