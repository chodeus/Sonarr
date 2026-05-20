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

// Torrent ports C# `Types/Torrent.cs:6-29`. Only the columns
// Sonarr reads are surfaced. JSON tags match the C# `[JsonProperty]`
// attributes (camelCase wire names).
type Torrent struct {
	BytesDone    int64    `json:"bytesDone"`
	Directory    string   `json:"directory"`
	Eta          int64    `json:"eta"`
	Message      string   `json:"message"`
	Name         string   `json:"name"`
	Ratio        float64  `json:"ratio"`
	SizeBytes    int64    `json:"sizeBytes"`
	Status       []string `json:"status"`
	Tags         []string `json:"tags"`
	DateFinished *int64   `json:"dateFinished,omitempty"`
}

// GetTorrents fires GET `/api/torrents` and returns the
// hash-keyed map of torrents. Mirrors C# `FloodProxy.GetTorrents`
// at `:181-188`. The wire payload is a top-level object that wraps
// the actual map under a `torrents` key — matching the C#
// `TorrentListSummary.Torrents` projection at `Types/TorrentListSummary.cs`.
func (p *Proxy) GetTorrents(ctx context.Context, s Settings) (map[string]*Torrent, error) {
	var out struct {
		Torrents map[string]*Torrent `json:"torrents"`
	}
	if err := p.callAuth(ctx, s, http.MethodGet, "torrents", nil, &out); err != nil {
		return nil, err
	}
	if out.Torrents == nil {
		out.Torrents = map[string]*Torrent{}
	}
	return out.Torrents, nil
}

// DeleteTorrent fires POST `/api/torrents/delete` with the
// `{hashes:[hash], deleteData:bool}` body. Mirrors C#
// `FloodProxy.DeleteTorrent` at `:167-179`.
func (p *Proxy) DeleteTorrent(ctx context.Context, s Settings, hash string, deleteData bool) error {
	body, err := json.Marshal(map[string]any{
		"hashes":     []string{hash},
		"deleteData": deleteData,
	})
	if err != nil {
		return fmt.Errorf("flood: marshal delete body: %w", err)
	}
	return p.callAuth(ctx, s, http.MethodPost, "torrents/delete", body, nil)
}

// SetTorrentsTags fires PATCH `/api/torrents/tags` with the
// `{hashes:[hash], tags:[tags...]}` body. Mirrors C#
// `FloodProxy.SetTorrentsTags` at `:199-213`. Used by
// MarkItemAsImported to apply PostImportTags.
func (p *Proxy) SetTorrentsTags(ctx context.Context, s Settings, hash string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	body, err := json.Marshal(map[string]any{
		"hashes": []string{hash},
		"tags":   tags,
	})
	if err != nil {
		return fmt.Errorf("flood: marshal tags body: %w", err)
	}
	return p.callAuth(ctx, s, http.MethodPatch, "torrents/tags", body, nil)
}

// GetTorrentContentPaths fires GET `/api/torrents/{hash}/contents`
// and returns the per-content `path` field. Mirrors C#
// `FloodProxy.GetTorrentContentPaths` at `:190-197`.
//
// The wire shape is `[{"path": "..."}]`; the Go port unmarshals
// directly into a slice and projects to []string.
func (p *Proxy) GetTorrentContentPaths(ctx context.Context, s Settings, hash string) ([]string, error) {
	var raw []struct {
		Path string `json:"path"`
	}
	if err := p.callAuth(ctx, s, http.MethodGet, "torrents/"+hash+"/contents", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		out = append(out, c.Path)
	}
	return out, nil
}

// AddTorrentByFile fires POST `/api/torrents/add-files` with the
// base64-encoded .torrent body. Mirrors C# `FloodProxy.AddTorrentByFile`
// at `:117-140`. Body shape:
//
//	{
//	  "files":       ["<base64-torrent>"],
//	  "tags":        ["<tag>", ...],
//	  "destination": "<settings.Destination>",   // omitted when blank
//	  "start":       true                          // omitted when StartOnAdd=false
//	}
//
// Returns no value (the wire response is opaque per C# void return).
func (p *Proxy) AddTorrentByFile(ctx context.Context, s Settings, fileBase64 string, tags []string) error {
	body := addBody(s, "files", []string{fileBase64}, tags)
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("flood: marshal add-files body: %w", err)
	}
	return p.callAuth(ctx, s, http.MethodPost, "torrents/add-files", raw, nil)
}

// AddTorrentByUrl fires POST `/api/torrents/add-urls` with the
// magnet link or HTTP URL. Mirrors C# `FloodProxy.AddTorrentByUrl`
// at `:142-165`. Symmetric to [AddTorrentByFile] — the only wire
// difference is the `urls` key.
func (p *Proxy) AddTorrentByUrl(ctx context.Context, s Settings, url string, tags []string) error {
	body := addBody(s, "urls", []string{url}, tags)
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("flood: marshal add-urls body: %w", err)
	}
	return p.callAuth(ctx, s, http.MethodPost, "torrents/add-urls", raw, nil)
}

// addBody composes the shared body for add-files / add-urls. The
// payload-key arg ("files" vs "urls") picks the array slot. Tags is
// always emitted (even when empty); destination + start are
// conditional per the C# `Add(...)` guards at `:127-135,152-160`.
//
// We use a [map[string]any] (rather than a typed struct) so the
// optional fields can be omitted without resorting to omitempty
// gymnastics — matches the C# `Dictionary<string, object>` shape.
func addBody(s Settings, payloadKey string, payload, tags []string) map[string]any {
	if tags == nil {
		tags = []string{}
	}
	body := map[string]any{
		payloadKey: payload,
		"tags":     tags,
	}
	if strings.TrimSpace(s.Destination) != "" {
		body["destination"] = s.Destination
	}
	if s.StartOnAdd {
		body["start"] = true
	}
	return body
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
