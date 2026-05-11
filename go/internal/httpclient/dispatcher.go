package httpclient

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Dispatcher is the low-level send-and-read surface. C# cite:
// src/NzbDrone.Common/Http/Dispatchers/IHttpDispatcher.cs:1-10 and
// ManagedHttpDispatcher.cs:47-136.
//
// A Dispatcher receives one fully-prepared Request and returns one Response.
// It does NOT follow redirects (the C# version sets AllowAutoRedirect=false
// on the underlying handler at ManagedHttpDispatcher.cs:153 — the Client
// drives the redirect chain). It does NOT manage a persistent cookie jar
// (the Client merges per-request cookies into a Cookie header before calling
// the dispatcher; Set-Cookie response headers are parsed by the Client).
// It does NOT enforce rate-limit / interceptor logic (sub-arc 1c).
//
// Sub-arc 1c will add:
//   - per-proxy-key client cache (mirrors ManagedHttpDispatcher.cs:138-145)
//   - SocketsHttpHandler.Proxy + ConnectCallback (Happy Eyeballs)
//   - ICertificateValidationService callback for TLS bypass.
type Dispatcher interface {
	Dispatch(ctx context.Context, req *Request) (*Response, error)
}

// netHTTPDispatcher is the real Dispatcher backed by net/http. It mirrors
// ManagedHttpDispatcher.GetResponseAsync at ManagedHttpDispatcher.cs:47-136.
//
// Defaults applied (mirroring CreateHttpClient at :147-177):
//   - automatic gzip decompression on (default for net/http)
//   - manual deflate decompression in handleResponseBody (Go stdlib doesn't
//     auto-decode deflate)
//   - cookie jar disabled at the transport level (Client drives cookies)
//   - redirects disabled at the transport level (Client drives redirects)
//   - default request timeout 100s when request.RequestTimeout is zero
//     (mirrors :69)
//
// brotli decompression is NOT yet implemented — the C# code enables it via
// DecompressionMethods.Brotli at :151. The Go stdlib has no brotli reader
// and andybalholm/brotli is not in the strategic plan's pre-approved deps,
// so brotli is flagged as a follow-up. Indexers that respond brotli will
// surface a `Content-Encoding: br` body the caller will need to handle (or
// we'll add the dep in 1c after sign-off).
type netHTTPDispatcher struct {
	userAgent string

	proxyProvider ProxyProvider

	// clientCache caches one *http.Client per proxy key so that the same
	// underlying connection pool is reused for requests sharing a proxy.
	// Mirrors C# ManagedHttpDispatcher._httpClientCache at :41 and
	// :138-145. The "no-proxy" key matches NO_PROXY_KEY at :19.
	cacheMu     sync.RWMutex
	clientCache map[string]*http.Client
}

// DispatcherOptions tunes the dispatcher constructor. A zero-value
// DispatcherOptions yields the same behaviour as NewDispatcher: env-based
// HTTP/HTTPS proxy via http.ProxyFromEnvironment via NoopProxyProvider,
// and the supplied UserAgent.
type DispatcherOptions struct {
	UserAgent     string
	ProxyProvider ProxyProvider
}

// NewDispatcher builds a Dispatcher using a fresh net/http client. The
// userAgent string is sent on every request unless overridden by
// Request.UserAgentOverride. No proxy is consulted for routing; callers
// that want proxy support should use NewDispatcherWithOptions.
func NewDispatcher(userAgent string) Dispatcher {
	return NewDispatcherWithOptions(DispatcherOptions{UserAgent: userAgent})
}

// NewDispatcherWithOptions builds a Dispatcher with a typed proxy provider
// and other knobs. When opts.ProxyProvider is nil, NoopProxyProvider{} is
// used (i.e. no proxy is consulted at all — env-based proxy is opt-in,
// not implicit).
func NewDispatcherWithOptions(opts DispatcherOptions) Dispatcher {
	provider := opts.ProxyProvider
	if provider == nil {
		provider = NoopProxyProvider{}
	}
	return &netHTTPDispatcher{
		userAgent:     opts.UserAgent,
		proxyProvider: provider,
		clientCache:   map[string]*http.Client{},
	}
}

// noProxyKey mirrors ManagedHttpDispatcher.NO_PROXY_KEY at :19.
const noProxyKey = "no-proxy"

// getClient returns the *http.Client to use for the given URI, honouring
// the configured ProxyProvider. Mirrors GetClient at :138-145.
func (d *netHTTPDispatcher) getClient(uri URI) (*http.Client, error) {
	settings := d.proxyProvider.GetProxySettings(uri)

	key := noProxyKey
	if settings != nil {
		key = settings.Key()
	}

	d.cacheMu.RLock()
	if c, ok := d.clientCache[key]; ok {
		d.cacheMu.RUnlock()
		return c, nil
	}
	d.cacheMu.RUnlock()

	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()
	// Re-check after acquiring write lock.
	if c, ok := d.clientCache[key]; ok {
		return c, nil
	}

	c, err := buildHTTPClient(settings)
	if err != nil {
		return nil, err
	}
	d.clientCache[key] = c
	return c, nil
}

// buildHTTPClient constructs the *http.Client mirroring CreateHttpClient at
// ManagedHttpDispatcher.cs:147-177. settings=nil ⇒ no proxy.
//
// HTTP proxies plug into [http.Transport.Proxy] via [http.ProxyURL].
// SOCKS proxies plug into [http.Transport.DialContext] via
// [ProxySettings.SOCKSDialer]. Slice 4.AA (queue [16]) added the
// SOCKS branch; previously SOCKS settings returned an error from
// URL().
func buildHTTPClient(settings *ProxySettings) (*http.Client, error) {
	transport := &http.Transport{
		DisableCompression: false, // explicit: gzip auto-decode
		MaxConnsPerHost:    12,    // mirror MaxConnectionsPerServer=12 at C# :156
	}
	if settings != nil {
		if settings.IsSOCKS() {
			dialer, err := settings.SOCKSDialer()
			if err != nil {
				return nil, fmt.Errorf("build http client for SOCKS proxy: %w", err)
			}
			transport.DialContext = dialer.DialContext
		} else {
			u, err := settings.URL()
			if err != nil {
				return nil, fmt.Errorf("build http client for proxy: %w", err)
			}
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{
		Transport: transport,
		// Manual redirect handling — return the response as-is so the
		// Client's redirect loop can inspect it. Mirrors AllowAutoRedirect=false
		// at C# :153.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 0,
	}, nil
}

// Dispatch sends one HTTP request and returns the parsed Response.
func (d *netHTTPDispatcher) Dispatch(ctx context.Context, req *Request) (*Response, error) {
	timeout := req.RequestTimeout
	if timeout == 0 {
		timeout = 100 * time.Second // C# default at ManagedHttpDispatcher.cs:69
	}

	dispatchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := d.buildHTTPRequest(dispatchCtx, req)
	if err != nil {
		return nil, err
	}

	client, err := d.getClient(req.URL)
	if err != nil {
		return nil, err
	}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		// Distinguish timeout (mirrors C# WebExceptionStatus.Timeout at :134).
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("http request timed out: %w", err)
		}
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer func() {
		// Drain + close on the way out so the connection can be reused.
		_, _ = io.Copy(io.Discard, httpResp.Body)
		_ = httpResp.Body.Close()
	}()

	body, err := readResponseBody(req, httpResp)
	if err != nil {
		return nil, fmt.Errorf("failed to read complete http response: %w", err)
	}

	headers := HeaderFromHTTP(mergeResponseHeaders(httpResp))
	resp := NewResponseFromBytes(req, headers, body, httpResp.StatusCode, httpResp.Proto)
	return resp, nil
}

// buildHTTPRequest mirrors the body of GetResponseAsync at
// ManagedHttpDispatcher.cs:49-100.
func (d *netHTTPDispatcher) buildHTTPRequest(ctx context.Context, req *Request) (*http.Request, error) {
	var body io.Reader
	if req.ContentData != nil {
		body = bytes.NewReader(req.ContentData)
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build http request: %w", err)
	}

	// User-Agent — request override wins, then dispatcher default.
	ua := req.UserAgentOverride
	if ua == "" {
		ua = d.userAgent
	}
	if ua != "" {
		httpReq.Header.Set("User-Agent", ua)
	}

	// Connection close vs keep-alive (mirror :53).
	if !req.ConnectionKeepAlive {
		httpReq.Close = true
	}

	// Basic auth (mirror :74-79).
	if req.BasicAuthUser != "" || req.BasicAuthPass != "" {
		httpReq.SetBasicAuth(req.BasicAuthUser, req.BasicAuthPass)
	}

	// Copy caller headers. The C# AddRequestHeaders (:179-229) special-cases
	// a handful of headers (Accept, Content-*, Date, Expect, Host, Range,
	// Referer, Transfer-Encoding, User-Agent, Proxy-Connection); Go's
	// http.Header treats all of these uniformly so a single loop suffices.
	// Range/Proxy-Connection raise NotImplementedException in C#; we let
	// the caller set them and pass them through as plain headers.
	if req.Headers != nil {
		for k, vs := range req.Headers.HTTP() {
			canon := http.CanonicalHeaderKey(k)
			// Skip User-Agent here — we set it above and don't want to dup.
			if canon == "User-Agent" {
				continue
			}
			// Host needs to be set on the Request, not the header map.
			if canon == "Host" && len(vs) > 0 {
				httpReq.Host = vs[0]
				continue
			}
			httpReq.Header.Del(canon)
			for _, v := range vs {
				httpReq.Header.Add(canon, v)
			}
		}
	}

	// Cookie header (the Client merges persistent + per-request cookies and
	// stuffs them into a single Cookie header before calling Dispatch). We
	// don't add anything here; Client populates request.Headers["Cookie"].

	// When the caller did NOT set Accept-Encoding, stdlib transport adds
	// `Accept-Encoding: gzip` and auto-decodes the response body. When the
	// caller DID set it (e.g. asking for deflate), stdlib leaves the body
	// compressed and decompressIfNeeded handles it via Content-Encoding.
	return httpReq, nil
}

// readResponseBody reads and (when needed) decompresses the response body.
// If req.ResponseStream is set AND the status is 200, body is streamed there
// and the returned []byte is nil. Mirrors ManagedHttpDispatcher.cs:108-119.
func readResponseBody(req *Request, httpResp *http.Response) ([]byte, error) {
	src, closer, err := decompressIfNeeded(httpResp)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	if req.ResponseStream != nil && httpResp.StatusCode == http.StatusOK {
		if _, err := io.Copy(req.ResponseStream, src); err != nil {
			return nil, err
		}
		return nil, nil
	}

	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// decompressIfNeeded inspects Content-Encoding and wraps the body reader
// with the appropriate decompressor.
//
// Cases:
//   - Content-Encoding: gzip (and net/http hasn't already auto-decoded —
//     stdlib auto-decodes only when it set Accept-Encoding implicitly; if
//     the caller set Accept-Encoding explicitly, stdlib leaves the body
//     compressed and clears the header).
//   - Content-Encoding: deflate — Go stdlib never auto-decodes; we wrap
//     with compress/zlib.
//   - Anything else — pass through.
func decompressIfNeeded(httpResp *http.Response) (io.Reader, io.Closer, error) {
	ce := strings.ToLower(strings.TrimSpace(httpResp.Header.Get("Content-Encoding")))
	switch ce {
	case "", "identity":
		return httpResp.Body, nil, nil
	case "gzip":
		gr, err := gzip.NewReader(httpResp.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("gzip decompress: %w", err)
		}
		return gr, gr, nil
	case "deflate":
		// Note: real-world "deflate" responses are usually zlib-wrapped; the
		// raw deflate variant is rarer. compress/zlib reads zlib-wrapped
		// streams. If we see a raw deflate stream we'd fall over here —
		// callers can fall back to flate.NewReader if needed.
		zr, err := zlib.NewReader(httpResp.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("deflate decompress: %w", err)
		}
		return zr, zr, nil
	default:
		// Brotli (`br`) and any other encoding pass through un-decoded; the
		// Response will carry the encoded bytes and Content-Encoding intact
		// so callers can decode if they have the codec. Flagged as follow-up.
		return httpResp.Body, nil, nil
	}
}

// mergeResponseHeaders combines the message + content headers a la
// ManagedHttpDispatcher.cs:126-128 (HttpResponseMessage.Headers and
// HttpContent.Headers are separate in .NET; Go's *http.Response.Header
// already covers both).
func mergeResponseHeaders(httpResp *http.Response) http.Header {
	out := http.Header{}
	for k, vs := range httpResp.Header {
		for _, v := range vs {
			out.Add(k, v)
		}
	}
	if httpResp.Trailer != nil {
		for k, vs := range httpResp.Trailer {
			for _, v := range vs {
				out.Add(k, v)
			}
		}
	}
	return out
}
