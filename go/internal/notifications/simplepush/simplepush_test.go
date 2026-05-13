package simplepush_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/simplepush"
)

func settingsJSON(t *testing.T, s simplepush.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// rewriteTransport redirects every outgoing request to the
// httptest.Server. Mirrors the pushover test precedent (Simplepush
// has a hardcoded api.simplepush.io URL).
type rewriteTransport struct{ base string }

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.base)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}

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

func makeSimplepush(t *testing.T, srv *httptest.Server, extras func(*simplepush.Settings)) notifications.Provider {
	t.Helper()
	s := simplepush.Settings{Key: "key-xyz"}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: simplepush.Implementation,
		Settings:       settingsJSON(t, s),
	}
	client := &http.Client{Transport: &rewriteTransport{base: srv.URL}}
	p, err := simplepush.New(def, notifications.Dependencies{HTTPClient: client})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresKey(t *testing.T) {
	t.Parallel()
	fails := (&simplepush.Settings{}).Validate()
	found := false
	for _, f := range fails {
		if f.PropertyName == "Key" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Key failure, got %+v", fails)
	}
}

func TestSimplepush_OnGrab_FormPost(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeSimplepush(t, srv, func(s *simplepush.Settings) {
		s.Event = "sonarr-event"
	})
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show.S01E01"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls = %d", len(capture))
	}
	got := capture[0]
	if got.Get("key") != "key-xyz" {
		t.Errorf("key=%q", got.Get("key"))
	}
	if got.Get("event") != "sonarr-event" {
		t.Errorf("event=%q", got.Get("event"))
	}
	if got.Get("title") != "Episode Grabbed" {
		t.Errorf("title=%q", got.Get("title"))
	}
	if got.Get("msg") != "Show.S01E01" {
		t.Errorf("msg=%q", got.Get("msg"))
	}
}

func TestSimplepush_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeSimplepush(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestSimplepush_Test_SuccessRoundTrip(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeSimplepush(t, srv, nil)
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

func TestSimplepush_Test_NonSuccessReturnsFailure(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeSimplepush(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 {
		t.Error("expected failure for 500")
	}
}

func TestSimplepush_SendFailedSurfacesError(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, http.StatusBadGateway, &capture)
	defer srv.Close()
	prov := makeSimplepush(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, simplepush.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestSimplepush_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(simplepush.Implementation, simplepush.ConfigContract) {
		t.Errorf("registry missing %s/%s", simplepush.Implementation, simplepush.ConfigContract)
	}
}
