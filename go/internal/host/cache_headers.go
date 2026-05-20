package host

import (
	"net/http"
	"strings"
)

// disableCacheHeaders applies the four "do not cache" headers that
// C# Sonarr's `RequestExtensions.DisableCache` sets on every cacheable
// API response. Mirrors
// `src/Sonarr.Http/Extensions/RequestExtensions.cs:110-116` for the
// header values plus the global `Vary: Accept-Encoding` Kestrel's
// response-compression middleware adds. Removes any `Last-Modified`
// the inner handler may have set so the cache-disabled posture is
// consistent.
//
// Applied unconditionally to every API route (`/api/*`, `/ping`) via
// the route-wrapping chain in buildMux. Static-frontend responses do
// NOT pass through this middleware so the React bundle's normal
// cache-busting via filename hashes still works.
//
// Audit fix C.1.
func disableCacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&cacheHeaderWriter{ResponseWriter: w}, r)
	})
}

// cacheHeaderWriter applies the no-cache headers exactly once,
// right before the response is committed (matches C#'s
// `headers.Remove("Last-Modified"); headers["Cache-Control"] = ...`
// posture which runs in the middleware AFTER the inner handler has
// already populated headers but before Kestrel writes them to the
// wire). Wrapping the ResponseWriter lets us intercept both explicit
// `WriteHeader` calls and the implicit one Go fires from the first
// `Write`.
type cacheHeaderWriter struct {
	http.ResponseWriter
	headersSet bool
}

// WriteHeader applies the no-cache headers (idempotent) then
// forwards to the embedded ResponseWriter.
func (w *cacheHeaderWriter) WriteHeader(status int) {
	w.applyOnce()
	w.ResponseWriter.WriteHeader(status)
}

// Write applies the no-cache headers if not yet set (Go fires an
// implicit WriteHeader on the first Write, so we mirror that here),
// then forwards to the embedded ResponseWriter.
func (w *cacheHeaderWriter) Write(b []byte) (int, error) {
	w.applyOnce()
	return w.ResponseWriter.Write(b)
}

func (w *cacheHeaderWriter) applyOnce() {
	if w.headersSet {
		return
	}
	w.headersSet = true
	h := w.Header()
	h.Set("Cache-Control", "no-cache, no-store")
	h.Set("Pragma", "no-cache")
	h.Set("Expires", "-1")
	// Vary mirrors the Kestrel response-compression middleware
	// default — Accept-Encoding because the same body MAY be
	// returned gzipped or identity depending on the client's
	// Accept-Encoding header. The Go port doesn't currently negotiate
	// content-encoding but emits the header so caching proxies in
	// front of either binary behave the same.
	h.Set("Vary", "Accept-Encoding")
	// Strip Last-Modified — the "do not cache" posture would otherwise
	// be ambiguous to clients that interpret Last-Modified as a
	// caching hint.
	h.Del("Last-Modified")
}

// shouldSkipCacheHeaders returns true for request paths the cache-
// disable headers must NOT apply to. C# CacheableSpecification
// returns false (= DisableCache) for everything by default, but
// returns true (= EnableCache) for /api/v3/MediaCover/* requests so
// the React UI can long-cache series posters. The Go port matches by
// skipping the disable middleware on that path prefix — the static-
// frontend handler downstream sets its own cache headers.
//
// Path comparison is case-insensitive (mirrors C#
// `StringComparison.CurrentCultureIgnoreCase` at
// `CacheableSpecification.cs:29`).
func shouldSkipCacheHeaders(path string) bool {
	return strings.Contains(strings.ToLower(path), "/mediacover")
}
