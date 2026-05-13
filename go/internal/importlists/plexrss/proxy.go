package plexrss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrRequestFailed wraps any transport-level failure (DNS / TCP /
// non-2xx response). Mirrors C# `ImportListException` raised inside
// `RssImportBaseParser.PreProcess` at `:65-67`.
var ErrRequestFailed = errors.New("plexrss: request failed")

// ErrUnauthorized is the distinguished sentinel for 401 — Test()
// maps this to a Url property failure (consistent with the other [C4]
// providers).
var ErrUnauthorized = errors.New("plexrss: authorization failed")

// fetchFeed GETs the configured Url and returns the raw XML body.
// Mirrors C# `RssImportRequestGenerator.GetSeriesRequest` (a simple
// GET with Accept: application/rss+xml) + `RssImportBaseParser.PreProcess`
// (rejects non-200 + html-on-xml-accept). The Go port collapses both
// into a single helper.
func fetchFeed(ctx context.Context, doer httpDoer, settings *Settings) ([]byte, error) {
	url := strings.TrimSpace(settings.URL)
	if url == "" {
		return nil, fmt.Errorf("%w: empty url", ErrRequestFailed)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrRequestFailed, err)
	}
	// RSS feeds use `application/rss+xml` (or text/xml fallback). The
	// C# code sets the Accept header to HttpAccept.Rss which expands
	// to `application/rss+xml, application/xml, text/xml`.
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")

	resp, err := doer.Do(req)
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
		return nil, fmt.Errorf("%w: status %d", ErrRequestFailed, resp.StatusCode)
	}
	// HTML-on-xml-accept guard: when the server returns text/html
	// while we asked for xml, Plex (or an upstream block page) is
	// returning a content gate rather than the feed. Mirrors C#
	// `:70-74`.
	if ct := resp.Header.Get("Content-Type"); strings.Contains(strings.ToLower(ct), "text/html") {
		return nil, fmt.Errorf("%w: response is HTML, not RSS (likely upstream block)", ErrRequestFailed)
	}
	return body, nil
}
