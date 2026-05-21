package webhook_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/webhook"
)

// settingsJSON builds a Settings JSON string suitable for the
// Notification.Settings column.
func settingsJSON(t *testing.T, s webhook.Settings) string {
	t.Helper()
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return string(out)
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		url      string
		wantFail bool
	}{
		{"valid http", "http://example.com/hook", false},
		{"valid https", "https://example.com/hook", false},
		{"empty", "", true},
		{"whitespace", "   ", true},
		{"no scheme", "example.com/hook", true},
		{"unsupported scheme", "ftp://example.com/hook", true},
		{"no host", "http://", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := webhook.Settings{URL: c.url}
			failures := s.Validate()
			if c.wantFail && len(failures) == 0 {
				t.Errorf("expected validation failure for %q", c.url)
			}
			if !c.wantFail && len(failures) > 0 {
				t.Errorf("unexpected failure for %q: %+v", c.url, failures)
			}
		})
	}
}

// TestWebhook_Test_SuccessRoundTrip verifies that a healthy 200
// response from the webhook target translates to a nil failure
// list. Mirrors C# `SendWebhookTest` happy path at `Webhook.cs:92-104`.
func TestWebhook_Test_SuccessRoundTrip(t *testing.T) {
	t.Parallel()

	var (
		gotMethod      string
		gotContentType string
		gotAccept      string
		gotBody        []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotAccept = r.Header.Get("Accept")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		ID:             7,
		Implementation: webhook.Implementation,
		Settings: settingsJSON(t, webhook.Settings{
			URL:    srv.URL,
			Method: int(webhook.MethodPOST),
		}),
	}
	p, err := webhook.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	failures, err := p.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) > 0 {
		t.Errorf("Test returned failures: %+v", failures)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type: got %q want application/json", gotContentType)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept: got %q want application/json", gotAccept)
	}
	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["eventType"] != "Test" {
		t.Errorf("eventType: got %v want Test", payload["eventType"])
	}
}

// TestWebhook_Test_NonSuccessReturnsFailure verifies that a non-2xx
// response surfaces a TestFailure entry. Mirrors C# `Test`
// returning a `NzbDroneValidationFailure("Url", ...)` at
// `Webhook.cs:100`.
func TestWebhook_Test_NonSuccessReturnsFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		ID:             7,
		Implementation: webhook.Implementation,
		Settings: settingsJSON(t, webhook.Settings{
			URL:    srv.URL,
			Method: int(webhook.MethodPOST),
		}),
	}
	p, _ := webhook.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	failures, err := p.Test(t.Context())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d: %+v", len(failures), failures)
	}
	if failures[0].PropertyName != "Url" {
		t.Errorf("failure PropertyName: got %q want Url", failures[0].PropertyName)
	}
}

// TestWebhook_PUTMethod verifies the Method=PUT path mirrors the
// C# `WebhookProxy.SendWebhook` switch arm at `WebhookProxy.cs:34`.
func TestWebhook_PUTMethod(t *testing.T) {
	t.Parallel()

	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		Implementation: webhook.Implementation,
		Settings: settingsJSON(t, webhook.Settings{
			URL:    srv.URL,
			Method: int(webhook.MethodPUT),
		}),
	}
	p, _ := webhook.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if _, err := p.Test(t.Context()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method: got %q want PUT", gotMethod)
	}
}

// TestWebhook_BasicAuthAndHeaders verifies that Settings.Username/
// Password attach a Basic-Auth header and Settings.Headers entries
// are added to the outgoing request. Mirrors the C# `WebhookProxy.
// SendWebhook` Basic-auth + custom-header arms at `WebhookProxy.cs:
// 41-49`.
func TestWebhook_BasicAuthAndHeaders(t *testing.T) {
	t.Parallel()

	var (
		gotAuthUser, gotAuthPass string
		gotAuthOK                bool
		gotCustom                string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthUser, gotAuthPass, gotAuthOK = r.BasicAuth()
		gotCustom = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		Implementation: webhook.Implementation,
		Settings: settingsJSON(t, webhook.Settings{
			URL:      srv.URL,
			Method:   int(webhook.MethodPOST),
			Username: "alice",
			Password: "hunter2",
			Headers: []webhook.HeaderEntry{
				{Key: "X-Custom", Value: "custom-value"},
			},
		}),
	}
	p, _ := webhook.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if _, err := p.Test(t.Context()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !gotAuthOK {
		t.Fatalf("expected basic-auth header")
	}
	if gotAuthUser != "alice" || gotAuthPass != "hunter2" {
		t.Errorf("basic auth: got (%q,%q) want (alice, hunter2)", gotAuthUser, gotAuthPass)
	}
	if gotCustom != "custom-value" {
		t.Errorf("X-Custom: got %q want custom-value", gotCustom)
	}
}

// TestWebhook_OnHealthIssue verifies the Health payload shape +
// EventType discriminator round-trips. Mirrors C#
// `BuildHealthPayload` at `WebhookBase.cs:159-170`.
func TestWebhook_OnHealthIssue(t *testing.T) {
	t.Parallel()

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		Implementation: webhook.Implementation,
		Settings: settingsJSON(t, webhook.Settings{
			URL:    srv.URL,
			Method: int(webhook.MethodPOST),
		}),
	}
	p, _ := webhook.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	hc := healthcheck.HealthCheck{
		Source:  "ApiKeyCheck",
		Type:    healthcheck.ResultWarning,
		Message: "API key is weak",
		WikiURL: "https://wiki.servarr.com/sonarr/system#api-key",
	}
	if err := p.OnHealthIssue(t.Context(), notifications.HealthCheckMessage{Check: hc}); err != nil {
		t.Fatalf("OnHealthIssue: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["eventType"] != "Health" {
		t.Errorf("eventType: got %v want Health", payload["eventType"])
	}
	if payload["level"] != "warning" {
		t.Errorf("level: got %v want warning", payload["level"])
	}
	if payload["message"] != "API key is weak" {
		t.Errorf("message: got %v want 'API key is weak'", payload["message"])
	}
	if payload["wikiUrl"] != "https://wiki.servarr.com/sonarr/system#api-key" {
		t.Errorf("wikiUrl: got %v", payload["wikiUrl"])
	}
	// J171 byte-shape guard: Health payloads must NOT carry an
	// `applicationUrl` key. C# `BuildHealthPayload` at
	// `WebhookBase.cs:159-170` does not assign WebhookPayload.
	// ApplicationUrl, leaving the C# `string` property null —
	// Newtonsoft's `NullValueHandling.Ignore` (Json.cs:26 via
	// payload.ToJson() at WebhookProxy.cs:39) drops the key. The
	// Go port preserves parity by inlining EventType + InstanceName
	// directly on HealthPayload instead of embedding BasePayload.
	if _, present := payload["applicationUrl"]; present {
		t.Errorf("Health payload must omit applicationUrl: %s", string(gotBody))
	}
}

// TestWebhook_OnHealthRestored emits the HealthRestored event type.
func TestWebhook_OnHealthRestored(t *testing.T) {
	t.Parallel()

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		Implementation: webhook.Implementation,
		Settings: settingsJSON(t, webhook.Settings{
			URL:    srv.URL,
			Method: int(webhook.MethodPOST),
		}),
	}
	p, _ := webhook.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err := p.OnHealthRestored(t.Context(), notifications.HealthCheckMessage{Check: healthcheck.HealthCheck{Source: "Foo"}}); err != nil {
		t.Fatalf("OnHealthRestored: %v", err)
	}
	var payload map[string]any
	_ = json.Unmarshal(gotBody, &payload)
	if payload["eventType"] != "HealthRestored" {
		t.Errorf("eventType: got %v want HealthRestored", payload["eventType"])
	}
	// J171 byte-shape guard: HealthRestored shares HealthPayload with
	// OnHealthIssue and must likewise omit `applicationUrl`.
	if _, present := payload["applicationUrl"]; present {
		t.Errorf("HealthRestored payload must omit applicationUrl: %s", string(gotBody))
	}
}

// TestWebhook_New_BadSettingsJSON surfaces the deserialisation
// failure as a typed error.
func TestWebhook_New_BadSettingsJSON(t *testing.T) {
	t.Parallel()
	def := &notifications.Notification{
		Implementation: webhook.Implementation,
		Settings:       "not json",
	}
	_, err := webhook.New(def, notifications.Dependencies{})
	if err == nil {
		t.Errorf("expected unmarshal error")
	}
}

// TestWebhook_New_NilDefinition rejects nil row.
func TestWebhook_New_NilDefinition(t *testing.T) {
	t.Parallel()
	_, err := webhook.New(nil, notifications.Dependencies{})
	if err == nil {
		t.Errorf("expected nil-def error")
	}
}

// TestWebhook_RegisteredInDefault verifies the init() registration
// landed and the Constructor resolves through
// notifications.Default(). The registry was populated when the
// webhook package was imported (test package imports it).
func TestWebhook_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(webhook.Implementation, webhook.ConfigContract) {
		t.Errorf("expected Webhook registered in notifications.Default")
	}
}

// TestWebhook_SendFailedSurfacesError verifies a transport-level
// failure (closed server) wraps as ErrSendFailed when invoked
// directly (vs Test() which wraps in a TestFailure).
func TestWebhook_SendFailedSurfacesError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		Implementation: webhook.Implementation,
		Settings: settingsJSON(t, webhook.Settings{
			URL:    srv.URL,
			Method: int(webhook.MethodPOST),
		}),
	}
	p, _ := webhook.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	err := p.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{})
	if err == nil {
		t.Fatalf("expected send error")
	}
	if !errors.Is(err, webhook.ErrSendFailed) {
		t.Errorf("error not ErrSendFailed: %v", err)
	}
}
