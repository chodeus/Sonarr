package host

import (
	"bytes"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// baseHrefRegex matches an existing `<base href="...">` tag in any
// quote style. Anchored to the `<base` opener so unrelated `href`
// attributes elsewhere don't match.
var baseHrefRegex = regexp.MustCompile(`(?i)<base\s+href\s*=\s*"[^"]*"\s*/?\s*>`)

// injectBaseHref returns middleware that rewrites the `<base href>`
// tag in HTML responses so the React SPA's client-side router
// resolves routes under the configured URL base (e.g. "/sonarr/").
// Mirrors the C# SPA-frontend behaviour where Sonarr's
// `UrlBaseMiddleware` rewrites index.html on the fly.
//
// Behaviour:
//   - urlBase == "" → middleware is a pass-through.
//   - Response Content-Type starts with "text/html" → buffer body,
//     rewrite `<base href="...">` to `<base href="<urlBase>/">`. If
//     no tag is present, inject one immediately after `<head>`.
//   - Other content types (JS, CSS, images, JSON) → pass-through.
//
// The handler buffers the full response so the rewrite can fire
// before the body hits the wire. For index.html (a few KB) this is
// negligible; for large bundle assets the Content-Type check keeps
// them on the fast path.
//
// Audit fix C.5.
func injectBaseHref(next http.Handler, urlBase string) http.Handler {
	if urlBase == "" {
		return next
	}
	// Normalize urlBase to "/<value>/" with leading + trailing slashes
	// so the emitted href is absolute relative to the site root.
	normalized := "/" + strings.Trim(urlBase, "/")
	if normalized == "/" {
		return next
	}
	if !strings.HasSuffix(normalized, "/") {
		normalized += "/"
	}
	tag := `<base href="` + normalized + `">`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := &baseHrefBufferingWriter{ResponseWriter: w}
		next.ServeHTTP(bw, r)
		if !strings.HasPrefix(bw.Header().Get("Content-Type"), "text/html") {
			bw.flushRaw()
			return
		}
		body := bw.body.Bytes()
		var rewritten []byte
		if baseHrefRegex.Match(body) {
			rewritten = baseHrefRegex.ReplaceAll(body, []byte(tag))
		} else {
			// Inject right after the `<head>` opening tag. If no
			// `<head>` is present, prepend the tag — the browser will
			// move it into <head> per HTML5 parsing rules.
			i := bytes.Index(bytes.ToLower(body), []byte("<head>"))
			if i < 0 {
				rewritten = append([]byte(tag), body...)
			} else {
				rewritten = append([]byte{}, body[:i+len("<head>")]...)
				rewritten = append(rewritten, []byte(tag)...)
				rewritten = append(rewritten, body[i+len("<head>"):]...)
			}
		}
		bw.flushRewritten(rewritten)
	})
}

// baseHrefBufferingWriter captures the response so injectBaseHref
// can rewrite the body before forwarding. Methods that fire before
// flushRaw/flushRewritten only buffer; the actual write to the
// underlying ResponseWriter happens at flush time.
type baseHrefBufferingWriter struct {
	http.ResponseWriter
	body   bytes.Buffer
	status int
}

// WriteHeader buffers the status; the actual call to the underlying
// ResponseWriter is deferred to flush time so headers can be
// adjusted (Content-Length on rewrite).
func (w *baseHrefBufferingWriter) WriteHeader(status int) {
	w.status = status
}

// Write buffers the body; the rewritten body hits the wire only
// after the inner handler returns.
func (w *baseHrefBufferingWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

func (w *baseHrefBufferingWriter) flushRaw() {
	w.commit(w.body.Bytes())
}

func (w *baseHrefBufferingWriter) flushRewritten(b []byte) {
	// Content-Length must reflect the new body length.
	w.Header().Del("Content-Length")
	w.commit(b)
}

func (w *baseHrefBufferingWriter) commit(body []byte) {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(status)
	_, _ = w.ResponseWriter.Write(body)
}

// IsIndexPath returns true when the URL path resolves to the React
// SPA's index.html (root, or any unmatched SPA route). Used by
// future call sites that want to short-circuit the base-href rewrite
// for non-HTML assets without buffering their response. Today the
// content-type sniff in injectBaseHref handles that, but the helper
// is exposed for tests + future tuning.
func IsIndexPath(p string) bool {
	cleaned := path.Clean(p)
	return cleaned == "/" || cleaned == "." || cleaned == "" ||
		strings.HasSuffix(cleaned, "/index.html") ||
		cleaned == "/index.html"
}
