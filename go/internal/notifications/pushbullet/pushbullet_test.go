package pushbullet_test

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
	"sonarr.tv/sonarr/internal/notifications/pushbullet"
)

type captured struct {
	AuthHeader string
	Form       url.Values
}

func capturingServer(t *testing.T, statusCode int, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		*capture = append(*capture, captured{
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

func settingsJSON(t *testing.T, s pushbullet.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makePushbullet(t *testing.T, srv *httptest.Server, extras func(*pushbullet.Settings)) notifications.Provider {
	t.Helper()
	s := pushbullet.Settings{ApiKey: "key-xyz"}
	if extras != nil {
		extras(&s)
	}
	pushbullet.SetAPIURLsForTest(srv.URL+"/pushes", srv.URL+"/devices")
	t.Cleanup(func() {
		pushbullet.SetAPIURLsForTest("https://api.pushbullet.com/v2/pushes", "https://api.pushbullet.com/v2/devices")
	})
	def := &notifications.Notification{
		Implementation: pushbullet.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := pushbullet.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresApiKey(t *testing.T) {
	t.Parallel()
	fails := (&pushbullet.Settings{}).Validate()
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

func TestPushbullet_NoDevicesNoChannels_SendsOneGlobalPush(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makePushbullet(t, srv, nil)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls = %d", len(capture))
	}
	got := capture[0].Form
	if got.Get("type") != "note" {
		t.Errorf("type=%q", got.Get("type"))
	}
	if got.Get("title") != "Sonarr - Episode Grabbed" {
		t.Errorf("title=%q", got.Get("title"))
	}
	if got.Get("body") != "x" {
		t.Errorf("body=%q", got.Get("body"))
	}
	if got.Has("channel_tag") || got.Has("device_id") || got.Has("device_iden") {
		t.Errorf("global push should not set target fields, got %v", got)
	}
	if !strings.HasPrefix(capture[0].AuthHeader, "Basic ") {
		t.Errorf("expected Basic auth, got %q", capture[0].AuthHeader)
	}
}

func TestPushbullet_ChannelTagsFanOut(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makePushbullet(t, srv, func(s *pushbullet.Settings) {
		s.ChannelTags = []string{"alpha", "beta"}
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if len(capture) != 2 {
		t.Fatalf("calls = %d, want 2", len(capture))
	}
	tags := []string{capture[0].Form.Get("channel_tag"), capture[1].Form.Get("channel_tag")}
	if tags[0] != "alpha" || tags[1] != "beta" {
		t.Errorf("tags = %v", tags)
	}
}

func TestPushbullet_DeviceIdsFanOut_IntDeviceIdVsStringDeviceIden(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makePushbullet(t, srv, func(s *pushbullet.Settings) {
		s.DeviceIds = []string{"123", "udid-abc"}
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if len(capture) != 2 {
		t.Fatalf("calls = %d, want 2", len(capture))
	}
	if capture[0].Form.Get("device_id") != "123" || capture[0].Form.Has("device_iden") {
		t.Errorf("numeric id should map to device_id, got %v", capture[0].Form)
	}
	if capture[1].Form.Get("device_iden") != "udid-abc" || capture[1].Form.Has("device_id") {
		t.Errorf("string id should map to device_iden, got %v", capture[1].Form)
	}
}

func TestPushbullet_SenderId_AddsSourceDeviceIden(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makePushbullet(t, srv, func(s *pushbullet.Settings) {
		s.SenderId = "sender-abc"
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if capture[0].Form.Get("source_device_iden") != "sender-abc" {
		t.Errorf("source_device_iden=%q", capture[0].Form.Get("source_device_iden"))
	}
}

func TestPushbullet_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makePushbullet(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestPushbullet_Test_401MapsToApiKey(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusUnauthorized, &capture)
	defer srv.Close()
	prov := makePushbullet(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "ApiKey" {
		t.Errorf("expected ApiKey failure, got %+v", failures)
	}
}

func TestPushbullet_500SurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makePushbullet(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, pushbullet.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestGetDevices_ParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"devices":[{"iden":"abc","nickname":"Phone","manufacturer":"X","model":"Y"}]}`))
	}))
	defer srv.Close()
	pushbullet.SetAPIURLsForTest(srv.URL+"/pushes", srv.URL+"/devices")
	t.Cleanup(func() {
		pushbullet.SetAPIURLsForTest("https://api.pushbullet.com/v2/pushes", "https://api.pushbullet.com/v2/devices")
	})
	devices, err := pushbullet.GetDevices(t.Context(), srv.Client(), &pushbullet.Settings{ApiKey: "k"})
	if err != nil {
		t.Fatalf("GetDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != "abc" || devices[0].Nickname != "Phone" {
		t.Errorf("devices = %+v", devices)
	}
}

func TestPushbullet_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(pushbullet.Implementation, pushbullet.ConfigContract) {
		t.Errorf("registry missing %s/%s", pushbullet.Implementation, pushbullet.ConfigContract)
	}
}
