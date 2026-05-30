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
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
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
func DeleteHandler(svc Writer, emitter Emitter, onDeleted ...apiv3.ImportListDeletedHook) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.ImportListDeleteHandler(svc, emitter, onDeleted...))
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
	return v5util.RewriteSuccessTo204(apiv3.ImportListTestHandler(tester))
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
	return v5util.RewriteAcceptedToOK(apiv3.ImportListBulkUpdateHandler(svc, emitter))
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
func BulkDeleteHandler(svc Writer, emitter Emitter, onDeleted ...apiv3.ImportListDeletedHook) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.ImportListBulkDeleteHandler(svc, emitter, onDeleted...))
}

// Response-rewriting middleware moved to internal/api/v5/internal/v5util
// at J256 (consolidation). Handlers above use v5util.RewriteSuccessTo204
// (DELETE/test/bulk-delete) and v5util.RewriteAcceptedToOK (bulk-update).
