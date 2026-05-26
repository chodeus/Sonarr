// Package importlist is the v5 wire surface for /api/v5/importlist.
//
// The v5 controller (src/Sonarr.Api.V5/ImportLists/ImportListController.cs)
// inherits from `ProviderControllerBase<TResource, TBulkResource, ...>`
// which mirrors the v3 base verbatim on request/response shapes but
// tightens response statuses via .NET 8 `TypedResults`:
//
//	v3 endpoint           v3 status              v5 status
//	DELETE /{id}          200 + `{}` body        204 No Content
//	PUT    /bulk          202 Accepted + body    200 OK + body (status only)
//	DELETE /bulk          200 + `{}` body        204 No Content
//	POST   /test          200 + `{}` body        204 No Content
//
// All other endpoints (GET list, GET by-id, GET schema, POST /, PUT /{id},
// POST /testall, POST /action/{name}) produce identical statuses + bodies
// between v3 and v5 and are re-exported.
//
// v5 also renames query params on POST/PUT — v3 takes `?forceSave=` while
// v5 takes `?skipTesting=&skipValidation=`. The Go-port v3 handlers do not
// implement `forceSave` (the body always wins; the test step is unwired
// in CRUD), so v5 inherits the same posture — neither v3 nor v5 honors
// those query params today, which is a consistent divergence from C# that
// can be ported as a follow-up slice when the importlist Test seam is
// fully wired into the CRUD flow.
//
// Audit row C.1.c (upstream v5 catch-up — closed 5.J223).
package importlist

import (
	"bytes"
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 wire shape. v5's ImportListResource
// adds no fields over v3 (the JSON shape is identical; the C# v5 class
// uses `string?` annotations but the on-wire effect matches v3).
type Resource = apiv3.ImportListResource

// BulkResource is an alias of the v3 bulk-body shape (unchanged in v5).
type BulkResource = apiv3.ImportListBulkResource

// Lister is the read-side interface for GET handlers. Aliased from v3
// so a future v5-specific service can satisfy this without referencing
// apiv3 directly.
type Lister = apiv3.ImportListLister

// ByIDGetter is the read-by-id interface for GET /{id}. Aliased from v3.
type ByIDGetter = apiv3.ImportListByIDGetter

// Writer is the read/write interface for CRUD + bulk handlers. Aliased
// from v3.
type Writer = apiv3.ImportListWriter

// Emitter is the SignalR-broadcast interface for write paths. Aliased
// from v3.
type Emitter = apiv3.ImportListEmitter

// Tester is the test-provider interface for POST /test + POST /testall.
// Aliased from v3.
type Tester = apiv3.ImportListTester

// Actioner is the request-action interface for POST /action/{name}.
// Aliased from v3.
type Actioner = apiv3.ImportListActioner

// ListHandler returns GET /api/v5/importlist. Same wire shape as v3.
var ListHandler = apiv3.ImportListHandler

// ByIDHandler returns GET /api/v5/importlist/{id}. Same wire shape as v3.
var ByIDHandler = apiv3.ImportListByIDHandler

// SchemaHandler returns GET /api/v5/importlist/schema. Same wire shape
// as v3.
var SchemaHandler = apiv3.ImportListSchemaHandler

// CreateHandler returns POST /api/v5/importlist. Same wire shape as v3
// (201 Created + resource body).
var CreateHandler = apiv3.ImportListCreateHandler

// UpdateHandler returns PUT /api/v5/importlist/{id}. Same wire shape as
// v3 (202 Accepted + resource body).
var UpdateHandler = apiv3.ImportListUpdateHandler

// TestAllHandler returns POST /api/v5/importlist/testall. Same wire
// shape as v3 (200 OK or 400 BadRequest, both with body).
var TestAllHandler = apiv3.ImportListTestAllHandler

// RequestActionHandler returns POST /api/v5/importlist/action/{name}.
// Same wire shape as v3.
var RequestActionHandler = apiv3.ImportListRequestActionHandler

// DeleteHandler returns DELETE /api/v5/importlist/{id}.
//
// Mirrors `ProviderControllerBase.DeleteProvider` at
// `src/Sonarr.Api.V5/Provider/ProviderControllerBase.cs:179-185`:
// `TypedResults.NoContent()` → 204 No Content with empty body. The v3
// handler emits 200 + `{}`; v5 strips the placeholder JSON in favour
// of an empty 204.
//
// Implementation: delegate to the v3 handler (which preserves the
// SignalR Deleted broadcast + error-path semantics) and rewrite the
// successful-200-with-`{}`-body to 204-empty.
func DeleteHandler(svc Writer, emitter Emitter) http.Handler {
	return rewriteSuccessTo204(apiv3.ImportListDeleteHandler(svc, emitter))
}

// TestHandler returns POST /api/v5/importlist/test.
//
// Mirrors `ProviderControllerBase.Test` at
// `src/Sonarr.Api.V5/Provider/ProviderControllerBase.cs:219-230`:
// `TypedResults.NoContent()` → 204 No Content with empty body on
// success. The v3 handler emits 200 + `{}` on success; v5 strips the
// placeholder JSON. Validation-failure path (4xx body) passes through
// unchanged.
func TestHandler(tester Tester) http.Handler {
	return rewriteSuccessTo204(apiv3.ImportListTestHandler(tester))
}

// BulkUpdateHandler returns PUT /api/v5/importlist/bulk.
//
// Mirrors `ProviderControllerBase.UpdateProvider(TBulkProviderResource)`
// at `src/Sonarr.Api.V5/Provider/ProviderControllerBase.cs:126-165`:
// `TypedResults.Ok(...)` → 200 OK + the updated resource array. The v3
// handler emits 202 Accepted with the same body; v5 demotes to 200 OK.
//
// Implementation: delegate to v3 and rewrite the 202 status to 200,
// preserving the body verbatim. Error statuses pass through unchanged.
func BulkUpdateHandler(svc Writer, emitter Emitter) http.Handler {
	return rewrite202To200(apiv3.ImportListBulkUpdateHandler(svc, emitter))
}

// BulkDeleteHandler returns DELETE /api/v5/importlist/bulk.
//
// Mirrors `ProviderControllerBase.DeleteProviders` at
// `src/Sonarr.Api.V5/Provider/ProviderControllerBase.cs:187-194`:
// `TypedResults.NoContent()` → 204 No Content with empty body. The v3
// handler emits 200 + `{}`; v5 strips it.
//
// Implementation: delegate to v3 and rewrite the successful-200-with-
// `{}`-body to 204-empty (same pattern as DeleteHandler / TestHandler).
func BulkDeleteHandler(svc Writer, emitter Emitter) http.Handler {
	return rewriteSuccessTo204(apiv3.ImportListBulkDeleteHandler(svc, emitter))
}

// ----- response-rewriting middleware -----

// bufferingWriter is a minimal http.ResponseWriter that captures the
// status code + body + headers into memory. The wrapping handler then
// decides what to forward to the real ResponseWriter.
//
// Mirrors httptest.ResponseRecorder's semantics but stays in the
// production import graph (httptest is fine to use here, but a local
// type keeps the dependency footprint tight).
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

// Header implements http.ResponseWriter.
func (b *bufferingWriter) Header() http.Header { return b.header }

// WriteHeader implements http.ResponseWriter. First call wins to match
// net/http's real semantics.
func (b *bufferingWriter) WriteHeader(code int) {
	if b.statusSet {
		return
	}
	b.status = code
	b.statusSet = true
}

// Write implements http.ResponseWriter. If WriteHeader was not yet
// called, defaults the status to 200 OK (mirrors net/http behaviour).
func (b *bufferingWriter) Write(p []byte) (int, error) {
	if !b.statusSet {
		b.status = http.StatusOK
		b.statusSet = true
	}
	b.wroteBytes = true
	return b.body.Write(p)
}

// rewriteSuccessTo204 wraps a v3 handler whose happy path writes
// 200 + `{}` body; on that exact shape it emits 204 + empty body
// instead. Any other status (4xx / 5xx) or body shape passes through
// verbatim, preserving the v3 error-envelope contracts.
//
// The C# v5 `TypedResults.NoContent()` always returns 204 with no
// Content-Type header, so the wrapper strips Content-Type when
// downgrading to 204.
func rewriteSuccessTo204(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := newBufferingWriter()
		h.ServeHTTP(rec, r)
		bodyBytes := rec.body.Bytes()
		if rec.status == http.StatusOK && bytes.Equal(bodyBytes, []byte(`{}`)) {
			// Happy v3 path → v5 NoContent. Drop Content-Type since
			// the body is empty.
			copyHeadersExcept(w.Header(), rec.header, "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Pass-through.
		copyHeaders(w.Header(), rec.header)
		w.WriteHeader(rec.status)
		_, _ = w.Write(bodyBytes)
	})
}

// rewrite202To200 wraps a v3 handler whose happy path writes 202
// Accepted with a body; v5 demotes the status to 200 OK while
// preserving the body byte-for-byte. Any other status passes through
// verbatim.
func rewrite202To200(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := newBufferingWriter()
		h.ServeHTTP(rec, r)
		copyHeaders(w.Header(), rec.header)
		status := rec.status
		if status == http.StatusAccepted {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(rec.body.Bytes())
	})
}

func copyHeaders(dst, src http.Header) {
	for k, v := range src {
		for _, vv := range v {
			dst.Add(k, vv)
		}
	}
}

func copyHeadersExcept(dst, src http.Header, except string) {
	for k, v := range src {
		if http.CanonicalHeaderKey(k) == http.CanonicalHeaderKey(except) {
			continue
		}
		for _, vv := range v {
			dst.Add(k, vv)
		}
	}
}
