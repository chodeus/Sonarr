package nzbget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
)

// HTTPDoer is the narrow http-client interface [Proxy] consumes.
// Same shape as deluge/qbittorrent/transmission.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Proxy is the NZBGet JSON-RPC wrapper. Ports C# `NzbgetProxy` at
// `src/NzbDrone.Core/Download/Clients/Nzbget/NzbgetProxy.cs:14-238`.
//
// NZBGet's auth shape is HTTP Basic — Authorization header attached
// on every request when Username + Password are set (no session
// token, no login flow). This is materially simpler than Deluge's
// auth.login cookie session (5.G2b) — the Proxy carries no auth
// state at all, just the request-ID counter for JSON-RPC.
//
// The endpoint is `<base>/jsonrpc` per C# `:230` (single endpoint
// for every method). NZBGet's wire envelope follows JSON-RPC 1.1:
//
//	Request:  {"version":"1.1","id":N,"method":"<name>","params":[...]}
//	Response: {"version":"1.1","result":<any>}    on success
//	          {"version":"1.1","error":{...}}     on failure
//
// 401 surfaces as ErrAuthFailed mirroring C# `:246-249`; non-2xx
// other than 401 surfaces as ErrUnexpected; transport errors as
// ErrUnavailable.
type Proxy struct {
	doer HTTPDoer
	log  *slog.Logger

	nextID atomic.Int64
}

// Sentinel errors covering the auth + transport + RPC arms.
var (
	// ErrAuthFailed surfaces an HTTP 401 / 403 from the JSON-RPC
	// endpoint — NZBGet rejects unauthenticated or wrong-creds
	// requests with these codes. Mirrors C#
	// `DownloadClientAuthenticationException` at
	// `NzbgetProxy.cs:248`.
	ErrAuthFailed = errors.New("nzbget: authentication failed")

	// ErrUnavailable surfaces a transport-level failure
	// (connection refused, TLS handshake, etc.). Mirrors C#
	// `DownloadClientUnavailableException` at `:255`.
	ErrUnavailable = errors.New("nzbget: unavailable")

	// ErrUnexpected surfaces a 5xx / unknown-status / malformed
	// response.
	ErrUnexpected = errors.New("nzbget: unexpected response")

	// ErrRPCFailure surfaces a JSON-RPC response carrying a non-
	// null `error` field. Mirrors C# `DownloadClientException`
	// raised at `:259`.
	ErrRPCFailure = errors.New("nzbget: RPC failure")
)

// NewProxy binds the deps. doer is REQUIRED; log defaults to
// slog.Default() when nil.
func NewProxy(doer HTTPDoer, log *slog.Logger) (*Proxy, error) {
	if doer == nil {
		return nil, errors.New("nzbget: NewProxy: nil HTTPDoer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{doer: doer, log: log}, nil
}

// BuildBaseURL composes the NZBGet JSON-RPC endpoint URL
// (scheme + host:port + UrlBase + "/jsonrpc"). Mirrors C#
// `NzbgetProxy.GetBaseUrl` at `:45-50` + `:230` (which appends
// "jsonrpc" via HttpUri.CombinePath).
//
// Examples:
//
//	useSsl=false host=localhost port=6789 urlBase=""        → "http://localhost:6789/jsonrpc"
//	useSsl=true  host=nzbget    port=6790 urlBase="/nzbget" → "https://nzbget:6790/nzbget/jsonrpc"
func (p *Proxy) BuildBaseURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s:%d", scheme, settings.Host, settings.Port)
	ub := strings.TrimSpace(settings.UrlBase)
	if ub != "" && ub != "/" {
		if !strings.HasPrefix(ub, "/") {
			ub = "/" + ub
		}
		base += strings.TrimRight(ub, "/")
	}
	return base + "/jsonrpc"
}

// rpcRequest is the canonical request envelope. Mirrors C#
// `JsonRpcRequest` at `Common/Http/JsonRpc/JsonRpcRequest.cs`.
// Version 1.1 is the NZBGet wire-format default per C#
// `JsonRpcRequestBuilder.cs`.
type rpcRequest struct {
	Version string `json:"version"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// rpcResponse is the canonical response envelope. Mirrors C#
// `JsonRpcResponse<TResult>` at
// `Common/Http/JsonRpc/JsonRpcResponse.cs`. Result and Error are
// raw JSON so per-method callers can deserialise into the typed
// shape they need.
type rpcResponse struct {
	Version string          `json:"version,omitempty"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError mirrors C# `JsonError` + `ErrorModel`. The surface shape
// is `{"name":<string>,"code":<int>,"message":<string>}` — name +
// code carry along for diagnostics; message is the human-readable
// failure description.
type rpcError struct {
	Name    string `json:"name"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Call dispatches one JSON-RPC method against the /jsonrpc endpoint.
// Returns the raw `result` JSON; per-method callers unmarshal into
// the typed shape they need.
//
// Auth: attaches Authorization: Basic base64(user:pass) when
// settings.Username AND settings.Password are non-empty. No session
// token, no login flow — matches the C# `NetworkCredential` arm at
// `NzbgetProxy.cs:236` which the underlying HTTP layer translates
// to a single Basic auth header.
//
// Mirrors C# `ProcessRequest` at `NzbgetProxy.cs:228-263`.
func (p *Proxy) Call(ctx context.Context, method string, params []any, settings Settings) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(rpcRequest{
		Version: "1.1",
		ID:      p.nextID.Add(1),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("nzbget: marshal %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BuildBaseURL(settings), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("nzbget: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if u := strings.TrimSpace(settings.Username); u != "" || strings.TrimSpace(settings.Password) != "" {
		req.SetBasicAuth(u, settings.Password)
	}

	resp, err := p.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("nzbget: read %s response: %w", method, readErr)
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w: HTTP %d", ErrAuthFailed, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s status=%d body=%s",
			ErrUnexpected, method, resp.StatusCode, truncate(respBody, 256))
	}

	var parsed rpcResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("nzbget: parse %s response: %w", method, err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("%w: %s (code=%d, name=%s, method=%s)",
			ErrRPCFailure, parsed.Error.Message, parsed.Error.Code, parsed.Error.Name, method)
	}
	return parsed.Result, nil
}

// truncate clamps a byte slice to n bytes for log lines.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
