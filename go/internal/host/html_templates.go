// HTML template-token substitution for the React bundle's index.html
// and login.html. Ports the literal-token replacement half of C#
// `Sonarr.Http/Frontend/Mappers/HtmlMapperBase.cs:53-70` +
// `IndexHtmlMapper.cs:45-71`.
//
// The React entrypoint at frontend/src/index.ts reads
// window.Sonarr.urlBase before its first /initialize.json fetch — the
// raw HTML on disk has `window.Sonarr = { urlBase: '__URL_BASE__' }`
// where '__URL_BASE__' is a literal placeholder the C# HtmlMapperBase
// replaces at request time with the configured UrlBase value.
//
// Without this middleware, the React bundle reads
// `window.Sonarr.urlBase === '__URL_BASE__'` and every downstream API
// path becomes `__URL_BASE__/api/v3/...` — every fetch 404s.
//
// J205 ports the three literal-token replacements (`__URL_BASE__`,
// `_THEME_`, `__MINI_PROFILER__`). The href/src URL-rewriter + cache-
// breaker arm (C# HtmlMapperBase.cs:53-67) is deferred to a follow-up
// slice — it only matters when urlBase is non-empty AND the bundle
// references assets via absolute paths. The default urlBase="" path
// (the smoke-test scenario + the default user install) doesn't need
// the rewriter to function.

package host

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"strings"
)

// htmlTemplatePlaceholders is the set of literal tokens this
// middleware substitutes. Keep the doc-comment per token tied to its
// C# origin line for review traceability.
const (
	// placeholderURLBase mirrors C# `HtmlMapperBase.cs:69`. Replaced
	// with the normalized URL base (e.g. "/sonarr" or empty).
	placeholderURLBase = "__URL_BASE__"

	// placeholderTheme mirrors C# `IndexHtmlMapper.cs:50`. Replaced
	// with the configured theme value ("auto" / "light" / "dark").
	placeholderTheme = "_THEME_"

	// placeholderMiniProfiler mirrors C# `IndexHtmlMapper.cs:58, :62, :67`.
	// C# emits the MiniProfiler include script in debug builds and an
	// empty string otherwise; the Go-port doesn't have a MiniProfiler
	// equivalent so this is always empty (matches the C# production
	// non-debug behaviour).
	placeholderMiniProfiler = "__MINI_PROFILER__"
)

// injectHTMLTemplates returns middleware that runs the three literal-
// token substitutions on `text/html` responses. Pass-through on any
// other content-type — keeps JS/CSS/image bundle assets on the fast
// path.
//
// The buffering pattern + Hijacker forwarding mirror
// [injectBaseHref]'s shape exactly so the two middlewares can compose
// without surprises (server.go chains: static → injectHTMLTemplates →
// injectBaseHref).
//
// urlBase is the post-normalisation URL base (empty or "/<value>");
// theme is the configured theme string ("auto" / "light" / "dark").
// Both come from configfile.Provider via the boot wiring.
func injectHTMLTemplates(next http.Handler, urlBase, theme string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := &htmlTemplateWriter{ResponseWriter: w}
		next.ServeHTTP(bw, r)
		if !strings.HasPrefix(bw.Header().Get("Content-Type"), "text/html") {
			bw.flushRaw()
			return
		}
		body := bw.body.String()
		body = strings.ReplaceAll(body, placeholderURLBase, urlBase)
		body = strings.ReplaceAll(body, placeholderTheme, theme)
		body = strings.ReplaceAll(body, placeholderMiniProfiler, "")
		bw.flushRewritten([]byte(body))
	})
}

// htmlTemplateWriter is the same buffering shape as
// [baseHrefBufferingWriter] — duplicated rather than shared so the two
// middlewares compose cleanly when stacked.
type htmlTemplateWriter struct {
	http.ResponseWriter
	body   bytes.Buffer
	status int
}

// WriteHeader buffers the status code; the actual call to the
// underlying ResponseWriter is deferred to flush time so the
// Content-Length header can be re-set when the substitution rewrites
// the body length.
func (w *htmlTemplateWriter) WriteHeader(status int) { w.status = status }

// Write buffers the body. The post-substitution body hits the wire
// only after the inner handler returns and flushRewritten fires.
func (w *htmlTemplateWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

// Hijack forwards to the embedded ResponseWriter when it implements
// http.Hijacker. Static-handler middleware shouldn't see WebSocket
// upgrades but keeping the wrapper transparent prevents surprises if
// the middleware stack ever changes.
func (w *htmlTemplateWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("host: htmlTemplateWriter: ResponseWriter does not implement Hijacker")
	}
	return h.Hijack()
}

func (w *htmlTemplateWriter) flushRaw() { w.commit(w.body.Bytes()) }
func (w *htmlTemplateWriter) flushRewritten(b []byte) {
	w.Header().Del("Content-Length")
	w.commit(b)
}
func (w *htmlTemplateWriter) commit(body []byte) {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(status)
	_, _ = w.ResponseWriter.Write(body)
}
