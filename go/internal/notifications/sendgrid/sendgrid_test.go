package sendgrid_test

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
	"sonarr.tv/sonarr/internal/notifications/sendgrid"
)

type captured struct {
	Path       string
	AuthHeader string
	Body       []byte
}

func capturingServer(t *testing.T, statusCode int, capture *[]captured) *httptest.Server {
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
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func settingsJSON(t *testing.T, s sendgrid.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeSendGrid(t *testing.T, srv *httptest.Server) notifications.Provider {
	t.Helper()
	s := sendgrid.Settings{
		BaseUrl:    srv.URL + "/v3/",
		ApiKey:     "key-xyz",
		From:       "sonarr@example.com",
		Recipients: []string{"a@x.com", "b@x.com"},
	}
	def := &notifications.Notification{
		Implementation: sendgrid.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := sendgrid.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    sendgrid.Settings
		want string
	}{
		{"NoApi", sendgrid.Settings{From: "a@x.com", Recipients: []string{"r@x.com"}}, "ApiKey"},
		{"NoFrom", sendgrid.Settings{ApiKey: "x", Recipients: []string{"r@x.com"}}, "From"},
		{"BadFromEmail", sendgrid.Settings{ApiKey: "x", From: "notanemail", Recipients: []string{"r@x.com"}}, "From"},
		{"NoRecipients", sendgrid.Settings{ApiKey: "x", From: "a@x.com"}, "Recipients"},
		{"BadRecipientEmail", sendgrid.Settings{ApiKey: "x", From: "a@x.com", Recipients: []string{"notanemail"}}, "Recipients"},
		{"OK", sendgrid.Settings{ApiKey: "x", From: "a@x.com", Recipients: []string{"r@x.com"}}, ""},
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

func TestSendGrid_OnGrab_PostsJSONPayload(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeSendGrid(t, srv)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show.S01E01"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if capture[0].Path != "/v3/mail/send" {
		t.Errorf("path=%q", capture[0].Path)
	}
	if !strings.HasPrefix(capture[0].AuthHeader, "Bearer ") {
		t.Errorf("expected Bearer auth, got %q", capture[0].AuthHeader)
	}
	var p sendgrid.Payload
	if err := json.Unmarshal(capture[0].Body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.From.Email != "sonarr@example.com" {
		t.Errorf("From=%q", p.From.Email)
	}
	if len(p.Content) != 1 || p.Content[0].Type != "text/plain" || p.Content[0].Value != "Show.S01E01" {
		t.Errorf("Content=%+v", p.Content)
	}
	if len(p.Personalizations) != 1 {
		t.Fatalf("Personalizations=%+v", p.Personalizations)
	}
	if p.Personalizations[0].Subject != "Episode Grabbed" {
		t.Errorf("Subject=%q", p.Personalizations[0].Subject)
	}
	tos := p.Personalizations[0].To
	if len(tos) != 2 || tos[0].Email != "a@x.com" || tos[1].Email != "b@x.com" {
		t.Errorf("To=%+v", tos)
	}
}

func TestSendGrid_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeSendGrid(t, srv)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestSendGrid_401SurfacesInvalidAPIKey(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusUnauthorized, &capture)
	defer srv.Close()
	prov := makeSendGrid(t, srv)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, sendgrid.ErrInvalidAPIKey) {
		t.Errorf("err=%v want ErrInvalidAPIKey", err)
	}
}

func TestSendGrid_500SurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeSendGrid(t, srv)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, sendgrid.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestSendGrid_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(sendgrid.Implementation, sendgrid.ConfigContract) {
		t.Errorf("registry missing %s/%s", sendgrid.Implementation, sendgrid.ConfigContract)
	}
}
