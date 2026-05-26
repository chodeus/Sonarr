// Package seasonpass is the v5 wire surface for /api/v5/seasonpass.
//
// The v5 controller (src/Sonarr.Api.V5/SeasonPass/SeasonPassController.cs)
// is a single-POST endpoint at 99 LOC of C# source. The C# implementation
// mirrors v3 line-for-line on body shape (`SeasonPassResource` with
// `Series` + nullable `MonitoringOptions`); the ONLY divergence is the
// success status code:
//
//	v3 endpoint  : POST /api/v3/seasonpass → 202 Accepted + body `{}`
//	v5 endpoint  : POST /api/v5/seasonpass → 204 NoContent + empty body
//
// Per the Wave 3 pattern from J253/J254 STATUS notes: status difference
// only → use a `rewriteAcceptedTo204` wrapper (new variant of J253's
// `rewriteSuccessTo204` — same idea but matches 202 instead of 200).
//
// Type aliases re-export the v3 wire shapes verbatim so the v5
// namespace is importable as a stable surface; if the v5 body shape
// ever diverges (currently identical per openapi v5 SeasonPassResource
// schema), the alias can fork without breaking importers.
//
// Wave 3 v5 endpoint family port — 99 LOC C# source. J255.
package seasonpass

import (
	"bytes"
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is the v5 SeasonPassResource wire shape. Aliased from v3
// because the openapi v5 schema is identical (Series + MonitoringOptions).
type Resource = apiv3.SeasonPassResource

// SeriesResource is one row in the body's `series[]` array. Aliased
// from v3.
//
// Note: the C# v5 type is named `SeasonPassSeriesResource`; the Go
// alias re-uses the v3 name `SeasonPassSeriesResource` since the
// `seasonpass` package qualifier already disambiguates.
type SeriesResource = apiv3.SeasonPassSeriesResource

// SeasonOnWrite is the write-side projection of a single season's
// monitored flag inside the body. Aliased from v3.
type SeasonOnWrite = apiv3.SeasonPassSeasonOnWrite

// MonitoringOptions is the embedded `monitoringOptions` body field
// (cascade behaviour driver). Aliased from v3.
type MonitoringOptions = apiv3.MonitoringOptions

// SeriesWriter / SeriesEmitter / EpisodeMonitorCascader are aliased
// from v3 so cmd/sonarr can wire the v5 route using the same
// dependency interfaces.
type SeriesWriter = apiv3.SeriesWriter
type SeriesEmitter = apiv3.SeriesEmitter
type EpisodeMonitorCascader = apiv3.EpisodeMonitorCascader

// PostHandler returns POST /api/v5/seasonpass.
//
// Mirrors `SeasonPassController.UpdateAll` at
// `src/Sonarr.Api.V5/SeasonPass/SeasonPassController.cs:18-58`:
// `TypedResults.NoContent()` → 204 with empty body. The v3 handler
// emits 202 Accepted with body `{}`; v5 demotes status to 204 and
// strips the body.
//
// Implementation: delegate to v3 (which preserves the full
// per-series load + Monitored-flag cascade + cascader integration +
// SignalR Updated broadcasts) and rewrite the successful
// 202-with-`{}`-body to 204-empty via the local `rewriteAcceptedTo204`
// middleware. Error paths (400 / 404 / 500) pass through unchanged.
//
// J255.
func PostHandler(svc SeriesWriter, emitter SeriesEmitter, cascader EpisodeMonitorCascader) http.Handler {
	return rewriteAcceptedTo204(apiv3.SeasonPassHandler(svc, emitter, cascader))
}

// ----- response-rewriting middleware -----

// bufferingWriter captures status + body + headers into memory. The
// wrapping handler then decides what to forward. Duplicated from
// internal/api/v5/importlist (J223) and internal/api/v5/metadata
// (J253); consolidation into a shared `v5util` package is queued —
// J255 is the THIRD duplication, one shy of the J243-pattern
// four-duplication trigger.
type bufferingWriter struct {
	header     http.Header
	body       bytes.Buffer
	status     int
	statusSet  bool
	wroteBytes bool
}

func newBufferingWriter() *bufferingWriter {
	return &bufferingWriter{header: http.Header{}, status: http.StatusOK}
}

func (b *bufferingWriter) Header() http.Header { return b.header }

func (b *bufferingWriter) WriteHeader(code int) {
	if b.statusSet {
		return
	}
	b.status = code
	b.statusSet = true
}

func (b *bufferingWriter) Write(p []byte) (int, error) {
	if !b.statusSet {
		b.status = http.StatusOK
		b.statusSet = true
	}
	b.wroteBytes = true
	return b.body.Write(p)
}

// rewriteAcceptedTo204 wraps a v3 handler whose happy path writes
// 202 Accepted + `{}` body; on that exact shape it emits 204 + empty
// body. Any other status (200 / 4xx / 5xx) or body shape passes
// through verbatim, preserving the v3 error-envelope contracts.
//
// Differs from J223's `rewriteSuccessTo204` only in the status-code
// match: 202 instead of 200. The other family (SeasonPass) is the
// only v3 endpoint that uses 202 for a non-Created success.
//
// The C# v5 `TypedResults.NoContent()` always returns 204 with no
// Content-Type header, so the wrapper strips Content-Type when
// downgrading.
//
// J255.
func rewriteAcceptedTo204(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := newBufferingWriter()
		h.ServeHTTP(rec, r)
		bodyBytes := rec.body.Bytes()
		if rec.status == http.StatusAccepted && bytes.Equal(bodyBytes, []byte(`{}`)) {
			copyHeadersExcept(w.Header(), rec.header, "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		copyHeaders(w.Header(), rec.header)
		w.WriteHeader(rec.status)
		_, _ = w.Write(bodyBytes)
	})
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func copyHeadersExcept(dst, src http.Header, except string) {
	for k, vs := range src {
		if http.CanonicalHeaderKey(k) == http.CanonicalHeaderKey(except) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
