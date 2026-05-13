package prowl_test

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
	"sonarr.tv/sonarr/internal/notifications/prowl"
)

func settingsJSON(t *testing.T, s prowl.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// rewriteTransport redirects every outgoing request to httptest.Server.
// Prowl posts to a hardcoded host so we rewrite per pushover precedent.
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

func makeProwl(t *testing.T, srv *httptest.Server, extras func(*prowl.Settings)) notifications.Provider {
	t.Helper()
	s := prowl.Settings{ApiKey: "key-xyz", Priority: int(prowl.PriorityNormal)}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: prowl.Implementation,
		Settings:       settingsJSON(t, s),
	}
	client := &http.Client{Transport: &rewriteTransport{base: srv.URL}}
	p, err := prowl.New(def, notifications.Dependencies{HTTPClient: client})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequiresApiKey(t *testing.T) {
	t.Parallel()
	fails := (&prowl.Settings{Priority: 0}).Validate()
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

func TestSettings_PriorityRange(t *testing.T) {
	t.Parallel()
	for _, p := range []int{-3, 3, 10} {
		fails := (&prowl.Settings{ApiKey: "x", Priority: p}).Validate()
		found := false
		for _, f := range fails {
			if f.PropertyName == "Priority" {
				found = true
			}
		}
		if !found {
			t.Errorf("priority=%d: expected Priority failure, got %+v", p, fails)
		}
	}
}

func TestProwl_OnGrab_FormPost(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeProwl(t, srv, func(s *prowl.Settings) {
		s.Priority = int(prowl.PriorityHigh)
	})
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	got := capture[0]
	if got.Get("apikey") != "key-xyz" {
		t.Errorf("apikey=%q", got.Get("apikey"))
	}
	if got.Get("application") != "Sonarr" {
		t.Errorf("application=%q", got.Get("application"))
	}
	if got.Get("event") != "Episode Grabbed" {
		t.Errorf("event=%q", got.Get("event"))
	}
	if got.Get("description") != "Show" {
		t.Errorf("description=%q", got.Get("description"))
	}
	if got.Get("priority") != "1" {
		t.Errorf("priority=%q", got.Get("priority"))
	}
}

func TestProwl_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeProwl(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestProwl_Test_SuccessRoundTrip(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeProwl(t, srv, nil)
	failures, err := prov.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
	if capture[0].Get("event") != "Test Notification" {
		t.Errorf("event=%q", capture[0].Get("event"))
	}
}

func TestProwl_InvalidApiKeySurfacesSentinel(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, http.StatusUnauthorized, &capture)
	defer srv.Close()
	prov := makeProwl(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, prowl.ErrInvalidAPIKey) {
		t.Errorf("err=%v want ErrInvalidAPIKey", err)
	}
}

func TestProwl_5xxSurfacesSendFailed(t *testing.T) {
	t.Parallel()
	var capture []url.Values
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeProwl(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, prowl.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestProwl_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(prowl.Implementation, prowl.ConfigContract) {
		t.Errorf("registry missing %s/%s", prowl.Implementation, prowl.ConfigContract)
	}
}
