package httpclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// realClient is the production Client backed by a Dispatcher and a
// persistent cookie jar. C# cite: src/NzbDrone.Common/Http/HttpClient.cs:41-386.
//
// The C# HttpClient delegates the socket-level work to an IHttpDispatcher;
// the Client itself is responsible for:
//   - merging persistent + per-request cookies into a Cookie header,
//   - looping the redirect chain (max 5 hops, 302/303→GET force),
//   - parsing Set-Cookie back into the jar (and into per-request cookies
//     when StoreResponseCookie is set),
//   - throwing typed errors on status >=400 unless suppressed,
//   - running pre-/post-request interceptors,
//   - calling the rate limiter before each dispatch (sub-arc 1c).
//
// Sub-arc 1d will add: per-host status tracker.
type realClient struct {
	dispatcher   Dispatcher
	rateLimiter  RateLimiter   // optional; when nil, rate-limit fields on Request are no-ops.
	interceptors []Interceptor // optional; ordered list applied around every dispatch.

	// jarMu guards persistentJar. The Go stdlib cookiejar.Jar is
	// goroutine-safe internally, but we still need our own mutex around the
	// merge-and-update sequence to keep cookie semantics consistent across
	// concurrent requests to the same host.
	jarMu         sync.Mutex
	persistentJar *cookiejar.Jar
}

// ClientOptions tunes NewClientWithOptions. Zero-value options yield the
// same behaviour as NewClient: a fresh dispatcher with the supplied
// UserAgent, no rate limiter, no interceptors, no proxy.
type ClientOptions struct {
	UserAgent     string
	Dispatcher    Dispatcher    // optional override; tests inject stubs here.
	RateLimiter   RateLimiter   // optional; when nil, Request.RateLimit is no-op.
	Interceptors  []Interceptor // optional ordered list.
	ProxyProvider ProxyProvider // optional; only consulted when Dispatcher is nil.
}

// NewClient builds a production Client. userAgent is sent on every request
// unless overridden by Request.UserAgentOverride. Returns an error only if
// the underlying cookiejar refuses to construct, which it won't.
func NewClient(userAgent string) (Client, error) {
	return NewClientWithOptions(ClientOptions{UserAgent: userAgent})
}

// NewClientWithOptions builds a Client with the supplied options. When
// opts.Dispatcher is nil, a fresh dispatcher is constructed with the
// supplied UserAgent and ProxyProvider.
func NewClientWithOptions(opts ClientOptions) (Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("httpclient: build cookie jar: %w", err)
	}
	d := opts.Dispatcher
	if d == nil {
		d = NewDispatcherWithOptions(DispatcherOptions{
			UserAgent:     opts.UserAgent,
			ProxyProvider: opts.ProxyProvider,
		})
	}
	return &realClient{
		dispatcher:    d,
		rateLimiter:   opts.RateLimiter,
		interceptors:  append([]Interceptor(nil), opts.Interceptors...),
		persistentJar: jar,
	}, nil
}

// NewClientWithDispatcher allows tests to inject a stub Dispatcher.
// Equivalent to NewClientWithOptions{Dispatcher: d}.
func NewClientWithDispatcher(d Dispatcher) (Client, error) {
	return NewClientWithOptions(ClientOptions{Dispatcher: d})
}

// MaxRedirects mirrors the constant at src/NzbDrone.Common/Http/HttpClient.cs:43.
const MaxRedirects = 5

// Execute implements Client. Mirrors HttpClient.ExecuteAsync at HttpClient.cs:65-123.
func (c *realClient) Execute(ctx context.Context, req *Request) (*Response, error) {
	c.applyPerRequestCookies(req)

	resp, err := c.executeWithRedirects(ctx, req)
	if err != nil {
		return nil, err
	}

	if !req.SuppressHTTPError && resp.HasHTTPError() && !suppressedStatus(req, resp.StatusCode) {
		if resp.StatusCode == http.StatusTooManyRequests {
			return resp, NewTooManyRequestsError(req, resp)
		}
		return resp, NewHTTPError(req, resp)
	}
	return resp, nil
}

// Get implements Client. Mirrors HttpClient.GetAsync at HttpClient.cs:318-322.
func (c *realClient) Get(ctx context.Context, req *Request) (*Response, error) {
	req.Method = http.MethodGet
	return c.Execute(ctx, req)
}

// Head implements Client. Mirrors HttpClient.HeadAsync at HttpClient.cs:343-347.
func (c *realClient) Head(ctx context.Context, req *Request) (*Response, error) {
	req.Method = http.MethodHead
	return c.Execute(ctx, req)
}

// Post implements Client. Mirrors HttpClient.PostAsync at HttpClient.cs:354-358.
func (c *realClient) Post(ctx context.Context, req *Request) (*Response, error) {
	req.Method = http.MethodPost
	return c.Execute(ctx, req)
}

// DownloadFile implements Client. Mirrors HttpClient.DownloadFileAsync at
// HttpClient.cs:264-310. Streams to a `<fileName>.part` and renames on
// success; the .part is unconditionally cleaned up on the way out.
func (c *realClient) DownloadFile(ctx context.Context, rawURL, fileName string) error {
	if dir := filepath.Dir(fileName); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("download: create dir: %w", err)
		}
	}
	partName := fileName + ".part"
	defer os.Remove(partName) //nolint:errcheck // best-effort cleanup; mirror HttpClient.cs:303-308.

	f, err := os.Create(partName)
	if err != nil {
		return fmt.Errorf("download: create part file: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()

	req, err := NewRequest(rawURL)
	if err != nil {
		return err
	}
	req.AllowAutoRedirect = true
	req.ResponseStream = f
	req.RequestTimeout = 300 * time.Second // mirror HttpClient.cs:284

	resp, err := c.Get(ctx, req)
	if err != nil {
		return err
	}

	// Mirror HttpClient.cs:287-290: if the body landed as text/html the
	// download is bogus. We can only detect this from headers because the
	// body went straight to disk.
	ct := resp.Headers.ContentType()
	if ct != "" && strings.Contains(ct, "text/html") {
		return NewHTTPErrorWithMessage(req, resp, "Site responded with html content.")
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("download: close part file: %w", err)
	}
	closed = true

	if _, err := os.Stat(fileName); err == nil {
		if err := os.Remove(fileName); err != nil {
			return fmt.Errorf("download: remove existing file: %w", err)
		}
	}
	if err := os.Rename(partName, fileName); err != nil {
		return fmt.Errorf("download: rename part: %w", err)
	}
	return nil
}

// executeWithRedirects ports the redirect loop at HttpClient.cs:71-103.
// Up to MaxRedirects hops; 302/303 force-GET when the original method was
// not idempotent.
func (c *realClient) executeWithRedirects(ctx context.Context, req *Request) (*Response, error) {
	chain := []string{req.URL.String()}
	resp, err := c.executeRequestOnce(ctx, req)
	if err != nil {
		return nil, err
	}

	for req.AllowAutoRedirect && resp.HasHTTPRedirect() {
		loc := resp.Headers.Get("Location")
		if loc == "" {
			return resp, fmt.Errorf("redirect with no Location header from %s", req.URL.String())
		}
		// Resolve relative redirect against current URL.
		nextURL, err := resolveRedirect(req.URL.String(), loc)
		if err != nil {
			return resp, fmt.Errorf("invalid redirect target %q: %w", loc, err)
		}
		req.URL = nextURL
		chain = append(chain, nextURL.String())

		if len(chain) > MaxRedirects {
			return resp, fmt.Errorf("too many automatic redirections were attempted for %s", strings.Join(chain, " -> "))
		}

		// 301/302/303 force GET when original was POST (or anything besides
		// GET/HEAD for 303). Mirrors RequestRequiresForceGet at
		// HttpClient.cs:130-138.
		if forceGetOnRedirect(resp.StatusCode, req.Method) {
			req.Method = http.MethodGet
			req.ContentData = nil
			req.ContentSummary = ""
		}

		resp, err = c.executeRequestOnce(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// executeRequestOnce ports the single-attempt body of HttpClient.ExecuteRequestAsync
// at HttpClient.cs:140-175.
func (c *realClient) executeRequestOnce(ctx context.Context, req *Request) (*Response, error) {
	// Pre-request interceptors (HttpClient.cs:142-145). May replace req.
	for _, i := range c.interceptors {
		req = i.PreRequest(req)
	}

	// Rate-limit (HttpClient.cs:147-150). Honours per-request RateLimit +
	// optional sub-key bucket. Cancellation propagates from ctx.
	if req.RateLimit > 0 && c.rateLimiter != nil {
		if err := c.rateLimiter.WaitAndPulse(ctx, req.URL.Host(), req.RateLimitKey, req.RateLimit); err != nil {
			return nil, err
		}
	}

	c.applyPerRequestCookies(req)

	resp, err := c.dispatcher.Dispatch(ctx, req)
	if err != nil {
		return nil, err
	}
	c.handleResponseCookies(req, resp)

	// Post-response interceptors (HttpClient.cs:164-167).
	for _, i := range c.interceptors {
		resp = i.PostResponse(resp)
	}
	return resp, nil
}

// applyPerRequestCookies merges the persistent jar's cookies for this URL
// with the per-request Cookies map and writes them into the request as a
// single Cookie header. Mirrors InitializeRequestCookies at HttpClient.cs:177-220.
func (c *realClient) applyPerRequestCookies(req *Request) {
	c.jarMu.Lock()
	defer c.jarMu.Unlock()

	u, err := url.Parse(req.URL.String())
	if err != nil {
		return
	}

	merged := map[string]string{}
	for _, ck := range c.persistentJar.Cookies(u) {
		merged[ck.Name] = ck.Value
	}
	for k, v := range req.Cookies {
		// Empty-value cookie deletes the entry on the server; we still send
		// it. The C# code emits an Expires=now-1d cookie; we emit Name=
		// per HTTP convention, which has the same effect.
		merged[k] = v
	}

	if len(merged) == 0 {
		req.Headers.Remove("Cookie")
		return
	}

	parts := make([]string, 0, len(merged))
	for k, v := range merged {
		parts = append(parts, k+"="+v)
	}
	req.Headers.Set("Cookie", strings.Join(parts, "; "))

	// Persist per-request cookies into the jar when StoreRequestCookie
	// is set (default true). Mirrors HttpClient.cs:211-214.
	if req.StoreRequestCookie && len(req.Cookies) > 0 {
		jarCookies := make([]*http.Cookie, 0, len(req.Cookies))
		for k, v := range req.Cookies {
			jarCookies = append(jarCookies, &http.Cookie{
				Name:    k,
				Value:   v,
				Path:    "/",
				Expires: time.Now().Add(time.Hour), // mirror HttpClient.cs:205
			})
		}
		c.persistentJar.SetCookies(u, jarCookies)
	}
}

// handleResponseCookies parses Set-Cookie headers and writes them into
// the persistent jar (when StoreResponseCookie is set). Mirrors
// HandleResponseCookies at HttpClient.cs:222-247.
func (c *realClient) handleResponseCookies(req *Request, resp *Response) {
	if !req.StoreResponseCookie {
		return
	}
	headers := resp.CookieHeaders()
	if len(headers) == 0 {
		return
	}

	c.jarMu.Lock()
	defer c.jarMu.Unlock()

	u, err := url.Parse(req.URL.String())
	if err != nil {
		return
	}

	cookies := parseSetCookieHeaders(headers)
	if len(cookies) > 0 {
		c.persistentJar.SetCookies(u, cookies)
	}
}

// parseSetCookieHeaders builds *http.Cookie values out of the raw Set-Cookie
// strings using the stdlib's parser. Each header line is one cookie; we
// build a tiny throwaway *http.Response to reuse stdlib parsing instead of
// hand-rolling.
func parseSetCookieHeaders(headers []string) []*http.Cookie {
	resp := &http.Response{Header: http.Header{}}
	for _, h := range headers {
		resp.Header.Add("Set-Cookie", h)
	}
	return resp.Cookies()
}

// forceGetOnRedirect ports HttpClient.RequestRequiresForceGet at HttpClient.cs:130-138.
func forceGetOnRedirect(status int, method string) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound:
		return method == http.MethodPost
	case http.StatusSeeOther:
		return method != http.MethodGet && method != http.MethodHead
	}
	return false
}

// resolveRedirect resolves a redirect Location relative to the current URL.
// Uses *url.URL.ResolveReference which follows RFC 3986 (the appropriate
// behaviour for HTTP redirects, even though the C# HttpUri operator + has
// non-standard merge semantics — redirects are RFC-3986 governed).
func resolveRedirect(base, loc string) (URI, error) {
	u, err := url.Parse(base)
	if err != nil {
		return URI{}, err
	}
	rel, err := url.Parse(loc)
	if err != nil {
		return URI{}, err
	}
	return ParseURI(u.ResolveReference(rel).String())
}

// suppressedStatus reports whether req asked for the given status to bypass
// HTTPError throwing. Mirrors HttpClient.cs:105.
func suppressedStatus(req *Request, code int) bool {
	if req.SuppressHTTPErrorStatusSet == nil {
		return false
	}
	_, ok := req.SuppressHTTPErrorStatusSet[code]
	return ok
}

// Compile-time interface check.
var _ Client = (*realClient)(nil)
