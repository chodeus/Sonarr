package flood

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
	"sync"
)

// HTTPDoer is the [*http.Client] slice the [Proxy] depends on.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Proxy is the HTTP client for a Flood endpoint. Mirrors C#
// `FloodProxy` at `FloodProxy.cs:25-223`. Goroutine-safe: the in-
// memory cookie cache is guarded by a mutex.
type Proxy struct {
	http HTTPDoer
	log  *slog.Logger

	mu      sync.Mutex
	cookies map[string][]*http.Cookie // authKey → cached cookie list
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
	return &Proxy{
		http:    httpDoer,
		log:     log,
		cookies: map[string][]*http.Cookie{},
	}
}

// baseURL composes the absolute URL stem (no `/api` suffix —
// callers compose the resource path on top). Mirrors C#
// `FloodProxy.BuildUrl` at `:38-41`.
func baseURL(s Settings) string {
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
	return base
}

// apiURL composes the final `<base>/api/<resource>` URL the request
// targets. Resource is normalised to leading-slash form so callers
// can pass either `auth/verify` or `/auth/verify` interchangeably.
func apiURL(s Settings, resource string) string {
	r := strings.TrimLeft(resource, "/")
	return baseURL(s) + "/api/" + r
}

// authKey identifies a unique cookie-cache slot for the in-memory
// session. Mirrors C# `BuildCachedCookieKey` at `FloodProxy.cs:43-46`
// with the `{baseUrl}:{username}` format.
func authKey(s Settings) string { return baseURL(s) + ":" + s.Username }

// authBody is the JSON body for `auth/authenticate`. Mirrors C#
// `:93-97`'s anonymous-object body.
type authBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// authenticate runs the POST /api/auth/authenticate flow and caches
// the returned cookies under [authKey]. Force=true bypasses the
// cache (used by the auth-retry branch on 401/403).
//
// Mirrors C# `FloodProxy.AuthAuthenticate` at `:85-106`. The Go
// port stores cookies on the [*http.Response].Cookies() list
// rather than the C# `response.GetCookies()` map — the wire shape
// is identical (Set-Cookie headers).
func (p *Proxy) authenticate(ctx context.Context, s Settings, force bool) ([]*http.Cookie, error) {
	key := authKey(s)

	if !force {
		p.mu.Lock()
		if c, ok := p.cookies[key]; ok {
			p.mu.Unlock()
			return c, nil
		}
		p.mu.Unlock()
	} else {
		p.mu.Lock()
		delete(p.cookies, key)
		p.mu.Unlock()
	}

	body, err := json.Marshal(authBody{Username: s.Username, Password: s.Password})
	if err != nil {
		return nil, fmt.Errorf("flood: marshal auth body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL(s, "auth/authenticate"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("flood: build auth/authenticate request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: auth/authenticate: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: auth/authenticate status %d", ErrAuth, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: auth/authenticate status %d", ErrUnavailable, resp.StatusCode)
	}

	cookies := resp.Cookies()
	p.mu.Lock()
	p.cookies[key] = cookies
	p.mu.Unlock()
	return cookies, nil
}

// AuthVerify fires GET `/api/auth/verify`. Mirrors C# `:108-115`.
// Used by the Test arm; succeeds (returns nil) when the auth cookie
// flow lands a 2xx, errors with [ErrAuth] on 401/403 or
// [ErrUnavailable] otherwise.
func (p *Proxy) AuthVerify(ctx context.Context, s Settings) error {
	return p.callAuth(ctx, s, http.MethodGet, "auth/verify", nil, nil)
}

// callAuth runs an authenticated request: ensures cookies (cached
// when available, fresh authenticate otherwise), attaches them, and
// fires the resource call. When the cookies came from the cache AND
// the resource call returns 401/403, the proxy re-authenticates
// once and retries the resource call (matches the Sonarr-healthcheck
// behaviour where a stale cookie can be picked up across restarts).
// A FRESH authenticate that fails surfaces [ErrAuth] immediately —
// no retry, since re-running the same credentials would just produce
// the same failure.
//
// body=nil → no Content-Type/body sent. out=nil → response body
// discarded.
func (p *Proxy) callAuth(ctx context.Context, s Settings, method, resource string, body []byte, out any) error {
	cookies, fromCache, err := p.ensureCookies(ctx, s, false)
	if err != nil {
		return err
	}
	err = p.resourceCall(ctx, s, method, resource, body, cookies, out)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrAuth) || !fromCache {
		return err
	}
	// Stale cached cookie → force reauth + retry the resource call once.
	p.log.Debug("flood: resource auth failed with cached cookies → reauth + retry", "resource", resource)
	cookies, _, err = p.ensureCookies(ctx, s, true)
	if err != nil {
		return err
	}
	return p.resourceCall(ctx, s, method, resource, body, cookies, out)
}

// ensureCookies returns the cookie set for s. fromCache=true means
// the cookies came from the in-memory cache; false means they were
// freshly fetched via [Proxy.authenticate].
func (p *Proxy) ensureCookies(ctx context.Context, s Settings, force bool) ([]*http.Cookie, bool, error) {
	if !force {
		p.mu.Lock()
		if c, ok := p.cookies[authKey(s)]; ok {
			p.mu.Unlock()
			return c, true, nil
		}
		p.mu.Unlock()
	}
	c, err := p.authenticate(ctx, s, force)
	return c, false, err
}

// resourceCall fires one HTTP request with the supplied cookies.
// No retry, no auth handling beyond status-code classification.
func (p *Proxy) resourceCall(ctx context.Context, s Settings, method, resource string, body []byte, cookies []*http.Cookie, out any) error {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiURL(s, resource), reqBody)
	if err != nil {
		return fmt.Errorf("flood: build %s %s: %w", method, resource, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, resource, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: %s status %d", ErrAuth, resource, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return fmt.Errorf("%w: %s status %d: %s", ErrUnavailable, resource, resp.StatusCode,
			strings.TrimSpace(truncate(string(respBody), 256)))
	}
	if out != nil {
		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
		if err != nil {
			return fmt.Errorf("flood: %s read body: %w", resource, err)
		}
		if len(respBody) > 0 {
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("flood: %s unmarshal: %w", resource, err)
			}
		}
	}
	return nil
}

// truncate clips s to n with an ellipsis for error-message bodies.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ErrUnavailable wraps every transport-level failure (network
// errors + non-2xx other than 401/403). Mirrors C#
// `DownloadClientException("Unable to connect to Flood, please
// check your settings")` at `FloodProxy.cs:77,81`.
var ErrUnavailable = errors.New("flood: unavailable")

// ErrAuth wraps every 401/403. Mirrors C#
// `DownloadClientAuthenticationException("Failed to authenticate
// with Flood")` at `FloodProxy.cs:74`.
var ErrAuth = errors.New("flood: authentication failed")
