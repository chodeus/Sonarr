// Package metadata is the v5 wire surface for /api/v5/metadata.
//
// The v5 controller (src/Sonarr.Api.V5/Metadata/MetadataController.cs)
// inherits from ProviderControllerBase (74 LOC of family-specific
// code + the inherited 270-LOC base) and overrides two methods with
// `[NonAction]` to REMOVE them from the v5 surface entirely:
//
//	UpdateProvider(TBulkProviderResource)   ← bulk PUT (removed)
//	DeleteProviders(TBulkProviderResource)  ← bulk DELETE (removed)
//
// So the v5 metadata endpoint set is the base-class set MINUS those
// two — leaves 9 endpoints:
//
//	v5 endpoint                       Status   vs v3
//	GET    /api/v5/metadata           200      same (Ok via TypedResults)
//	GET    /api/v5/metadata/{id}      200      same
//	GET    /api/v5/metadata/schema    200      same
//	POST   /api/v5/metadata           201      same (Created)
//	PUT    /api/v5/metadata/{id}      202      same (v3 already emits 202 — TypedAccepted)
//	DELETE /api/v5/metadata/{id}      204      **DIFFERENT** (v3 emits 200 `{}`; v5 NoContent)
//	POST   /api/v5/metadata/test      204      **DIFFERENT** (v3 emits 200 `{}`; v5 NoContent)
//	POST   /api/v5/metadata/testall   200/400  same
//	POST   /api/v5/metadata/action/{name}  200/400  same
//
// PUT /bulk and DELETE /bulk are NOT routed in v5 (the [NonAction]
// overrides at MetadataController.cs:22-32).
//
// Most handlers are variable-aliased to the v3 implementation; the
// two status-code-tightened endpoints (DELETE/{id} + POST /test) use
// `rewriteSuccessTo204` to demote v3's 200+`{}` to v5's 204+empty.
// Pattern matches J223's ImportList port; helpers duplicated here per
// the J225 precedent (`autotagging` package keeps its own helpers too)
// — consolidation into a shared `v5util` package is a queued follow-up.
//
// Wave 3 v5 endpoint family port — 74 LOC of C# source. J253.
package metadata

import (
	"bytes"
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 wire shape (unchanged in v5).
type Resource = apiv3.MetadataResource

// BulkResource is an alias of the v3 wire shape (unchanged in v5).
// Note: v5 doesn't route the bulk endpoints (NonAction overrides), but
// the resource type is kept exported for completeness + any future v5
// re-exposure (would require unblocking the C# [NonAction]s upstream).
type BulkResource = apiv3.MetadataBulkResource

// Writer is the read/write interface for CRUD handlers. Aliased from v3.
type Writer = apiv3.MetadataWriter

// Emitter is the SignalR-broadcast interface aliased from v3.
type Emitter = apiv3.MetadataEmitter

// Tester is the per-row provider-test interface aliased from v3.
type Tester = apiv3.MetadataTester

// Actioner is the per-row provider RequestAction interface aliased from v3.
type Actioner = apiv3.MetadataActioner

// RowsLister narrows metadata.Repo to the All() method used by
// list + testall handlers. Aliased from v3.
type RowsLister = apiv3.MetadataRowsLister

// ListHandler returns GET /api/v5/metadata. Same wire shape as v3.
var ListHandler = apiv3.MetadataListHandler

// ByIDHandler returns GET /api/v5/metadata/{id}. Same wire shape as v3.
var ByIDHandler = apiv3.MetadataByIDHandler

// SchemaHandler returns GET /api/v5/metadata/schema. Same wire shape as v3.
var SchemaHandler = apiv3.MetadataSchemaHandler

// CreateHandler returns POST /api/v5/metadata. Same wire shape (201).
var CreateHandler = apiv3.MetadataCreateHandler

// UpdateHandler returns PUT /api/v5/metadata/{id}. v3 already emits
// 202 Accepted (matching v5 TypedAccepted) so no wrapper needed.
var UpdateHandler = apiv3.MetadataUpdateHandler

// TestAllHandler returns POST /api/v5/metadata/testall. Same wire
// shape as v3 (200 with per-row results, or 400 with same body when
// any row failed validation).
var TestAllHandler = apiv3.MetadataTestAllHandler

// RequestActionHandler returns POST /api/v5/metadata/action/{name}.
// Same wire shape as v3.
var RequestActionHandler = apiv3.MetadataRequestActionHandler

// DeleteHandler returns DELETE /api/v5/metadata/{id}.
//
// Mirrors `ProviderControllerBase.DeleteProvider` at
// `src/Sonarr.Api.V5/Provider/ProviderControllerBase.cs:179-185`:
// `TypedResults.NoContent()` → 204 No Content with empty body. The v3
// handler emits 200 with body `{}`; v5 strips the placeholder JSON.
// 404 / 500 error paths pass through unchanged from v3.
func DeleteHandler(svc Writer, emitter Emitter) http.Handler {
	return rewriteSuccessTo204(apiv3.MetadataDeleteHandler(svc, emitter))
}

// TestHandler returns POST /api/v5/metadata/test.
//
// Mirrors `ProviderControllerBase.Test` at
// `src/Sonarr.Api.V5/Provider/ProviderControllerBase.cs:219-230`:
// `TypedResults.NoContent()` → 204 with empty body on success. The
// v3 handler emits 200 with body `{}` on success; v5 strips it.
// Validation-failure path (4xx body) passes through unchanged.
func TestHandler(tester Tester) http.Handler {
	return rewriteSuccessTo204(apiv3.MetadataTestHandler(tester))
}

// ----- response-rewriting middleware -----

// bufferingWriter captures status + body + headers into memory. The
// wrapping handler then decides what to forward. Duplicated from
// internal/api/v5/importlist (J223); consolidation into a shared
// v5util package is queued as a follow-up sub-arc.
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

// rewriteSuccessTo204 wraps a v3 handler whose happy path writes
// 200 + `{}` body; on that exact shape it emits 204 + empty body.
// Any other status (4xx / 5xx) or body shape passes through verbatim,
// preserving the v3 error-envelope contracts.
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
