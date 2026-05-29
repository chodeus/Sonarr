// Package v5util holds tiny HTTP-response-rewriting helpers shared
// across v5 endpoint-family packages. Lives under `internal/` so
// Go's import scope limits use to `internal/api/v5/...` packages
// (the only legitimate callers).
//
// Background: v5 controllers use .NET 8 `TypedResults` which produce
// different status codes than v3's `[ApiController]` defaults for
// several happy-path shapes:
//
//	v3 → 200 OK + body "{}"     ─┐
//	v3 → 200 OK + empty body    ─┼─→ v5 → 204 NoContent + empty body
//	v3 → 202 Accepted + body "{}"┘
//
// Rather than re-implement each v3 handler in v5 (duplicating the
// SignalR broadcast wiring + error envelope handling + service
// dispatch), v5 family ports wrap the v3 handler via these helpers
// to demote the happy-path status while preserving error-path
// semantics verbatim.
//
// Consolidation history: the rewrite helpers were duplicated across
// J223 (importlist), J253 (metadata), and J255 (seasonpass). J256
// extracts them here (per the J243-pattern four-duplication trigger
// — the fourth wrapper need was customfilter at J257).
//
// J256.
package v5util

import (
	"bytes"
	"net/http"
	"strings"
)

// TranslateIncludeSubresources wraps a v3 handler so the v5-native
// `?includeSubresources=a,b,c` enum-array query param sets the
// equivalent v3-style boolean include flags before delegating. v5
// controllers expose sub-resource selection as one CSV/repeated enum
// array (e.g. EpisodeSubresource[]) where v3 used individual
// `?includeX=true` bools; the Go-port v5 handlers alias the v3 handlers,
// which still read the bools — so this translates the v5 param onto them.
//
// mapping maps each lower-cased subresource token to its v3 bool query
// key (e.g. "series" → "includeSeries"). Both repeated params and
// comma-separated values are accepted; token matching is
// case-insensitive. When no includeSubresources param is present the
// request passes through untouched (v3-style bools still work).
func TranslateIncludeSubresources(h http.Handler, mapping map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		vals, ok := q["includeSubresources"]
		if !ok {
			h.ServeHTTP(w, r)
			return
		}
		for _, v := range vals {
			for _, tok := range strings.Split(v, ",") {
				if key, found := mapping[strings.ToLower(strings.TrimSpace(tok))]; found {
					q.Set(key, "true")
				}
			}
		}
		q.Del("includeSubresources")
		r2 := r.Clone(r.Context())
		r2.URL.RawQuery = q.Encode()
		h.ServeHTTP(w, r2)
	})
}

// BufferingWriter is a minimal http.ResponseWriter that captures the
// status code + body + headers into memory. The wrapping handler then
// decides what to forward to the real ResponseWriter. Exported for
// callers who want to compose their own rewrite logic; most callers
// should reach for the `Rewrite*` helpers instead.
//
// Mirrors httptest.ResponseRecorder's semantics but stays in the
// production import graph.
type BufferingWriter struct {
	header     http.Header
	body       bytes.Buffer
	status     int
	statusSet  bool
	wroteBytes bool
}

// NewBufferingWriter returns a zero-value BufferingWriter with the
// default status (200) and an empty header map.
func NewBufferingWriter() *BufferingWriter {
	return &BufferingWriter{header: http.Header{}, status: http.StatusOK}
}

// Header implements http.ResponseWriter.
func (b *BufferingWriter) Header() http.Header { return b.header }

// WriteHeader implements http.ResponseWriter. First call wins to
// match net/http's real semantics.
func (b *BufferingWriter) WriteHeader(code int) {
	if b.statusSet {
		return
	}
	b.status = code
	b.statusSet = true
}

// Write implements http.ResponseWriter. If WriteHeader was not yet
// called, defaults the status to 200 OK (mirrors net/http behaviour).
func (b *BufferingWriter) Write(p []byte) (int, error) {
	if !b.statusSet {
		b.status = http.StatusOK
		b.statusSet = true
	}
	b.wroteBytes = true
	return b.body.Write(p)
}

// Status returns the captured status code.
func (b *BufferingWriter) Status() int { return b.status }

// Body returns the captured body bytes.
func (b *BufferingWriter) Body() []byte { return b.body.Bytes() }

// RewriteHappyTo204 wraps a v3 handler whose happy path writes
// `sourceStatus` + (either body `{}` OR empty body); on that exact
// shape it emits 204 NoContent + empty body. Any other status (4xx,
// 5xx, or a non-happy-shape body at sourceStatus) passes through
// verbatim, preserving the v3 error-envelope contracts.
//
// The C# v5 `TypedResults.NoContent()` always returns 204 with no
// Content-Type header, so the wrapper strips Content-Type when
// downgrading to 204.
//
// Three call patterns established by Wave 3 v5 family ports:
//   - 200 + `{}` body (most provider-family DELETE/test handlers)
//   - 200 + empty body (CustomFilter DELETE)
//   - 202 + `{}` body (SeasonPass POST)
//
// All three demote to 204 + empty per the C# v5 TypedResults.NoContent()
// convention.
//
// J256.
func RewriteHappyTo204(sourceStatus int, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := NewBufferingWriter()
		h.ServeHTTP(rec, r)
		bodyBytes := rec.Body()
		happy := rec.Status() == sourceStatus && (len(bodyBytes) == 0 || bytes.Equal(bodyBytes, []byte(`{}`)))
		if happy {
			CopyHeadersExcept(w.Header(), rec.Header(), "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		CopyHeaders(w.Header(), rec.Header())
		w.WriteHeader(rec.Status())
		_, _ = w.Write(bodyBytes)
	})
}

// RewriteSuccessTo204 is shorthand for RewriteHappyTo204(http.StatusOK, h).
// Most v3 happy-paths emit 200 + (empty or "{}") body, so this is
// the dominant convenience entry point.
//
// J256.
func RewriteSuccessTo204(h http.Handler) http.Handler {
	return RewriteHappyTo204(http.StatusOK, h)
}

// RewriteAcceptedTo204 is shorthand for RewriteHappyTo204(http.StatusAccepted, h).
// Used by v5/seasonpass (the only v3 endpoint that uses 202 for a
// non-Created success).
//
// J256.
func RewriteAcceptedTo204(h http.Handler) http.Handler {
	return RewriteHappyTo204(http.StatusAccepted, h)
}

// RewriteAcceptedToOK wraps a v3 handler whose happy path writes
// 202 Accepted + body; v5 demotes the status to 200 OK while
// preserving the body byte-for-byte. Differs from RewriteAcceptedTo204
// in that the body IS kept; this matches the v5 ImportList BulkUpdate
// shape (`TypedResults.Ok(...)` returning the updated-resources array
// where v3 emits the same array under 202).
//
// Any other status passes through verbatim.
//
// J256.
func RewriteAcceptedToOK(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := NewBufferingWriter()
		h.ServeHTTP(rec, r)
		CopyHeaders(w.Header(), rec.Header())
		status := rec.Status()
		if status == http.StatusAccepted {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(rec.Body())
	})
}

// CopyHeaders copies all entries from src to dst (additive — does
// NOT clear dst first).
func CopyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// CopyHeadersExcept copies all entries from src to dst EXCEPT the
// named header (case-insensitive). Used by the rewrite helpers to
// drop Content-Type when downgrading to 204 NoContent.
func CopyHeadersExcept(dst, src http.Header, except string) {
	for k, vs := range src {
		if http.CanonicalHeaderKey(k) == http.CanonicalHeaderKey(except) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
