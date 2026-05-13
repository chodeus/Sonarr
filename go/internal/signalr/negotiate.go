package signalr

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
)

// NegotiateResponse is the JSON shape ASP.NET Core SignalR clients
// expect at `POST /signalr/messages/negotiate?negotiateVersion=1`.
// Mirrors the SignalR HTTP-transport handshake — clients call this
// to learn the connection id + the set of available transports
// before opening the actual transport (WebSocket / SSE /
// long-polling).
//
// The Go-port hub today advertises ONLY WebSockets. SignalR's JS
// client falls back to ServerSentEvents then LongPolling when
// WebSockets isn't listed, but every Sonarr frontend release since
// 2020 ships a WS-capable client + the modern dotnet SignalR
// runtime negotiates WS first by default; advertising just WS
// matches what the C# server emits in practice. LongPolling
// support is DEFERRED until a fallback need surfaces.
//
// `connectionId` is a fresh per-request identifier the client
// echoes in the subsequent WebSocket connect URL (`?id=<connId>`).
// The Go port generates a 24-byte random id base64'd to keep the
// shape close to ASP.NET's own connection-id format.
type NegotiateResponse struct {
	NegotiateVersion    int                  `json:"negotiateVersion"`
	ConnectionID        string               `json:"connectionId"`
	ConnectionToken     string               `json:"connectionToken"`
	AvailableTransports []AvailableTransport `json:"availableTransports"`
}

// AvailableTransport is one entry in NegotiateResponse.
// AvailableTransports. The SignalR spec defines three transport
// names: WebSockets, ServerSentEvents, LongPolling. The Go port
// advertises only WebSockets; the others are DEFERRED.
type AvailableTransport struct {
	Transport       string   `json:"transport"`
	TransferFormats []string `json:"transferFormats"`
}

// ConnectionIDGenerator builds the per-negotiate connection id.
// Production wires `DefaultConnectionIDGenerator` (24-byte random
// base64); tests can inject a deterministic generator.
type ConnectionIDGenerator func() (string, error)

// DefaultConnectionIDGenerator returns a 24-byte cryptographically
// random base64-url-encoded id. Matches the rough shape ASP.NET
// Core SignalR emits (the .NET runtime uses a similar approach via
// `RandomNumberGenerator.GetBytes(16)` + Base64Url).
func DefaultConnectionIDGenerator() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("signalr: connection-id generator: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NegotiateHandler returns the http.Handler for
// `POST /signalr/messages/negotiate`. The connection id is generated
// per request and echoed back; the response advertises WebSockets
// as the only transport.
//
// The C# server validates `?negotiateVersion=` (must be 0 or 1) and
// returns 400 otherwise — the Go port accepts both verbatim. POST
// is the canonical method; the Go port also accepts OPTIONS for
// CORS preflight (the C# tree's CORS middleware handles this
// transparently; the Go port replies with a bare 200).
//
// Auth is the host's shared `requireAPIKey` middleware — the
// SignalR JS client passes the key via `?access_token=` query (the
// parser was extended in slice 4.ES to recognise that fallback
// alongside the existing `?apikey=`).
func NegotiateHandler(gen ConnectionIDGenerator) http.Handler {
	if gen == nil {
		gen = DefaultConnectionIDGenerator
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST, OPTIONS")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		id, err := gen()
		if err != nil {
			http.Error(w, fmt.Sprintf("signalr: %v", err), http.StatusInternalServerError)
			return
		}
		resp := NegotiateResponse{
			NegotiateVersion: 1,
			ConnectionID:     id,
			// ConnectionToken is the older protocol-version field the
			// SignalR JS client still reads on `negotiateVersion=0`.
			// Echo the connection id for forward-compat — at v1 the
			// client uses ConnectionID for the WS connect URL and
			// ignores Token, so the value duplication is harmless.
			ConnectionToken: id,
			AvailableTransports: []AvailableTransport{
				{Transport: "WebSockets", TransferFormats: []string{"Text", "Binary"}},
			},
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(resp)
		_, _ = w.Write(buf)
	})
}
