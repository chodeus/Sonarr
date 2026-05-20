package aria2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// HTTPDoer is the slice of [*http.Client] the [Proxy] depends on.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Proxy is the XML-RPC client for an aria2c endpoint. Mirrors C#
// `Aria2Proxy` at `Aria2Proxy.cs:21-180`. Goroutine-safe: stateless
// beyond construction-time deps.
type Proxy struct {
	http HTTPDoer
	log  *slog.Logger
}

// NewProxy constructs a Proxy. http defaults to http.DefaultClient
// when nil; log defaults to slog.Default.
func NewProxy(httpDoer HTTPDoer, log *slog.Logger) *Proxy {
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{http: httpDoer, log: log}
}

// endpointURL builds the absolute URL the XML-RPC POST targets.
// Mirrors C# `XmlRpcRequestBuilder` constructor at `Aria2Proxy.cs:155`.
func endpointURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	rpcPath := strings.TrimSpace(settings.RpcPath)
	if rpcPath == "" {
		rpcPath = "/rpc"
	}
	if !strings.HasPrefix(rpcPath, "/") {
		rpcPath = "/" + rpcPath
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, settings.Host, settings.Port, rpcPath)
}

// tokenArg returns the `token:<SecretToken>` string aria2 expects
// as the first arg on every authenticated method call. Mirrors C#
// `GetToken` at `Aria2Proxy.cs:148-151`.
func tokenArg(settings Settings) string {
	return "token:" + settings.SecretToken
}

// call submits an XML-RPC methodCall and returns the parsed
// response's first param value. FaultError is surfaced verbatim;
// HTTP-level failures wrap the underlying error.
func (p *Proxy) call(ctx context.Context, settings Settings, method string, args ...any) (*rawValue, error) {
	body, err := encodeCall(method, args...)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(settings), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("aria2: build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("Accept", "text/xml")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aria2: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("aria2: %s read body: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aria2: %s status %d: %s", method, resp.StatusCode, strings.TrimSpace(truncate(string(respBody), 256)))
	}
	value, err := decodeResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("aria2: %s: %w", method, err)
	}
	return value, nil
}

// GetVersion fires aria2.getVersion + reads the `version` struct
// field from the response. Mirrors C# `GetVersion` at `:33-41`.
// Returns the version string (e.g. "1.36.0").
func (p *Proxy) GetVersion(ctx context.Context, settings Settings) (string, error) {
	value, err := p.call(ctx, settings, "aria2.getVersion", tokenArg(settings))
	if err != nil {
		return "", err
	}
	if v, ok := value.structField("version"); ok {
		return v.stringValue(), nil
	}
	return "", errors.New("aria2: getVersion: 'version' field missing from response")
}

// ErrAuthFailed is a sentinel placeholder — aria2's auth-failure
// surfaces as a FaultError with a specific message (e.g.
// "Unauthorized"); callers should match against the FaultError's
// Message field. Exposed for symmetry with the rTorrent / utorrent
// proxies.
var ErrAuthFailed = errors.New("aria2: authentication failed")

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
