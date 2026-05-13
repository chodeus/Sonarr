package pushover_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/pushover"
)

func settingsJSON(t *testing.T, s pushover.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// capturingServer returns a test server that records every received
// form payload + always responds 200 unless statusCode is non-zero.
func capturingServer(t *testing.T, statusCode int, capture *[]url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		*capture = append(*capture, vals)
		if statusCode > 0 {
			w.WriteHeader(statusCode)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func makePushoverForServer(t *testing.T, srv *httptest.Server, extras func(*pushover.Settings)) notifications.Provider {
	t.Helper()
	s := pushover.Settings{UserKey: "user-xyz", ApiKey: "app-xyz"}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: pushover.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := pushover.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Override the proxy URL by re-wiring the http client to redirect
	// to srv? The proxy uses the hardcoded API URL. Tests inject the
	// httpDoer-backed client via the rewriteTransport pattern below.
	return p
}

// rewriteTransport rewrites every outgoing request to use the
// httptest.Server's scheme+host while preserving the path. Used to
// redirect the hardcoded pushover API URL to the test server.
type rewriteTransport struct{ base string }

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.base)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func makePushoverForServerWithRewrite(t *testing.T, srv *httptest.Server, extras func(*pushover.Settings)) notifications.Provider {
	t.Helper()
	s := pushover.Settings{UserKey: "user-xyz", ApiKey: "app-xyz"}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: pushover.Implementation,
		Settings:       settingsJSON(t, s),
	}
	client := &http.Client{Transport: &rewriteTransport{base: srv.URL}}
	p, err := pushover.New(def, notifications.Dependencies{HTTPClient: client})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresUserKey(t *testing.T) {
	t.Parallel()
	s := pushover.Settings{}
	fails := s.Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "UserKey" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected UserKey failure, got %+v", fails)
	}
}

func TestSettings_EmergencyRetryBounds(t *testing.T) {
	t.Parallel()
	s := pushover.Settings{UserKey: "u", Priority: int(pushover.PriorityEmergency), Retry: 10}
	fails := s.Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "Retry" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Retry failure for Emergency priority + retry=10, got %+v", fails)
	}
}

func TestPushover_OnGrab_PostsFormFields(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makePushoverForServerWithRewrite(t, srv, func(s *pushover.Settings) {
		s.Devices = []string{"phone", "tablet"}
		s.Sound = "bugle"
	})
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show.S01E01"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls = %d", len(capture))
	}
	got := capture[0]
	if got.Get("title") != "Episode Grabbed" {
		t.Errorf("title=%q", got.Get("title"))
	}
	if got.Get("message") != "Show.S01E01" {
		t.Errorf("message=%q", got.Get("message"))
	}
	if got.Get("token") != "app-xyz" {
		t.Errorf("token=%q", got.Get("token"))
	}
	if got.Get("user") != "user-xyz" {
		t.Errorf("user=%q", got.Get("user"))
	}
	if got.Get("device") != "phone,tablet" {
		t.Errorf("device=%q want phone,tablet", got.Get("device"))
	}
	if got.Get("priority") != "0" {
		t.Errorf("priority=%q", got.Get("priority"))
	}
	if got.Get("sound") != "bugle" {
		t.Errorf("sound=%q", got.Get("sound"))
	}
}

func TestPushover_Emergency_AddsRetryAndExpire(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makePushoverForServerWithRewrite(t, srv, func(s *pushover.Settings) {
		s.Priority = int(pushover.PriorityEmergency)
		s.Retry = 60
		s.Expire = 3600
	})
	if err := prov.OnHealthIssue(t.Context(), notifications.HealthCheckMessage{
		Check: healthcheck.HealthCheck{Message: "boom"},
	}); err != nil {
		t.Fatalf("OnHealthIssue: %v", err)
	}
	got := capture[0]
	if got.Get("retry") != "60" {
		t.Errorf("retry=%q", got.Get("retry"))
	}
	if got.Get("expire") != "3600" {
		t.Errorf("expire=%q", got.Get("expire"))
	}
}

func TestPushover_NonEmergency_OmitsRetryAndExpire(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makePushoverForServerWithRewrite(t, srv, func(s *pushover.Settings) {
		s.Priority = int(pushover.PriorityHigh)
		s.Retry = 60
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if capture[0].Has("retry") {
		t.Error("retry should not be set for non-Emergency priority")
	}
}

func TestPushover_TtlZeroIsOmitted(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makePushoverForServerWithRewrite(t, srv, nil)
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if capture[0].Has("ttl") {
		t.Error("ttl=0 should be omitted")
	}
}

func TestPushover_Test_SuccessRoundTrip(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makePushoverForServerWithRewrite(t, srv, nil)
	failures, err := prov.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
	if capture[0].Get("title") != "Test Notification" {
		t.Errorf("title=%q", capture[0].Get("title"))
	}
}

func TestPushover_Test_NonSuccessReturnsFailure(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, http.StatusUnauthorized, &capture)
	defer srv.Close()
	prov := makePushoverForServerWithRewrite(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 {
		t.Error("expected failure for 401")
	}
}

func TestPushover_SendFailedSurfacesError(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makePushoverForServerWithRewrite(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, pushover.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestPushover_DoesNotSupportOnRename(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makePushoverForServerWithRewrite(t, srv, nil)
	type renameSupporter interface{ SupportsOnRename() bool }
	if rs, ok := prov.(renameSupporter); !ok || rs.SupportsOnRename() {
		t.Error("Pushover should not support OnRename")
	}
	// Even if invoked, OnRename should return nil without sending.
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
}

func TestPushover_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(pushover.Implementation, pushover.ConfigContract) {
		t.Errorf("registry missing %s/%s", pushover.Implementation, pushover.ConfigContract)
	}
}

// Ensure makePushoverForServer compiles (unused; retained as a
// drop-in for tests that don't need rewrite).
var _ = makePushoverForServer
