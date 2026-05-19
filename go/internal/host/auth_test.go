package host

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type erroringResolver struct{ err error }

func (e erroringResolver) APIKey(context.Context) (string, error) { return "", e.err }

func TestParseAPIKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		path    string
		query   string
		headers map[string]string
		want    string
	}{
		{"empty", "/api/v3/series", "", nil, ""},
		{"query_wins", "/api/v3/series", "apikey=q-key", map[string]string{"X-Api-Key": "h-key"}, "q-key"},
		{"header", "/api/v3/series", "", map[string]string{"X-Api-Key": "h-key"}, "h-key"},
		{"bearer", "/api/v3/series", "", map[string]string{"Authorization": "Bearer abc123"}, "abc123"},
		{"bearer_no_space", "/api/v3/series", "", map[string]string{"Authorization": "BearerAbc"}, "BearerAbc"},
		{"bearer_global_replace", "/api/v3/series", "", map[string]string{"Authorization": "Bearer foo Bearer bar"}, "foo bar"},
		{"empty_query_falls_through", "/api/v3/series", "apikey=", map[string]string{"X-Api-Key": "h-key"}, "h-key"},
		// On /signalr/* the SignalR scheme reads `?access_token=`
		// (WebSocket upgrades can't carry custom headers). REST
		// paths reject `?access_token=` — C# mounts the SignalR
		// scheme only on the /signalr prefix.
		{"signalr_access_token", "/signalr/messages/negotiate", "access_token=sr-key", nil, "sr-key"},
		{"rest_ignores_access_token", "/api/v3/series", "access_token=sr-key", nil, ""},
		{"rest_ignores_access_token_falls_to_header", "/api/v3/series", "access_token=sr-key", map[string]string{"X-Api-Key": "h-key"}, "h-key"},
		// On /signalr/* `?apikey=` is the REST scheme's query name
		// and the SignalR scheme rejects it.
		{"signalr_ignores_apikey", "/signalr/messages/negotiate", "apikey=q-key", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := tc.path
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if got := parseAPIKey(req); got != tc.want {
				t.Errorf("parseAPIKey: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestRequireAPIKey(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	mkServer := func(resolver APIKeyResolver) *Server {
		return New(Config{}, logger, probeFn(func(context.Context) error { return nil }), resolver)
	}

	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	cases := []struct {
		name       string
		resolver   APIKeyResolver
		headers    map[string]string
		query      string
		wantStatus int
		wantBody   string
	}{
		{"ok_header", staticAPIKey("secret"), map[string]string{"X-Api-Key": "secret"}, "", 200, "ok"},
		{"ok_query", staticAPIKey("secret"), nil, "apikey=secret", 200, "ok"},
		{"ok_bearer", staticAPIKey("secret"), map[string]string{"Authorization": "Bearer secret"}, "", 200, "ok"},
		{"missing_key", staticAPIKey("secret"), nil, "", 401, ""},
		{"wrong_key", staticAPIKey("secret"), map[string]string{"X-Api-Key": "nope"}, "", 401, ""},
		{"stored_empty_rejects", staticAPIKey(""), map[string]string{"X-Api-Key": ""}, "", 401, ""},
		{"resolver_error", erroringResolver{err: errors.New("boom")}, map[string]string{"X-Api-Key": "anything"}, "", 401, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := mkServer(tc.resolver)
			url := "/api"
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			srv.requireAPIKey(authed).ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("status: got %d want %d", rr.Code, tc.wantStatus)
			}
			if got := rr.Body.String(); got != tc.wantBody {
				t.Errorf("body: got %q want %q", got, tc.wantBody)
			}
		})
	}
}

// policyResolver is a test double that satisfies APIKeyResolver +
// AuthPolicy. The fields control the mode + CGNAT trust returned to
// the middleware. Used to drive the localhost-bypass branch end to
// end without standing up a configfile.Provider.
type policyResolver struct {
	key        string
	mode       string
	trustCgnat bool
	keyErr     error
	modeErr    error
}

func (p policyResolver) APIKey(context.Context) (string, error) { return p.key, p.keyErr }
func (p policyResolver) AuthenticationRequired(context.Context) (string, error) {
	return p.mode, p.modeErr
}
func (p policyResolver) TrustCgnatIpAddresses(context.Context) (bool, error) {
	return p.trustCgnat, nil
}

func TestRequireAPIKeyLocalBypass(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	cases := []struct {
		name       string
		resolver   policyResolver
		remoteAddr string
		wantStatus int
	}{
		// DisabledForLocalAddresses + loopback IPv4 → bypass.
		{"loopback_v4_bypass", policyResolver{key: "secret", mode: "DisabledForLocalAddresses"}, "127.0.0.1:5544", 200},
		// IPv6 loopback → bypass.
		{"loopback_v6_bypass", policyResolver{key: "secret", mode: "DisabledForLocalAddresses"}, "[::1]:5544", 200},
		// LAN class-C → bypass.
		{"lan_class_c_bypass", policyResolver{key: "secret", mode: "DisabledForLocalAddresses"}, "192.168.1.5:5544", 200},
		// LAN class-A → bypass.
		{"lan_class_a_bypass", policyResolver{key: "secret", mode: "DisabledForLocalAddresses"}, "10.0.0.3:5544", 200},
		// LAN class-B (172.16-31) → bypass.
		{"lan_class_b_bypass", policyResolver{key: "secret", mode: "DisabledForLocalAddresses"}, "172.20.0.4:5544", 200},
		// 172.32.x.x is OUTSIDE the class-B private range → reject.
		{"lan_class_b_edge_outside", policyResolver{key: "secret", mode: "DisabledForLocalAddresses"}, "172.32.0.4:5544", 401},
		// Link-local → bypass.
		{"link_local_bypass", policyResolver{key: "secret", mode: "DisabledForLocalAddresses"}, "169.254.10.5:5544", 200},
		// Public IP → reject.
		{"public_ip_reject", policyResolver{key: "secret", mode: "DisabledForLocalAddresses"}, "8.8.8.8:5544", 401},
		// CGNAT without trust flag → reject.
		{"cgnat_no_trust_reject", policyResolver{key: "secret", mode: "DisabledForLocalAddresses"}, "100.64.0.1:5544", 401},
		// CGNAT with trust flag → bypass.
		{"cgnat_with_trust_bypass", policyResolver{key: "secret", mode: "DisabledForLocalAddresses", trustCgnat: true}, "100.64.0.1:5544", 200},
		// Mode = Enabled (default) → reject local IP too.
		{"mode_enabled_no_bypass", policyResolver{key: "secret", mode: "Enabled"}, "127.0.0.1:5544", 401},
		// Mode = DisabledForLocalAddresses but valid API key supplied — still 200 (key wins).
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(Config{}, logger, probeFn(func(context.Context) error { return nil }), tc.resolver)
			req := httptest.NewRequest(http.MethodGet, "/api/v3/series", nil)
			req.RemoteAddr = tc.remoteAddr
			rr := httptest.NewRecorder()
			srv.requireAPIKey(authed).ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Errorf("status: got %d want %d", rr.Code, tc.wantStatus)
			}
		})
	}
}

func TestRequireAPIKeyValidKeyOverridesBypass(t *testing.T) {
	t.Parallel()
	// A valid API key takes the fast path; the bypass branch is not
	// even consulted. Regression guard against accidentally inverting
	// the check order.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	resolver := policyResolver{key: "secret", mode: "Enabled"}
	srv := New(Config{}, logger, probeFn(func(context.Context) error { return nil }), resolver)
	authed := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v3/series", nil)
	req.Header.Set("X-Api-Key", "secret")
	req.RemoteAddr = "8.8.8.8:5544"
	rr := httptest.NewRecorder()
	srv.requireAPIKey(authed).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
}
