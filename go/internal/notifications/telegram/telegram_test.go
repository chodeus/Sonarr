package telegram_test

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

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/telegram"
	"sonarr.tv/sonarr/internal/series"
)

func settingsJSON(t *testing.T, s telegram.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// capturingServer captures every incoming POST body + records the
// request path. Always replies 200 unless statusCode is non-zero.
func capturingServer(t *testing.T, statusCode int, capture *[]capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*capture = append(*capture, capturedRequest{Path: r.URL.Path, Body: body})
		if statusCode > 0 {
			w.WriteHeader(statusCode)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

type capturedRequest struct {
	Path string
	Body []byte
}

// rewriteTransport redirects every outgoing request to the
// httptest.Server while preserving path. Used to bypass the hardcoded
// api.telegram.org host inside the proxy. Mirrors the pushover test
// precedent at pushover_test.go.
type rewriteTransport struct{ base string }

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.base)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}

type stubConfig struct{ name string }

func (s *stubConfig) InstanceName() string { return s.name }

func makeTelegram(t *testing.T, srv *httptest.Server, extras func(*telegram.Settings), instance string) notifications.Provider {
	t.Helper()
	s := telegram.Settings{
		BotToken:    "token-xyz",
		ChatId:      "chat-1",
		LinkPreview: int(telegram.LinkPreviewNone),
	}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: telegram.Implementation,
		Settings:       settingsJSON(t, s),
	}
	client := &http.Client{Transport: &rewriteTransport{base: srv.URL}}
	deps := notifications.Dependencies{HTTPClient: client}
	if instance != "" {
		deps.ConfigProvider = &stubConfig{name: instance}
	}
	p, err := telegram.New(def, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresBotTokenAndChatID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    telegram.Settings
		want string
	}{
		{"MissingBoth", telegram.Settings{LinkPreview: int(telegram.LinkPreviewNone)}, "BotToken"},
		{"MissingChat", telegram.Settings{BotToken: "x", LinkPreview: int(telegram.LinkPreviewNone)}, "ChatId"},
		{"OK", telegram.Settings{BotToken: "x", ChatId: "y", LinkPreview: int(telegram.LinkPreviewNone)}, ""},
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

func TestSettings_TopicIDMustBeGreaterThanOne(t *testing.T) {
	t.Parallel()
	one := 1
	s := telegram.Settings{BotToken: "x", ChatId: "y", TopicId: &one}
	fails := s.Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "TopicId" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected TopicId failure, got %+v", fails)
	}
}

func TestSettings_LinkPreviewMustBeInMetadataLinks(t *testing.T) {
	t.Parallel()
	// LinkPreview=Trakt but MetadataLinks doesn't include Trakt.
	s := telegram.Settings{
		BotToken:      "x",
		ChatId:        "y",
		MetadataLinks: []int{int(notifications.MetadataLinkImdb)},
		LinkPreview:   int(telegram.LinkPreviewTrakt),
	}
	fails := s.Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "LinkPreview" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected LinkPreview-not-in-MetadataLinks failure, got %+v", fails)
	}
}

func TestTelegram_OnGrab_PostsHTMLBody(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeTelegram(t, srv, nil, "")
	msg := notifications.GrabMessage{
		Series:  &series.Series{Title: "Show", ImdbID: "tt001"},
		Message: "Show.S01E01 & friends",
	}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls = %d", len(capture))
	}
	if got := capture[0].Path; got != "/bottoken-xyz/sendmessage" {
		t.Errorf("path = %q", got)
	}
	var p telegram.Payload
	if err := json.Unmarshal(capture[0].Body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.ChatID != "chat-1" {
		t.Errorf("ChatID = %q", p.ChatID)
	}
	if p.ParseMode != "HTML" {
		t.Errorf("ParseMode = %q", p.ParseMode)
	}
	if !strings.Contains(p.Text, "<b>Episode Grabbed</b>") {
		t.Errorf("Text missing bold title: %q", p.Text)
	}
	if !strings.Contains(p.Text, "Show.S01E01 &amp; friends") {
		t.Errorf("Text missing HTML-escaped message: %q", p.Text)
	}
}

func TestTelegram_BrandedAndInstance_Title(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeTelegram(t, srv, func(s *telegram.Settings) {
		s.IncludeAppNameInTitle = true
		s.IncludeInstanceNameInTitle = true
	}, "MyBox")
	if err := prov.OnDownload(t.Context(), notifications.DownloadMessage{Message: "ok"}); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	var p telegram.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if !strings.Contains(p.Text, "<b>Sonarr - Episode Downloaded - MyBox</b>") {
		t.Errorf("Text missing branded+instance title: %q", p.Text)
	}
}

func TestTelegram_MetadataLinks_Rendered(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeTelegram(t, srv, func(s *telegram.Settings) {
		s.MetadataLinks = []int{
			int(notifications.MetadataLinkImdb),
			int(notifications.MetadataLinkTvdb),
		}
		s.LinkPreview = int(telegram.LinkPreviewImdb)
	}, "")
	msg := notifications.GrabMessage{
		Series:  &series.Series{Title: "Show", ImdbID: "tt007", TvdbID: 99},
		Message: "msg",
	}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	var p telegram.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if !strings.Contains(p.Text, `<a href="https://www.imdb.com/title/tt007">IMDb</a>`) {
		t.Errorf("Text missing IMDb anchor: %q", p.Text)
	}
	if !strings.Contains(p.Text, `<a href="http://www.thetvdb.com/?tab=series&id=99">TVDb</a>`) {
		t.Errorf("Text missing TVDb anchor: %q", p.Text)
	}
	if p.LinkPreviewOptions == nil || p.LinkPreviewOptions.IsDisabled {
		t.Errorf("LinkPreviewOptions IsDisabled = true, want false")
	}
	if got := p.LinkPreviewOptions.URL; got != "https://www.imdb.com/title/tt007" {
		t.Errorf("LinkPreviewOptions.URL = %q", got)
	}
}

func TestTelegram_LinkPreviewNone_DisablesPreview(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeTelegram(t, srv, func(s *telegram.Settings) {
		s.LinkPreview = int(telegram.LinkPreviewNone)
	}, "")
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{})
	var p telegram.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if p.LinkPreviewOptions == nil || !p.LinkPreviewOptions.IsDisabled {
		t.Errorf("expected IsDisabled=true for LinkPreview=None, got %+v", p.LinkPreviewOptions)
	}
}

func TestTelegram_TopicIDPropagatesToMessageThreadID(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	topic := 42
	prov := makeTelegram(t, srv, func(s *telegram.Settings) {
		s.TopicId = &topic
		s.SendSilently = true
	}, "")
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{})
	var p telegram.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if p.MessageThreadID == nil || *p.MessageThreadID != 42 {
		t.Errorf("MessageThreadID = %v", p.MessageThreadID)
	}
	if !p.DisableNotification {
		t.Errorf("DisableNotification = false, want true")
	}
}

func TestTelegram_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeTelegram(t, srv, nil, "")
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false (C# parity)")
	}
}

func TestTelegram_OnHealthRestored_PrefixesResolvedMessage(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeTelegram(t, srv, nil, "")
	if err := prov.OnHealthRestored(t.Context(), notifications.HealthCheckMessage{
		Check: healthcheck.HealthCheck{Message: "disk-ok"},
	}); err != nil {
		t.Fatalf("OnHealthRestored: %v", err)
	}
	var p telegram.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if !strings.Contains(p.Text, "The following issue is now resolved: disk-ok") {
		t.Errorf("Text missing resolved-prefix: %q", p.Text)
	}
}

func TestTelegram_Test_SuccessRoundTrip(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeTelegram(t, srv, nil, "")
	failures, err := prov.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
	var p telegram.Payload
	_ = json.Unmarshal(capture[0].Body, &p)
	if !strings.Contains(p.Text, `<a href="https://sonarr.tv">Sonarr.tv</a>`) {
		t.Errorf("Test message missing Sonarr.tv link: %q", p.Text)
	}
}

func TestTelegram_Test_ValidationFailsBeforeSend(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("Test with invalid settings should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	def := &notifications.Notification{
		Implementation: telegram.Implementation,
		Settings:       `{"botToken":"","chatId":""}`,
	}
	client := &http.Client{Transport: &rewriteTransport{base: srv.URL}}
	p, err := telegram.New(def, notifications.Dependencies{HTTPClient: client})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	failures, _ := p.Test(t.Context())
	if len(failures) == 0 {
		t.Error("expected validation failures for empty settings")
	}
}

func TestTelegram_SendFailedSurfacesError(t *testing.T) {
	t.Parallel()
	var capture []capturedRequest
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeTelegram(t, srv, nil, "")
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, telegram.ErrSendFailed) {
		t.Errorf("err = %v, want ErrSendFailed", err)
	}
}

func TestTelegram_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(telegram.Implementation, telegram.ConfigContract) {
		t.Errorf("registry missing %s/%s", telegram.Implementation, telegram.ConfigContract)
	}
}
