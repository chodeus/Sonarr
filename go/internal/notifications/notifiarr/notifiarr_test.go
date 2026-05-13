package notifiarr_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/notifiarr"
	"sonarr.tv/sonarr/internal/notifications/webhook"
)

type captured struct {
	Path   string
	APIKey string
	Body   []byte
}

func capturingServer(t *testing.T, statusCode int, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*capture = append(*capture, captured{
			Path:   r.URL.Path,
			APIKey: r.Header.Get("X-API-Key"),
			Body:   b,
		})
		if statusCode > 0 {
			w.WriteHeader(statusCode)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func settingsJSON(t *testing.T, s notifiarr.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeNotifiarr(t *testing.T, srv *httptest.Server) notifications.Provider {
	t.Helper()
	s := notifiarr.Settings{ApiKey: "key-xyz"}
	notifiarr.SetAPIBaseURLForTest(srv.URL)
	t.Cleanup(func() { notifiarr.SetAPIBaseURLForTest("https://notifiarr.com") })
	def := &notifications.Notification{
		Implementation: notifiarr.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := notifiarr.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresApiKey(t *testing.T) {
	t.Parallel()
	fails := (&notifiarr.Settings{}).Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "ApiKey" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ApiKey failure, got %+v", fails)
	}
}

func TestNotifiarr_OnGrab_PostsWebhookPayload(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeNotifiarr(t, srv)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show.S01E01"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls = %d", len(capture))
	}
	if capture[0].Path != "/api/v1/notification/sonarr" {
		t.Errorf("path=%q", capture[0].Path)
	}
	if capture[0].APIKey != "key-xyz" {
		t.Errorf("X-API-Key=%q", capture[0].APIKey)
	}
	var p webhook.GrabPayload
	if err := json.Unmarshal(capture[0].Body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.EventType != webhook.EventTypeGrab {
		t.Errorf("EventType=%q", p.EventType)
	}
}

func TestNotifiarr_Test_PostsTestEventType(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeNotifiarr(t, srv)
	failures, err := prov.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
	var p webhook.GrabPayload
	_ = json.Unmarshal(capture[0].Body, &p)
	if p.EventType != webhook.EventTypeTest {
		t.Errorf("EventType=%q want Test", p.EventType)
	}
}

func TestNotifiarr_401SurfacesInvalidAPIKey(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusUnauthorized, &capture)
	defer srv.Close()
	prov := makeNotifiarr(t, srv)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, notifiarr.ErrInvalidAPIKey) {
		t.Errorf("err=%v want ErrInvalidAPIKey", err)
	}
}

func TestNotifiarr_400IsNonFatal(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusBadRequest, &capture)
	defer srv.Close()
	prov := makeNotifiarr(t, srv)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"}); err != nil {
		t.Errorf("400 should be non-fatal, got %v", err)
	}
}

func TestNotifiarr_503SurfacesServiceUnavailable(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusServiceUnavailable, &capture)
	defer srv.Close()
	prov := makeNotifiarr(t, srv)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, notifiarr.ErrServiceUnavailable) {
		t.Errorf("err=%v want ErrServiceUnavailable", err)
	}
}

func TestNotifiarr_520SurfacesCloudflare(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 520, &capture)
	defer srv.Close()
	prov := makeNotifiarr(t, srv)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, notifiarr.ErrCloudflare) {
		t.Errorf("err=%v want ErrCloudflare", err)
	}
}

func TestNotifiarr_500SurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeNotifiarr(t, srv)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, notifiarr.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestNotifiarr_SupportsOnRename(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeNotifiarr(t, srv)
	if !prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be true (Notifiarr extends WebhookBase)")
	}
	// Quick smoke: OnRename does POST (unlike most other providers).
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename: %v", err)
	}
}

func TestNotifiarr_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(notifiarr.Implementation, notifiarr.ConfigContract) {
		t.Errorf("registry missing %s/%s", notifiarr.Implementation, notifiarr.ConfigContract)
	}
}
