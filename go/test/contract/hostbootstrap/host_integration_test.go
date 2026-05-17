// host_integration_test.go composes every slice-4.EP-onwards surface
// (host.Server, host.WithStaticFS, host.WithURLBase, signalr.Hub,
// signalr.NegotiateHandler, signalr.WebSocketHandler) inside one
// httptest.Server and asserts the dispatch precedence rules hold
// end-to-end. Phase 5's three sub-arcs (REST routes, SignalR
// fan-out, frontend bundle) each have isolated tests; this one is
// the cap that proves they compose.
//
// The shape under test is the same shape cmd/sonarr's buildHostServer
// assembles in production — single mux, /api/... + /ping + /signalr/
// + static fallback all rooted under an optional URL base. The only
// piece omitted is the bus subscriber wiring (covered by
// cmd/sonarr/signalr_wiring_test.go end-to-end already).
package hostbootstrap

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/internal/signalr"
)

// integrationFixture bundles the test server + the URL base prefix
// + the API key the routes were registered under. Sub-tests pull the
// base URL off ts and stitch the prefix + query themselves.
type integrationFixture struct {
	ts     *httptest.Server
	prefix string
	apiKey string
	hub    *signalr.Hub
}

// newIntegrationFixture spins a httptest.Server backed by a host.Server
// configured with every sub-arc-3 option (URL base + embedded FS) AND
// the SignalR + sample-API routes registered as the production binary
// would register them.
func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	const apiKey = "host-integration-test-key"
	const prefix = "/sonarr"

	// In-memory bundle stand-in. The placeholder // go:embed bundle
	// shipped in slice 4.FG also has just index.html — same shape.
	bundle := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><html><body>SPA</body></html>")},
		"static/app.js": &fstest.MapFile{Data: []byte("console.log('app');")},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := signalr.NewHub(signalr.WithHostVersion("integration-test"))

	apiPing := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv := host.New(
		host.Config{}, logger,
		okProbe{}, fixedAPIKey(apiKey),
		host.WithStaticFS(bundle),
		host.WithURLBase(prefix),
		host.WithRoutes(
			// A sample anonymous API route stands in for the per-
			// resource handlers — the precedence under test is
			// "API routes take priority over the static fallback",
			// not the per-route projection logic.
			host.Route{Method: "GET", Path: "/api/v3/ping", Handler: apiPing, Anonymous: true},
			host.Route{Method: "POST", Path: "/signalr/messages/negotiate", Handler: signalr.NegotiateHandler(nil)},
			host.Route{Method: "GET", Path: "/signalr/messages", Handler: signalr.WebSocketHandler(hub, logger)},
		),
	)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &integrationFixture{ts: ts, prefix: prefix, apiKey: apiKey, hub: hub}
}

func TestHostIntegration_SPARootUnderURLBase(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t)

	for _, path := range []string{"/sonarr", "/sonarr/", "/sonarr/series/42"} {
		resp, err := http.Get(f.ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status=%d body=%q", path, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "SPA") {
			t.Errorf("GET %s did not serve SPA index.html: %q", path, body)
		}
	}
}

func TestHostIntegration_StaticAssetUnderURLBase(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t)

	resp, err := http.Get(f.ts.URL + "/sonarr/static/app.js")
	if err != nil {
		t.Fatalf("GET static asset: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("static asset status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "console.log") {
		t.Errorf("static asset body: %q", body)
	}
}

func TestHostIntegration_APIRouteUnderURLBase(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t)

	// API route registered as Anonymous → no apikey required.
	resp, err := http.Get(f.ts.URL + "/sonarr/api/v3/ping")
	if err != nil {
		t.Fatalf("GET api route: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("api route status=%d body=%q", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Errorf("api route body: %q", body)
	}
}

func TestHostIntegration_OutOfPrefix404s(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t)

	for _, path := range []string{"/", "/api/v3/ping", "/signalr/messages/negotiate", "/sonarrextra/api/v3/ping"} {
		resp, err := http.Get(f.ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status=%d, want 404", path, resp.StatusCode)
		}
	}
}

func TestHostIntegration_SignalRNegotiateUnderURLBase(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t)

	// Negotiate is authed — apikey via the SignalR client's
	// `?access_token=` fallback (parseAPIKey learned to accept it
	// in slice 4.ES) AND via `?apikey=` (the canonical REST
	// fallback). Both work; the test uses access_token to mimic
	// the JS client.
	url := f.ts.URL + f.prefix + "/signalr/messages/negotiate?access_token=" + f.apiKey
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST negotiate: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("negotiate status=%d body=%q", resp.StatusCode, body)
	}
	var got signalr.NegotiateResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("negotiate unmarshal: %v body=%q", err, body)
	}
	if got.ConnectionID == "" {
		t.Errorf("negotiate connectionId empty: %+v", got)
	}
	if got.NegotiateVersion != 1 {
		t.Errorf("negotiateVersion=%d, want 1", got.NegotiateVersion)
	}
	wsAdvertised := false
	for _, tr := range got.AvailableTransports {
		if tr.Transport == "WebSockets" {
			wsAdvertised = true
		}
	}
	if !wsAdvertised {
		t.Errorf("WebSockets not advertised: %+v", got.AvailableTransports)
	}
}

func TestHostIntegration_NegotiateRejectsMissingKey(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t)

	url := f.ts.URL + f.prefix + "/signalr/messages/negotiate"
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST negotiate (no key): %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing-key status=%d, want 401", resp.StatusCode)
	}
}

func TestHostIntegration_WebSocketUpgradeAndBroadcast(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t)

	wsURL := "ws" + strings.TrimPrefix(f.ts.URL, "http") + f.prefix + "/signalr/messages?access_token=" + f.apiKey
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	// Complete the HubProtocol handshake first — the transport
	// reads it within HandshakeTimeout before registering with
	// the Hub.
	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	if err := c.Write(ctx, websocket.MessageText, hs); err != nil {
		t.Fatalf("ws write handshake: %v", err)
	}

	readFrame := func() []byte {
		readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
		defer readCancel()
		_, data, err := c.Read(readCtx)
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		frames := signalr.SplitFrames(data)
		if len(frames) == 0 {
			t.Fatalf("empty WS message")
		}
		return frames[0]
	}

	_ = readFrame() // handshake reply (empty {} on success)

	// Version event fires from OnConnect into Clients.All.
	versionFrame := readFrame()
	var versionInv signalr.InvocationMessage
	if err := json.Unmarshal(versionFrame, &versionInv); err != nil {
		t.Fatalf("version unmarshal: %v frame=%q", err, versionFrame)
	}
	if versionInv.Target != "receiveMessage" {
		t.Errorf("version target=%q", versionInv.Target)
	}
	if len(versionInv.Arguments) == 0 {
		t.Fatalf("version arguments empty")
	}
	argBytes, _ := json.Marshal(versionInv.Arguments[0])
	if !strings.Contains(string(argBytes), `"name":"version"`) {
		t.Errorf("version envelope: %s", argBytes)
	}
	if !strings.Contains(string(argBytes), `"version":"integration-test"`) {
		t.Errorf("version body should carry hub host version: %s", argBytes)
	}

	// Drive an out-of-band broadcast through the hub and assert
	// the WS receives it. Mirrors the production bus-subscriber
	// path (subscriber → ResourceBroadcaster → Hub.Broadcast →
	// transport).
	if err := f.hub.Broadcast(ctx, signalr.SignalRMessage{Name: "tag", Body: nil, Version: signalr.V3Version}); err != nil {
		t.Fatalf("hub broadcast: %v", err)
	}
	frame := readFrame()
	var inv signalr.InvocationMessage
	if err := json.Unmarshal(frame, &inv); err != nil {
		t.Fatalf("broadcast unmarshal: %v", err)
	}
	body, _ := json.Marshal(inv.Arguments[0])
	if !strings.Contains(string(body), `"name":"tag"`) {
		t.Errorf("broadcast envelope: %s", body)
	}
	if !strings.Contains(string(body), `"version":3`) {
		t.Errorf("broadcast version: %s", body)
	}
}

func TestHostIntegration_WebSocketRejectsMissingKey(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t)

	wsURL := "ws" + strings.TrimPrefix(f.ts.URL, "http") + f.prefix + "/signalr/messages"
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	c, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		_ = c.Close(websocket.StatusNormalClosure, "")
		t.Fatalf("ws dial without key should fail")
	}
	if resp == nil {
		t.Fatalf("ws dial: nil response")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("ws dial without key status=%d, want 401", resp.StatusCode)
	}
}
