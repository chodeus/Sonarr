package hadouken

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
)

// base64StdEncoding aliases the stdlib codec; defined as a package-
// level var so the [base64Encode] helper doesn't pull the import
// list onto every call site.
var base64StdEncoding = base64.StdEncoding

// HTTPDoer is the [*http.Client] slice the [Proxy] depends on.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// SystemInfo ports C# `HadoukenSystemInfo` at
// `Models/HadoukenSystemInfo.cs`. The Test path reads
// `Versions["hadouken"]` to gate against [minVersion].
type SystemInfo struct {
	Commitish string            `json:"commitish"`
	Branch    string            `json:"branch"`
	Versions  map[string]string `json:"versions"`
}

// Proxy is the JSON-RPC 2.0 client for a Hadouken endpoint. Mirrors
// C# `HadoukenProxy` at `HadoukenProxy.cs:22-184`. Goroutine-safe:
// stateless beyond the atomic request-id counter.
type Proxy struct {
	http  HTTPDoer
	log   *slog.Logger
	reqID atomic.Int64
}

// NewProxy constructs a Proxy. http=nil binds [http.DefaultClient];
// log=nil binds [slog.Default].
func NewProxy(httpDoer HTTPDoer, log *slog.Logger) *Proxy {
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{http: httpDoer, log: log}
}

// endpointURL composes the JSON-RPC POST URL. Mirrors C#
// `HadoukenProxy.ProcessRequest` URL composition at `:72-73`:
//
//	BuildBaseUrl(UseSsl, Host, Port, UrlBase) + "/api"
func endpointURL(s Settings) string {
	scheme := "http"
	if s.UseSsl {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s:%d", scheme, s.Host, s.Port)
	ub := strings.TrimSpace(s.UrlBase)
	if ub != "" && ub != "/" {
		if !strings.HasPrefix(ub, "/") {
			ub = "/" + ub
		}
		base += strings.TrimRight(ub, "/")
	}
	return base + "/api"
}

// rpcRequest is the canonical JSON-RPC 2.0 envelope. Mirrors C#
// `JsonRpcRequestBuilder` output at `HadoukenProxy.cs:75`.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int64  `json:"id"`
}

// rpcResponse is the canonical JSON-RPC 2.0 reply envelope. Result
// is `json.RawMessage` so per-method callers can deserialise into
// the typed shape they need.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      int64           `json:"id"`
}

// rpcError is the JSON-RPC 2.0 error object the server returns
// on a method failure. Mirrors C# `Json.Deserialize<JsonRpcResponse<T>>`
// + the `result.Error` check at `:103-106`.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) String() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("code=%d msg=%s", e.Code, e.Message)
}

// call submits one JSON-RPC method invocation against the
// Hadouken endpoint and unmarshals the `result` field into out.
// out=nil discards the result body.
//
// Error mapping mirrors C# `ProcessRequest<T>` at
// `HadoukenProxy.cs:70-108`:
//
//   - Transport failure → wrapped [ErrUnavailable]; the Test path
//     surfaces this as the localized "unable to connect" failure.
//   - HTTP 401 → [ErrAuth]; Test surfaces as the password failure.
//   - JSON-RPC `error` non-nil → [ErrRPC] carrying the server-side
//     code+message.
func (p *Proxy) call(ctx context.Context, s Settings, method string, params []any, out any) error {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      p.reqID.Add(1),
	})
	if err != nil {
		return fmt.Errorf("hadouken: marshal %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(s), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("hadouken: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip,deflate")
	if u := strings.TrimSpace(s.Username); u != "" {
		req.SetBasicAuth(u, s.Password)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %s", ErrAuth, method)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("hadouken: %s read body: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s status %d: %s", ErrUnavailable, method, resp.StatusCode,
			strings.TrimSpace(truncate(string(respBody), 256)))
	}

	var env rpcResponse
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("hadouken: %s unmarshal envelope: %w", method, err)
	}
	if env.Error != nil {
		return fmt.Errorf("%w: %s: %s", ErrRPC, method, env.Error.String())
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("hadouken: %s unmarshal result: %w", method, err)
		}
	}
	return nil
}

// GetSystemInfo fires `core.getSystemInfo`. Used by the Test path
// to read the running Hadouken version and enforce [minVersion].
func (p *Proxy) GetSystemInfo(ctx context.Context, s Settings) (*SystemInfo, error) {
	var info SystemInfo
	if err := p.call(ctx, s, "core.getSystemInfo", []any{}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// AddTorrentFile fires `webui.addTorrent` with the base64-encoded
// .torrent body. Mirrors C# `HadoukenProxy.AddTorrentFile` at
// `HadoukenProxy.cs:50-53`. Returns whatever string the Hadouken
// server replies with (typically the infohash); the caller is
// responsible for uppercasing and the fallback-to-req.InfoHash
// branch when the response is empty.
//
// Params order matches the C# call:
//
//	["file", "<base64-encoded-torrent>", {"label": "<Category>"}]
func (p *Proxy) AddTorrentFile(ctx context.Context, s Settings, contents []byte) (string, error) {
	var result string
	params := []any{
		"file",
		base64Encode(contents),
		labelParam(s.Category),
	}
	if err := p.call(ctx, s, "webui.addTorrent", params, &result); err != nil {
		return "", err
	}
	return result, nil
}

// AddTorrentUri fires `webui.addTorrent` with a URL (magnet link
// or HTTP(S) DownloadURL). Mirrors C# `HadoukenProxy.AddTorrentUri`
// at `HadoukenProxy.cs:55-58`. C# discards the result string and
// returns void; the Go port follows suit — the caller composes the
// DownloadID from req.InfoHash since Hadouken does not echo the
// hash back on the URL-add path reliably.
//
// Params order matches the C# call:
//
//	["url", "<url-or-magnet>", {"label": "<Category>"}]
func (p *Proxy) AddTorrentUri(ctx context.Context, s Settings, url string) error {
	params := []any{
		"url",
		url,
		labelParam(s.Category),
	}
	return p.call(ctx, s, "webui.addTorrent", params, nil)
}

// labelParam builds the third positional arg to `webui.addTorrent`:
// `{"label": "<category>"}`. The C# code passes an anonymous
// object; the Go port emits the same JSON shape.
func labelParam(category string) map[string]string {
	return map[string]string{"label": category}
}

// base64Encode wraps [encoding/base64] without taking the import at
// the top of this file (it's not referenced anywhere else). Mirrors
// C# `Convert.ToBase64String` at `HadoukenProxy.cs:52`.
func base64Encode(contents []byte) string {
	return base64StdEncoding.EncodeToString(contents)
}

// truncate clips s to n chars with an ellipsis when overflowing.
// Used to bound error-message bodies inserted into wrap chains.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ErrUnavailable wraps every transport-level failure (network
// errors, 5xx responses, non-2xx that isn't 401). Mirrors C#
// `DownloadClientUnavailableException` at `:95,98`.
var ErrUnavailable = errors.New("hadouken: unavailable")

// ErrAuth signals HTTP 401 from the JSON-RPC endpoint — Username
// or Password is wrong. Mirrors C#
// `DownloadClientAuthenticationException` (raised from C# 401
// handling deep inside `_httpClient.Execute`).
var ErrAuth = errors.New("hadouken: authentication failed")

// ErrRPC wraps every Hadouken-side error object. Mirrors C#
// `DownloadClientException("Error response received from Hadouken")`
// at `:105`.
var ErrRPC = errors.New("hadouken: rpc error")
