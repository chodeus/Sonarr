// Package indexer is the v5 wire surface for /api/v5/indexer and
// /api/v5/indexerflag.
//
// The v5 family (src/Sonarr.Api.V5/Indexers/) is 139 LOC of C# total
// across 5 files — IndexerController (25), IndexerResource (51),
// IndexerBulkResource (30), IndexerFlagController (21),
// IndexerFlagResource (12).
//
// IndexerController inherits ProviderControllerBase, which expands
// to 11 routes:
//
//	v5 endpoint                          v5 status   vs v3
//	GET    /api/v5/indexer                  200       same
//	POST   /api/v5/indexer                  201       same (v3 Go-port already emits 201)
//	GET    /api/v5/indexer/{id}             200       same
//	PUT    /api/v5/indexer/{id}             202       same (v3 Go-port already emits 202)
//	DELETE /api/v5/indexer/{id}             **204**   v3 emits 200 + `{}`
//	GET    /api/v5/indexer/schema           200       same
//	PUT    /api/v5/indexer/bulk             **200**   v3 Go-port emits 202
//	DELETE /api/v5/indexer/bulk             **204**   v3 emits 200 + `{}`
//	POST   /api/v5/indexer/test             **204**   v3 emits 200 + `{}`
//	POST   /api/v5/indexer/testall          200       same
//	POST   /api/v5/indexer/action/{name}    200       same
//	GET    /api/v5/indexerflag              200       same
//
// Resource shape: IDENTICAL field-for-field to v3 IndexerResource
// (`enableRss`, `enableAutomaticSearch`, `enableInteractiveSearch`,
// `supportsRss`, `supportsSearch`, `protocol`, `priority`,
// `seasonSearchMaximumSingleEpisodeAge`, `downloadClientId` —
// 9 fields, same types — per `src/Sonarr.Api.V5/Indexers/IndexerResource.cs`
// vs `src/Sonarr.Api.V3/Indexers/IndexerResource.cs`). Same for
// IndexerBulkResource (5 nullable fields) and IndexerFlagResource
// (id / name + computed nameLower).
//
// FOUR status divergences only — no field-level divergence. Same
// pattern family as J260 (RemotePathMappings: PUT 202→200 + DELETE
// 200→204) and J261 (Blocklist: two DELETEs 200+`{}`→204).
//
// Wave 3 v5 endpoint family port — 139 LOC C# source. J265.
package indexer

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is an alias of the v3 wire shape (unchanged in v5 —
// confirmed field-for-field per package doc).
type Resource = apiv3.IndexerResource

// BulkResource is an alias of the v3 bulk-update wire shape
// (unchanged in v5).
type BulkResource = apiv3.IndexerBulkResource

// FlagResource is an alias of the v3 flag wire shape (unchanged in
// v5).
type FlagResource = apiv3.IndexerFlagResource

// Lister is an alias of the v3 read interface.
type Lister = apiv3.IndexerLister

// ByIDGetter is an alias of the v3 by-id read interface.
type ByIDGetter = apiv3.IndexerByIDGetter

// Writer is an alias of the v3 CUD interface.
type Writer = apiv3.IndexerWriter

// Emitter is an alias of the v3 SignalR-fan-out subset.
type Emitter = apiv3.IndexerEmitter

// Tester is an alias of the v3 per-row provider Test interface.
type Tester = apiv3.IndexerTester

// Actioner is an alias of the v3 per-row provider RequestAction
// interface.
type Actioner = apiv3.IndexerActioner

// ----- pure-status-match handlers (re-exported verbatim) -----

// ListHandler returns GET /api/v5/indexer (200 + []Resource). Same
// as v3.
var ListHandler = apiv3.IndexerHandler

// ByIDHandler returns GET /api/v5/indexer/{id} (200 + Resource or
// 404). Same as v3.
var ByIDHandler = apiv3.IndexerByIDHandler

// SchemaHandler returns GET /api/v5/indexer/schema (200 +
// []Resource of schemas). Same as v3.
var SchemaHandler = apiv3.IndexerSchemaHandler

// CreateHandler returns POST /api/v5/indexer (201 + Resource). The
// v3 Go-port handler ALREADY emits 201 (per `internal/api/v3/indexer.go:164`),
// matching the v5 openapi `Created<>` declaration.
var CreateHandler = apiv3.IndexerCreateHandler

// UpdateHandler returns PUT /api/v5/indexer/{id} (202 + Resource).
// The v3 Go-port handler ALREADY emits 202 (per `:222`), matching
// the v5 openapi `Accepted<>` declaration.
var UpdateHandler = apiv3.IndexerUpdateHandler

// TestAllHandler returns POST /api/v5/indexer/testall (200 + array).
// Same as v3 — mixed-results path still returns 400 (validation
// failure aggregate) per v3 semantics.
var TestAllHandler = apiv3.IndexerTestAllHandler

// RequestActionHandler returns POST /api/v5/indexer/action/{name}
// (200 + variable body). Same as v3.
var RequestActionHandler = apiv3.IndexerRequestActionHandler

// FlagsHandler returns GET /api/v5/indexerflag (200 + 9-flag list).
// Same as v3.
var FlagsHandler = apiv3.IndexerFlagHandler

// ----- status-rewriting wrappers -----

// DeleteHandler returns DELETE /api/v5/indexer/{id}. v3 emits
// 200 + `{}` (also on idempotent missing-id); v5 emits 204 + empty
// (TypedResults.NoContent). Wraps v3 via v5util.RewriteSuccessTo204.
//
// J265.
func DeleteHandler(svc Writer, emitter Emitter) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.IndexerDeleteHandler(svc, emitter))
}

// BulkDeleteHandler returns DELETE /api/v5/indexer/bulk. Same
// 200 + `{}` → 204 + empty rewrite as DeleteHandler.
//
// J265.
func BulkDeleteHandler(svc Writer, emitter Emitter) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.IndexerBulkDeleteHandler(svc, emitter))
}

// TestHandler returns POST /api/v5/indexer/test. v3 emits
// 200 + `{}` on success; v5 emits 204 + empty (TypedResults.NoContent).
// Wraps v3 via v5util.RewriteSuccessTo204. Validation-failure cases
// (400) pass through unchanged because the rewriter only matches
// successful 200 responses.
//
// J265.
func TestHandler(tester Tester) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.IndexerTestHandler(tester))
}

// BulkUpdateHandler returns PUT /api/v5/indexer/bulk. v3 Go-port
// emits 202 + body; v5 emits 200 + body (TypedResults.Ok). Wraps
// v3 via v5util.RewriteAcceptedToOK.
//
// J265.
func BulkUpdateHandler(svc Writer, emitter Emitter) http.Handler {
	return v5util.RewriteAcceptedToOK(apiv3.IndexerBulkUpdateHandler(svc, emitter))
}
