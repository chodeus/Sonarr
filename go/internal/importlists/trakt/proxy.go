package trakt

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

// renewURL is the auth.servarr.com token-refresh endpoint. Mirrors C#
// `TraktImportBase.RenewUri` at `:21`.
//
// Mutable via [SetRenewURLForTest] for httptest seams.
var renewURL = "https://auth.servarr.com/v1/trakt_sonarr/renew"

// SetRenewURLForTest swaps the renew endpoint. Tests redirect outbound
// calls to an httptest.Server.
func SetRenewURLForTest(u string) { renewURL = u }

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrRequestFailed wraps any transport-level failure.
var ErrRequestFailed = errors.New("trakt-import: request failed")

// ErrUnauthorized is the distinguished sentinel for 401.
var ErrUnauthorized = errors.New("trakt-import: unauthorized")

// isValidRootURL is the Go port of `IsValidUrl` for the ValidRootUrl
// rule.
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

// fetchJSON GETs the configured Trakt URL with the standard headers
// + optional Bearer auth and returns the raw body bytes. Used by each
// variant's per-list-type endpoint construction.
func fetchJSON(ctx context.Context, doer httpDoer, url, accessToken string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrRequestFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", clientID)
	if t := strings.TrimSpace(accessToken); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
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
		return nil, fmt.Errorf("%w: status %d: %s", ErrRequestFailed, resp.StatusCode, string(body))
	}
	return body, nil
}

// refreshAuthToken GETs the renew endpoint + returns the new token
// bundle. Mirrors C# `TraktImportBase.RefreshToken` at `:118-145`.
func refreshAuthToken(ctx context.Context, doer httpDoer, refreshToken string) (*authToken, error) {
	endpoint := renewURL + "?refresh_token=" + url.QueryEscape(refreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build renew: %w", ErrRequestFailed, err)
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: renew do: %w", ErrRequestFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%w: renew status %d: %s", ErrRequestFailed, resp.StatusCode, string(excerpt))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: read renew: %w", ErrRequestFailed, err)
	}
	var tok authToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("%w: unmarshal renew: %w", ErrRequestFailed, err)
	}
	if tok.AccessToken == "" {
		return nil, nil
	}
	return &tok, nil
}

// urlSlug is the C# `ToUrlSlug` extension used by the List variant
// to convert a Listname into a URL-safe path segment. Lowercase +
// non-alphanumeric chars replaced by '-', collapsed runs.
func urlSlug(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == ' ' || r == '_':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
