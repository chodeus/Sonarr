package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// HTTPDoer is the narrow http-client interface [Proxy] consumes. The
// production binding is `*http.Client` (or the project's
// `*httpclient.Client`). Mirrors the Transmission family pattern at
// `internal/downloadclients/transmission/proxy.go:21-23`.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Proxy is the qBittorrent WebUI HTTP wrapper. Ports C#
// `QBittorrentProxyV2` at
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrentProxyV2.cs:14-449`
// (with the V1 fallback deliberately omitted — V1 is the pre-3.2.0
// API, which qBittorrent itself retired years ago; the Go port
// targets v2 only). The auth contract differs from Transmission:
//
//   - With ApiKey: every request carries `Authorization: Bearer <key>`;
//     no login round-trip. Mirrors the C# `BuildRequest` arm at
//     `QBittorrentProxyV2.cs:355-367`.
//   - With Username + Password: POST /api/v2/auth/login with
//     form-encoded `username=&password=` returns a SID cookie that
//     subsequent requests must replay; on 403 the proxy re-runs
//     login once. Mirrors C# `AuthenticateClient` at `:417-484`
//     and the reauth-once branch at `:391-398`.
//
// The SID cache is keyed on (baseURL, username, password) to mirror
// the C# cache key at `:434`. Goroutine-safe via sync.RWMutex.
type Proxy struct {
	doer HTTPDoer
	log  *slog.Logger

	mu      sync.RWMutex
	cookies map[string]string // authKey → SID cookie value
}

// Auth-flow + transport sentinel errors. Wrapping these via
// fmt.Errorf("%w: …", …) lets callers `errors.Is` against the typed
// arm without losing the human-readable context.
var (
	// ErrAuthFailed surfaces a login round-trip that returned a
	// non-"Ok." body, a 401, or a 403 that survived the reauth
	// retry. Mirrors C# `DownloadClientAuthenticationException` at
	// `:454,484`.
	ErrAuthFailed = errors.New("qbittorrent: authentication failed")

	// ErrIPBanned surfaces the qBittorrent "User's IP is banned for
	// too many failed login attempts" response body the WebUI
	// returns with HTTP 403 after repeated bad-credential attempts.
	// Distinguished from ErrAuthFailed so the orchestrator can stop
	// retrying instead of triggering further lockout.
	ErrIPBanned = errors.New("qbittorrent: ip banned by webui (too many failed login attempts)")

	// ErrUnavailable surfaces a transport-level failure
	// (connection refused, TLS handshake, etc.). Mirrors C#
	// `DownloadClientUnavailableException` at `:69,480`.
	ErrUnavailable = errors.New("qbittorrent: unavailable")

	// ErrUnexpected surfaces a 5xx / unknown-status / malformed
	// response.
	ErrUnexpected = errors.New("qbittorrent: unexpected response")
)

// NewProxy binds the deps. doer is REQUIRED; log defaults to
// slog.Default() when nil. Mirrors Transmission's
// [transmission.NewProxy].
func NewProxy(doer HTTPDoer, log *slog.Logger) (*Proxy, error) {
	if doer == nil {
		return nil, errors.New("qbittorrent: NewProxy: nil HTTPDoer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{doer: doer, log: log, cookies: map[string]string{}}, nil
}

// BuildBaseURL composes the absolute WebUI root (scheme + host +
// port + UrlBase, no trailing slash). Mirrors the C# `BuildRequest`
// constructor at `QBittorrentProxyV2.cs:355-360` which feeds
// `HttpRequestBuilder(useSsl, host, port, urlBase)`.
//
// Examples:
//
//	useSsl=false host=localhost port=8080 urlBase=""              → "http://localhost:8080"
//	useSsl=true  host=qbit       port=8443 urlBase="/qbittorrent" → "https://qbit:8443/qbittorrent"
//
// Callers compose the API path (e.g. "/api/v2/auth/login") on top.
func (p *Proxy) BuildBaseURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s:%d", scheme, settings.Host, settings.Port)
	ub := strings.TrimSpace(settings.UrlBase)
	if ub != "" {
		if !strings.HasPrefix(ub, "/") {
			ub = "/" + ub
		}
		base += strings.TrimRight(ub, "/")
	}
	return base
}

// EnsureAuth runs the login flow when needed and returns the SID
// cookie value the caller should attach to subsequent requests.
// Returns an empty string (no error) when the credentials route
// through ApiKey — caller attaches the Authorization header
// instead.
//
// force=true bypasses the cache (used by the 403 reauth branch in
// DoRequest).
//
// Mirrors C# `AuthenticateClient` at `QBittorrentProxyV2.cs:417-484`.
func (p *Proxy) EnsureAuth(ctx context.Context, settings Settings, force bool) (string, error) {
	if strings.TrimSpace(settings.ApiKey) != "" {
		return "", nil
	}
	// No-credentials case (some qBittorrent installs run with
	// WebUI auth disabled) — mirror C# `:423-430`: skip login but
	// return success so the request proceeds anonymously.
	if strings.TrimSpace(settings.Username) == "" && strings.TrimSpace(settings.Password) == "" {
		return "", nil
	}

	key := p.authKey(settings)

	if !force {
		p.mu.RLock()
		if sid, ok := p.cookies[key]; ok {
			p.mu.RUnlock()
			return sid, nil
		}
		p.mu.RUnlock()
	} else {
		p.mu.Lock()
		delete(p.cookies, key)
		p.mu.Unlock()
	}

	form := url.Values{}
	form.Set("username", settings.Username)
	form.Set("password", settings.Password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BuildBaseURL(settings)+"/api/v2/auth/login",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("qbittorrent: build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// qBittorrent's WebUI requires a Referer matching the BaseURL
	// or it returns 403. The C# HttpRequestBuilder sets this
	// automatically via the BaseUrl arg; the Go port sets it
	// explicitly. Drop the SetReferer pattern from the C# code at
	// `:443` — qBittorrent only checks Referer host, not full
	// URL, so the BaseURL suffices.
	req.Header.Set("Referer", p.BuildBaseURL(settings))

	resp, err := p.doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(string(body))

	switch resp.StatusCode {
	case http.StatusOK:
		// qBittorrent returns plaintext "Ok." on success, "Fails."
		// on bad credentials, or an "IP banned" string on lockout
		// (HTTP 200 in some builds, 403 in others). Mirror C#
		// `:472-478` — non-"Ok." body = auth failure.
		if strings.Contains(strings.ToLower(bodyStr), "banned") {
			return "", ErrIPBanned
		}
		if bodyStr != "" && bodyStr != "Ok." {
			return "", fmt.Errorf("%w: response=%q", ErrAuthFailed, truncate(body, 64))
		}
	case http.StatusUnauthorized:
		return "", fmt.Errorf("%w: HTTP 401", ErrAuthFailed)
	case http.StatusForbidden:
		if strings.Contains(strings.ToLower(bodyStr), "banned") {
			return "", ErrIPBanned
		}
		return "", fmt.Errorf("%w: HTTP 403", ErrAuthFailed)
	default:
		return "", fmt.Errorf("%w: login status=%d body=%s", ErrUnexpected, resp.StatusCode, truncate(body, 256))
	}

	// Extract the SID cookie. qBittorrent names it "SID" — pull
	// the first match and cache it.
	var sid string
	for _, c := range resp.Cookies() {
		if c.Name == "SID" {
			sid = c.Value
			break
		}
	}
	if sid == "" {
		// Some reverse-proxy configurations strip cookies on the
		// way back; surface as auth failure rather than silently
		// proceed without one.
		return "", fmt.Errorf("%w: login succeeded but no SID cookie returned", ErrAuthFailed)
	}

	p.mu.Lock()
	p.cookies[key] = sid
	p.mu.Unlock()
	return sid, nil
}

// DoRequest fires one HTTP request against the WebUI. Attaches the
// SID cookie OR the Authorization Bearer header per settings, runs
// the 403-reauth-once retry the C# code does at
// `QBittorrentProxyV2.cs:391-398`, and returns the response status
// + body bytes.
//
// `body` may be nil; `contentType` is required when body is non-nil.
func (p *Proxy) DoRequest(ctx context.Context, method, path string, body io.Reader, contentType string, settings Settings) (int, []byte, error) {
	// Snapshot body so the reauth retry can replay it.
	var raw []byte
	if body != nil {
		var err error
		raw, err = io.ReadAll(body)
		if err != nil {
			return 0, nil, fmt.Errorf("qbittorrent: read request body: %w", err)
		}
	}

	sid, err := p.EnsureAuth(ctx, settings, false)
	if err != nil {
		return 0, nil, err
	}

	status, respBody, err := p.fireOnce(ctx, method, path, raw, contentType, settings, sid)
	if err != nil {
		return 0, nil, err
	}

	// 403 → reauth + replay once. Skip the reauth branch when the
	// auth scheme is ApiKey-only (no session to refresh).
	if status == http.StatusForbidden && strings.TrimSpace(settings.ApiKey) == "" &&
		(strings.TrimSpace(settings.Username) != "" || strings.TrimSpace(settings.Password) != "") {
		sid, err = p.EnsureAuth(ctx, settings, true)
		if err != nil {
			return 0, nil, err
		}
		status, respBody, err = p.fireOnce(ctx, method, path, raw, contentType, settings, sid)
		if err != nil {
			return 0, nil, err
		}
	}
	return status, respBody, nil
}

// fireOnce dispatches a single HTTP request with the supplied SID +
// auth scheme. Used by DoRequest's first attempt + reauth replay.
func (p *Proxy) fireOnce(ctx context.Context, method, path string, body []byte, contentType string, settings Settings, sid string) (int, []byte, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	target := p.BuildBaseURL(settings) + path

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("qbittorrent: build request %s %s: %w", method, path, err)
	}
	if len(body) > 0 && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", p.BuildBaseURL(settings))

	if apiKey := strings.TrimSpace(settings.ApiKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	} else if sid != "" {
		req.AddCookie(&http.Cookie{Name: "SID", Value: sid})
	}

	resp, err := p.doer.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("qbittorrent: read response body: %w", readErr)
	}
	return resp.StatusCode, respBody, nil
}

// GetVersion fires GET /api/v2/app/version. Mirrors C#
// `QBittorrentProxyV2.GetVersion` at
// `QBittorrentProxyV2.cs:80-88`. The Test slice (5.G1c) drives this
// directly via DoRequest because it wants finer-grained
// failure-vs-error control; GetItems + Download routing prefer this
// wrapper for the version-gated branches.
func (p *Proxy) GetVersion(ctx context.Context, settings Settings) (string, error) {
	status, body, err := p.DoRequest(ctx, http.MethodGet, "/api/v2/app/version", nil, "", settings)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("%w: GET /api/v2/app/version status=%d body=%s",
			ErrUnexpected, status, truncate(body, 128))
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(body)), "v")), nil
}

// GetTorrents fires GET /api/v2/torrents/info. Mirrors C#
// `QBittorrentProxyV2.GetTorrents` at `QBittorrentProxyV2.cs:96-106`.
// When settings.TvCategory is non-empty, applies it as the
// ?category= filter so qBittorrent returns only matching torrents
// — mirrors C# `:99-102`. Returns the JSON-deserialised slice; an
// empty array maps to an empty slice (never nil).
func (p *Proxy) GetTorrents(ctx context.Context, settings Settings) ([]QBittorrentTorrent, error) {
	path := "/api/v2/torrents/info"
	if tvCat := strings.TrimSpace(settings.TvCategory); tvCat != "" {
		q := url.Values{}
		q.Set("category", tvCat)
		path = path + "?" + q.Encode()
	}
	status, body, err := p.DoRequest(ctx, http.MethodGet, path, nil, "", settings)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w: GET /api/v2/torrents/info status=%d", ErrAuthFailed, status)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: GET /api/v2/torrents/info status=%d body=%s",
			ErrUnexpected, status, truncate(body, 256))
	}
	var torrents []QBittorrentTorrent
	if err := json.Unmarshal(body, &torrents); err != nil {
		return nil, fmt.Errorf("qbittorrent: parse /api/v2/torrents/info response: %w", err)
	}
	if torrents == nil {
		torrents = []QBittorrentTorrent{}
	}
	return torrents, nil
}

// authKey is the cache key (baseURL, username, password). Mirrors
// the C# cache key at `QBittorrentProxyV2.cs:434`. Password is part
// of the key so a config-edit invalidates the cached cookie
// naturally.
func (p *Proxy) authKey(settings Settings) string {
	return p.BuildBaseURL(settings) + ":" + settings.Username + ":" + settings.Password
}

// truncate clamps a byte slice to n bytes for log lines. Mirrors
// transmission/proxy.go's helper but kept package-local — these
// are not on the cross-package surface.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
