package utorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// HTTPDoer is the slice of [*http.Client] the [Proxy] depends on.
// The stdlib `*http.Client` satisfies it; tests inject a stub.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Proxy is the cookie+token-authenticated HTTP client for a
// uTorrent endpoint. Mirrors C# `UTorrentProxy` at
// `UTorrentProxy.cs:30-264`.
//
// Auth flow (C# `:228-264`):
//
//  1. GET /<urlBase>/gui/token.html with Basic-Auth.
//  2. Parse `<div id='token' style='display:none;'>TOKEN</div>` from the
//     HTML body — that's the per-session token.
//  3. The response sets a GUID cookie that subsequent requests must
//     replay.
//  4. Every subsequent GET adds `?token=TOKEN&...` + the GUID cookie.
//  5. On 401/400 the token/cookie are stale → re-fetch via step 1.
//
// Goroutine-safety: the in-memory session (token + cookies) is
// guarded by a mutex; concurrent calls share the same session and
// trigger at most one re-auth at a time.
type Proxy struct {
	http HTTPDoer
	log  *slog.Logger

	mu      sync.Mutex
	token   string
	cookies []*http.Cookie
	authKey string // tracks "did settings change" so we drop stale session
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

// baseURL builds the absolute URL prefix for /gui/ requests.
// Mirrors C# `BuildRequest` constructor + `.Resource("/gui/")` at
// `:185-197`.
func baseURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	urlBase := strings.Trim(strings.TrimSpace(settings.UrlBase), "/")
	if urlBase == "" {
		return fmt.Sprintf("%s://%s:%d/gui/", scheme, settings.Host, settings.Port)
	}
	return fmt.Sprintf("%s://%s:%d/%s/gui/", scheme, settings.Host, settings.Port, urlBase)
}

// authKeyFor produces the cache key the auth state belongs to.
// Matches the C# `"{baseUrl}:{password}"` key shape at `:230` —
// settings change (host/port/password change) drops the session.
func authKeyFor(settings Settings) string {
	return baseURL(settings) + ":" + settings.Password
}

// ErrAuthFailed is returned when the token-fetch response is 401.
// Typically means Settings.Username/Password are wrong or the
// uTorrent WebUI requires login (the C# tree raises
// DownloadClientAuthenticationException for the same case).
var ErrAuthFailed = errors.New("utorrent: authentication failed (check Username + Password)")

// tokenRegex extracts the per-session token from the
// /gui/token.html body. The C# tree parses the body as XML and
// reads `FirstChild.FirstChild.InnerText` — works because the
// token.html body is a small, self-contained `<html><div>TOKEN</div></html>`
// fragment. The Go port uses a regex because the body isn't
// guaranteed-valid XML across uTorrent builds (some emit
// inline-style with single quotes that html.Parse needs but
// encoding/xml rejects).
var tokenRegex = regexp.MustCompile(`<div[^>]*id\s*=\s*['"]token['"][^>]*>([^<]+)</div>`)

// authenticate fetches /gui/token.html, extracts the per-session
// token, and captures the GUID cookies for replay. force=true
// drops any existing session (used on re-auth after a stale-token
// response).
func (p *Proxy) authenticate(ctx context.Context, settings Settings, force bool) error {
	key := authKeyFor(settings)

	p.mu.Lock()
	hasSession := !force && p.authKey == key && p.token != "" && len(p.cookies) > 0
	p.mu.Unlock()
	if hasSession {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(settings)+"token.html", nil)
	if err != nil {
		return fmt.Errorf("utorrent: build token request: %w", err)
	}
	if settings.Username != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("utorrent: token fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrAuthFailed
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("utorrent: token read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("utorrent: token fetch status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	m := tokenRegex.FindStringSubmatch(string(body))
	if len(m) < 2 || strings.TrimSpace(m[1]) == "" {
		return errors.New("utorrent: token not found in /gui/token.html response")
	}

	p.mu.Lock()
	p.authKey = key
	p.token = strings.TrimSpace(m[1])
	p.cookies = resp.Cookies()
	p.mu.Unlock()
	return nil
}

// snapshot returns the current session (token + cookies) for the
// settings key, or empty when the session is missing.
func (p *Proxy) snapshot(settings Settings) (token string, cookies []*http.Cookie, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authKey != authKeyFor(settings) || p.token == "" {
		return "", nil, false
	}
	out := make([]*http.Cookie, len(p.cookies))
	copy(out, p.cookies)
	return p.token, out, true
}

// call submits a token-authenticated GET to /gui/?<query>. The
// query already contains the per-action params; this method
// prepends the token. On 400/401 the session is invalidated and a
// single re-auth + retry happens (matches C# `:208-219`).
//
// Returns the response body bytes — callers parse as
// [Response].
func (p *Proxy) call(ctx context.Context, settings Settings, query url.Values) ([]byte, error) {
	return p.callInner(ctx, settings, query, true)
}

func (p *Proxy) callInner(ctx context.Context, settings Settings, query url.Values, allowReauth bool) ([]byte, error) {
	if err := p.authenticate(ctx, settings, false); err != nil {
		return nil, err
	}
	token, cookies, ok := p.snapshot(settings)
	if !ok {
		return nil, errors.New("utorrent: no session after authenticate")
	}

	q := url.Values{}
	q.Set("token", token)
	for k, vv := range query {
		for _, v := range vv {
			q.Add(k, v)
		}
	}

	endpoint := baseURL(settings) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("utorrent: build request: %w", err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if settings.Username != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("utorrent: %s: %w", query.Get("action"), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized) && allowReauth {
		// Stale session — drop + retry once.
		p.mu.Lock()
		p.authKey = ""
		p.token = ""
		p.cookies = nil
		p.mu.Unlock()
		if err := p.authenticate(ctx, settings, true); err != nil {
			return nil, err
		}
		return p.callInner(ctx, settings, query, false)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuthFailed
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("utorrent: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("utorrent: status %d: %s", resp.StatusCode, strings.TrimSpace(truncate(string(body), 256)))
	}
	return body, nil
}

// Response is the JSON envelope uTorrent's /gui/ returns. Mirrors
// C# `UTorrentResponse` at `UTorrentResponse.cs:7-25`. Only the
// fields the Go-port slices read are surfaced; the rest decode to
// the wire-shape but stay unused (Settings table for GetConfig,
// CacheNumber for the GetItems delta cache, etc.).
type Response struct {
	Build           int        `json:"build"`
	Torrents        [][]any    `json:"torrents"`
	Label           [][]string `json:"label"`
	TorrentsChanged [][]any    `json:"torrentp"`
	TorrentsRemoved []string   `json:"torrentm"`
	CacheNumber     string     `json:"torrentc"`
	Settings        [][]any    `json:"settings"`
}

// GetVersion fires action=getsettings and returns the `build`
// integer from the response. Mirrors C# `GetVersion` at `:48-55`.
func (p *Proxy) GetVersion(ctx context.Context, settings Settings) (int, error) {
	body, err := p.call(ctx, settings, url.Values{"action": []string{"getsettings"}})
	if err != nil {
		return 0, err
	}
	var parsed Response
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("utorrent: decode getsettings: %w", err)
	}
	return parsed.Build, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
