package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// httpDoer is the minimal HTTP-client surface the proxy needs.
// Pattern-mirror of the [internal/importlists/sonarr.httpDoer]
// precedent.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrRequestFailed wraps any transport-level failure (DNS / TCP /
// non-2xx response). Mirrors C# `HttpException` raised by
// `IHttpClient` inside `CustomImportProxy.Execute` at
// `CustomImportProxy.cs:75-80` — the C# code wraps any non-2xx as
// HttpException; the Go port wraps any transport-level error or
// non-2xx as [ErrRequestFailed].
var ErrRequestFailed = errors.New("custom-import: request failed")

// ErrUnauthorized is the distinguished sentinel for 401 — Test()
// maps this to a BaseUrl property failure per C# `CustomImportProxy.Test`
// at `:47-52`.
var ErrUnauthorized = errors.New("custom-import: authorization failed")

// Series ports `CustomSeries` at
// `ImportLists/Custom/CustomAPIResource.cs:3-9`. The remote endpoint
// returns a JSON array of these.
type Series struct {
	Title  string `json:"title"`
	TvdbID int    `json:"tvdbId"`
	TmdbID int    `json:"tmdbId"`
	ImdbID string `json:"imdbId"`
}

// Proxy wraps the HTTP transport for the Custom import list.
// Mirrors C# `CustomImportProxy` at `CustomImportProxy.cs:23-81`.
//
// Goroutine-safety: httpDoer is shared across concurrent Fetch /
// Test calls. Settings is captured by value at request time.
type Proxy struct {
	doer httpDoer
}

// NewProxy builds a Proxy with the supplied transport. doer == nil
// falls back to http.DefaultClient.
func NewProxy(doer httpDoer) *Proxy {
	if doer == nil {
		return &Proxy{doer: http.DefaultClient}
	}
	return &Proxy{doer: doer}
}

// GetSeries GETs the configured BaseURL and decodes the JSON array.
// Mirrors C# `CustomImportProxy.GetSeries` + `Execute` at `:36-80`.
// An empty BaseURL returns an empty slice (mirrors C# `:69-72`).
func (p *Proxy) GetSeries(ctx context.Context, settings *Settings) ([]Series, error) {
	base := strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if base == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrRequestFailed, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: do: %w", ErrRequestFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<24))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %w", ErrRequestFailed, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrRequestFailed, resp.StatusCode, string(body))
	}
	var out []Series
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: unmarshal: %w", ErrRequestFailed, err)
	}
	return out, nil
}

// isValidRootURL is the Go port of the C# `IsValidUrl` filtered for
// the "ValidRootUrl" rule applied at `CustomSettings.cs:11`. Requires
// the value to parse + carry an http/https scheme + a non-empty host.
func isValidRootURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}
