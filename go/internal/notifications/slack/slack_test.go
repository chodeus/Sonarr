package slack_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/slack"
	"sonarr.tv/sonarr/internal/series"
)

func settingsJSON(t *testing.T, s slack.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeSlackForServer(t *testing.T, srv *httptest.Server, extras func(*slack.Settings)) notifications.Provider {
	t.Helper()
	s := slack.Settings{WebHookURL: srv.URL, Username: "sonarr-bot"}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: slack.Implementation,
		ConfigContract: ptrStr(slack.ConfigContract),
		Settings:       settingsJSON(t, s),
	}
	p, err := slack.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func ptrStr(v string) *string { return &v }

func TestSettings_ValidateRequiresWebHookAndUsername(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		s       slack.Settings
		wantErr string
	}{
		{"OK", slack.Settings{WebHookURL: "https://hooks.slack.com/x", Username: "bot"}, ""},
		{"MissingURL", slack.Settings{Username: "bot"}, "WebHookUrl"},
		{"BadURL", slack.Settings{WebHookURL: "ftp://x", Username: "bot"}, "WebHookUrl"},
		{"MissingUsername", slack.Settings{WebHookURL: "https://hooks.slack.com/x"}, "Username"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.s
			fails := s.Validate()
			if c.wantErr == "" {
				if len(fails) != 0 {
					t.Errorf("expected no failures, got %+v", fails)
				}
				return
			}
			found := false
			for _, f := range fails {
				if f.PropertyName == c.wantErr {
					found = true
				}
			}
			if !found {
				t.Errorf("missing %s failure; got %+v", c.wantErr, fails)
			}
		})
	}
}

func TestSlack_OnGrab_SendsPayloadWithGrabbedPrefix(t *testing.T) {
	t.Parallel()
	var payload slack.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	prov := makeSlackForServer(t, srv, nil)
	msg := notifications.GrabMessage{Series: &series.Series{Title: "Show"}, Message: "Show.S01E01"}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if !strings.HasPrefix(payload.Text, "Grabbed: ") {
		t.Errorf("Text = %q, want 'Grabbed: …' prefix", payload.Text)
	}
	if len(payload.Attachments) != 1 || payload.Attachments[0].Color != slack.ColorWarning {
		t.Errorf("Attachment = %+v, want one warning-colored", payload.Attachments)
	}
	if payload.Username != "sonarr-bot" {
		t.Errorf("Username = %q", payload.Username)
	}
}

func TestSlack_OnDownload_GoodColor(t *testing.T) {
	t.Parallel()
	var payload slack.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeSlackForServer(t, srv, nil)
	if err := prov.OnDownload(t.Context(), notifications.DownloadMessage{Series: &series.Series{Title: "X"}, Message: "x"}); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	if payload.Attachments[0].Color != slack.ColorGood {
		t.Errorf("color=%q want good", payload.Attachments[0].Color)
	}
	if !strings.HasPrefix(payload.Text, "Imported: ") {
		t.Errorf("Text=%q", payload.Text)
	}
}

func TestSlack_IconEmojiVsURLDiscriminator(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		icon      string
		wantEmoji string
		wantURL   string
	}{
		{"Emoji", ":parrot:", ":parrot:", ""},
		{"URL", "https://example.com/icon.png", "", "https://example.com/icon.png"},
		{"Empty", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var payload slack.Payload
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &payload)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			prov := makeSlackForServer(t, srv, func(s *slack.Settings) { s.Icon = c.icon })
			_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Series: &series.Series{Title: "X"}, Message: "x"})
			if payload.IconEmoji != c.wantEmoji {
				t.Errorf("IconEmoji=%q want %q", payload.IconEmoji, c.wantEmoji)
			}
			if payload.IconURL != c.wantURL {
				t.Errorf("IconURL=%q want %q", payload.IconURL, c.wantURL)
			}
		})
	}
}

func TestSlack_HealthIssueColorMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		typ       healthcheck.Result
		wantColor string
	}{
		{"Warning", healthcheck.ResultWarning, slack.ColorWarning},
		{"Error", healthcheck.ResultError, slack.ColorDanger},
		{"Notice", healthcheck.ResultNotice, slack.ColorDanger},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var payload slack.Payload
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &payload)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			prov := makeSlackForServer(t, srv, nil)
			msg := notifications.HealthCheckMessage{Check: healthcheck.HealthCheck{
				Source: "RootFolderCheck", Type: c.typ, Message: "missing",
			}}
			if err := prov.OnHealthIssue(t.Context(), msg); err != nil {
				t.Fatalf("OnHealthIssue: %v", err)
			}
			if payload.Attachments[0].Color != c.wantColor {
				t.Errorf("color=%q want %q", payload.Attachments[0].Color, c.wantColor)
			}
		})
	}
}

func TestSlack_Test_NonSuccessReturnsFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	prov := makeSlackForServer(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 {
		t.Error("expected at least one failure for 401")
	}
}

func TestSlack_SendFailedSurfacesError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	prov := makeSlackForServer(t, srv, nil)
	err := prov.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: &series.Series{Title: "X"}})
	if !errors.Is(err, slack.ErrSendFailed) {
		t.Errorf("err = %v, want ErrSendFailed", err)
	}
}

func TestSlack_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(slack.Implementation, slack.ConfigContract) {
		t.Errorf("registry missing %s/%s", slack.Implementation, slack.ConfigContract)
	}
}
