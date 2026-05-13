package join_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/join"
)

type captured struct {
	Path  string
	Query url.Values
}

func capturingServer(t *testing.T, statusCode int, body string, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*capture = append(*capture, captured{Path: r.URL.Path, Query: r.URL.Query()})
		if statusCode > 0 {
			w.WriteHeader(statusCode)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		if body == "" {
			body = `{"success":true}`
		}
		_, _ = w.Write([]byte(body))
	}))
}

func settingsJSON(t *testing.T, s join.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeJoin(t *testing.T, srv *httptest.Server, extras func(*join.Settings)) notifications.Provider {
	t.Helper()
	s := join.Settings{ApiKey: "key-xyz", Priority: int(join.PriorityNormal)}
	if extras != nil {
		extras(&s)
	}
	join.SetAPIURLForTest(srv.URL)
	t.Cleanup(func() {
		join.SetAPIURLForTest("https://joinjoaomgcd.appspot.com/_ah/api/messaging/v1/sendPush")
	})
	def := &notifications.Notification{
		Implementation: join.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := join.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresApiKey(t *testing.T) {
	t.Parallel()
	fails := (&join.Settings{}).Validate()
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

func TestSettings_RejectsLegacyDeviceIds(t *testing.T) {
	t.Parallel()
	fails := (&join.Settings{ApiKey: "x", DeviceIds: "legacy-id"}).Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "DeviceIds" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected DeviceIds failure, got %+v", fails)
	}
}

func TestJoin_NoDevicesNoNames_FallsBackToGroupAll(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makeJoin(t, srv, nil)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	q := capture[0].Query
	if q.Get("deviceId") != "group.all" {
		t.Errorf("deviceId=%q", q.Get("deviceId"))
	}
	if q.Get("title") != "Sonarr - Episode Grabbed" {
		t.Errorf("title=%q", q.Get("title"))
	}
	if q.Get("apikey") != "key-xyz" {
		t.Errorf("apikey=%q", q.Get("apikey"))
	}
	if q.Get("priority") != "0" {
		t.Errorf("priority=%q", q.Get("priority"))
	}
}

func TestJoin_DeviceNames_TakesPriorityOverDeviceIds(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makeJoin(t, srv, func(s *join.Settings) {
		s.DeviceNames = "phone,tablet"
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	q := capture[0].Query
	if q.Get("deviceNames") != "phone,tablet" {
		t.Errorf("deviceNames=%q", q.Get("deviceNames"))
	}
	if q.Has("deviceId") {
		t.Error("deviceId should not be set when deviceNames is provided")
	}
}

func TestJoin_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeJoin(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestJoin_Test_NoDevicesErrorMapsToDeviceIds(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, `{"success":false,"errorMessage":"No devices to send to"}`, &capture)
	defer srv.Close()
	prov := makeJoin(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "DeviceIds" {
		t.Errorf("expected DeviceIds failure, got %+v", failures)
	}
}

func TestJoin_Test_UserAuthErrorMapsToApiKey(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, `{"success":false,"userAuthError":true}`, &capture)
	defer srv.Close()
	prov := makeJoin(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "ApiKey" {
		t.Errorf("expected ApiKey failure, got %+v", failures)
	}
}

func TestJoin_NoDeviceToSendMessageToMapsToAuth(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, `{"success":false,"errorMessage":"No device to send message to"}`, &capture)
	defer srv.Close()
	prov := makeJoin(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, join.ErrAuth) {
		t.Errorf("err=%v want ErrAuth", err)
	}
}

func TestJoin_5xxSurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, "", &capture)
	defer srv.Close()
	prov := makeJoin(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, join.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestJoin_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(join.Implementation, join.ConfigContract) {
		t.Errorf("registry missing %s/%s", join.Implementation, join.ConfigContract)
	}
}
