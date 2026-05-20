package rtorrent

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
// The stdlib `*http.Client` satisfies it; tests inject a stub.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Proxy is the XML-RPC client for an rTorrent endpoint. Mirrors C#
// `RTorrentProxy` at `RTorrentProxy.cs:28-217`. The endpoint URL is
// built per-call from the settings (so a single Proxy can serve
// multiple Settings instances — matches the C#
// `RTorrentSettings settings` per-method-arg pattern).
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
// Mirrors C# `XmlRpcRequestBuilder` constructor at
// `RTorrentProxy.cs:185-191` — scheme from UseSsl, host/port from
// settings, path = UrlBase (default "RPC2"). Empty UrlBase falls
// back to "RPC2" matching the C# default at
// `RTorrentSettings.cs:27`.
func endpointURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	urlBase := strings.TrimSpace(settings.UrlBase)
	urlBase = strings.Trim(urlBase, "/")
	if urlBase == "" {
		urlBase = "RPC2"
	}
	return fmt.Sprintf("%s://%s:%d/%s", scheme, settings.Host, settings.Port, urlBase)
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
		return nil, fmt.Errorf("rtorrent: build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("Accept", "text/xml")
	if strings.TrimSpace(settings.Username) != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rtorrent: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("rtorrent: %s read body: %w", method, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rtorrent: %s status %d: %s", method, resp.StatusCode, strings.TrimSpace(truncate(string(respBody), 256)))
	}

	value, err := decodeResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("rtorrent: %s: %w", method, err)
	}
	return value, nil
}

// ErrUnauthorized is returned when the rTorrent endpoint responds
// 401 — typically because Settings.Username/Password are wrong or
// the rTorrent .htaccess rejected the credentials.
var ErrUnauthorized = errors.New("rtorrent: 401 Unauthorized (check Username + Password)")

// GetVersion fires system.client_version. Mirrors C# `:35-40`.
// Returns "0.0.0" when the response is missing a <string> value
// (matches the C# `.FirstOrDefault()?.Value ?? "0.0.0"` posture).
func (p *Proxy) GetVersion(ctx context.Context, settings Settings) (string, error) {
	value, err := p.call(ctx, settings, "system.client_version")
	if err != nil {
		return "", err
	}
	if v := value.stringValue(); v != "" {
		return v, nil
	}
	return "0.0.0", nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
