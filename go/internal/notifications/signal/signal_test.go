package signal_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/signal"
)

type captured struct {
	Path       string
	AuthHeader string
	Body       []byte
}

func capturingServer(t *testing.T, statusCode int, body string, capture *[]captured) *httptest.Server {
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
		} else {
			w.WriteHeader(http.StatusOK)
		}
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
}

func settingsJSON(t *testing.T, s signal.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// serverAddr cracks an httptest.Server URL into Host + Port. The
// Signal proxy doesn't have a SetBaseURLForTest seam — the Settings
// already specify Host/Port. We point Settings.Host/Port at the
// httptest server.
func serverAddr(t *testing.T, srv *httptest.Server) (string, int) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return u.Hostname(), port
}

func makeSignal(t *testing.T, srv *httptest.Server, extras func(*signal.Settings)) notifications.Provider {
	t.Helper()
	host, port := serverAddr(t, srv)
	s := signal.Settings{
		Host:         host,
		Port:         port,
		SenderNumber: "+1234",
		ReceiverId:   "+5678",
	}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: signal.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := signal.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_RequireHostPortSenderReceiver(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    signal.Settings
		want string
	}{
		{"NoHost", signal.Settings{Port: 8080, SenderNumber: "+1", ReceiverId: "+2"}, "Host"},
		{"NoPort", signal.Settings{Host: "h", SenderNumber: "+1", ReceiverId: "+2"}, "Port"},
		{"NoSender", signal.Settings{Host: "h", Port: 8080, ReceiverId: "+2"}, "SenderNumber"},
		{"NoReceiver", signal.Settings{Host: "h", Port: 8080, SenderNumber: "+1"}, "ReceiverId"},
		{"OK", signal.Settings{Host: "h", Port: 8080, SenderNumber: "+1", ReceiverId: "+2"}, ""},
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

func TestSignal_OnGrab_PostsPayload(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makeSignal(t, srv, nil)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show.S01E01"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if capture[0].Path != "/v2/send" {
		t.Errorf("path=%q", capture[0].Path)
	}
	var p signal.Payload
	if err := json.Unmarshal(capture[0].Body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Number != "+1234" {
		t.Errorf("Number=%q", p.Number)
	}
	if len(p.Recipients) != 1 || p.Recipients[0] != "+5678" {
		t.Errorf("Recipients=%v", p.Recipients)
	}
	if !strings.Contains(p.Message, "Episode Grabbed") || !strings.Contains(p.Message, "Show.S01E01") {
		t.Errorf("Message=%q", p.Message)
	}
}

func TestSignal_BasicAuth_WhenBothUserAndPassSet(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, "", &capture)
	defer srv.Close()
	prov := makeSignal(t, srv, func(s *signal.Settings) {
		s.AuthUsername = "u"
		s.AuthPassword = "p"
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !strings.HasPrefix(capture[0].AuthHeader, "Basic ") {
		t.Errorf("expected Basic auth, got %q", capture[0].AuthHeader)
	}
}

func TestSignal_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeSignal(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestSignal_Test_401MapsToAuthUsername(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusUnauthorized, "", &capture)
	defer srv.Close()
	prov := makeSignal(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "AuthUsername" {
		t.Errorf("expected AuthUsername failure, got %+v", failures)
	}
}

func TestSignal_Test_InvalidGroupIdMapsToReceiverId(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusBadRequest, `{"error":"Invalid group id supplied"}`, &capture)
	defer srv.Close()
	prov := makeSignal(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "ReceiverId" {
		t.Errorf("expected ReceiverId failure, got %+v", failures)
	}
}

func TestSignal_Test_InvalidAccountMapsToSenderNumber(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusBadRequest, `{"error":"Invalid account name"}`, &capture)
	defer srv.Close()
	prov := makeSignal(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "SenderNumber" {
		t.Errorf("expected SenderNumber failure, got %+v", failures)
	}
}

func TestSignal_Test_PlainHTTPToHTTPSPortMapsToUseSsl(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusBadRequest, "400 The plain HTTP request was sent to HTTPS port", &capture)
	defer srv.Close()
	prov := makeSignal(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "UseSsl" {
		t.Errorf("expected UseSsl failure, got %+v", failures)
	}
}

func TestSignal_500SurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, "", &capture)
	defer srv.Close()
	prov := makeSignal(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, signal.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestSignal_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(signal.Implementation, signal.ConfigContract) {
		t.Errorf("registry missing %s/%s", signal.Implementation, signal.ConfigContract)
	}
}
