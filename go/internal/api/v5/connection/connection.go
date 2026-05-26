// Package connection is the v5 wire surface for /api/v5/connection.
//
// The v5 family (src/Sonarr.Api.V5/Connections/) is 154 LOC of C#
// across 3 files — ConnectionController (32),
// ConnectionResource (110), ConnectionBulkResource (12).
//
// **PATH RENAME on the v5 surface**: v3 exposes this family at
// `/api/v3/notification/...`; v5 renames the path to
// `/api/v5/connection/...`. The backing service is unchanged —
// `NotificationFactory` + `NotificationDefinition` — the rename is
// purely on the public wire surface. The Resource is also renamed
// (`NotificationResource` → `ConnectionResource`) but its fields are
// identical, so a Go type alias is wire-compatible.
//
// ConnectionController inherits ProviderControllerBase, BUT v5
// explicitly disables the two bulk endpoints with `[NonAction]` +
// `throw new NotImplementedException()` (per `:23-31`). That means
// v5 has 9 routes (NOT 11 — no /bulk variants):
//
//	v5 endpoint                          v5 status   vs v3 Go-port (notification.go)
//	GET    /api/v5/connection               200       same as /api/v3/notification list
//	POST   /api/v5/connection               201       same as v3 (Go-port already emits 201)
//	GET    /api/v5/connection/{id}          200       same as v3
//	PUT    /api/v5/connection/{id}          202       same as v3 (Go-port already emits 202)
//	**DELETE /api/v5/connection/{id}**      **204**   v3 emits 200 + `{}`
//	GET    /api/v5/connection/schema        200       same as v3
//	**POST  /api/v5/connection/test**       **204**   v3 emits 200 + `{}`
//	POST   /api/v5/connection/testall       200       same as v3
//	POST   /api/v5/connection/action/{name} 200       same as v3
//
// NO /api/v5/connection/bulk — explicitly [NonAction] in C# v5.
// v3 Go-port wires `NotificationBulkNotSupportedHandler` (returns
// 501) at `/api/v3/notification/bulk`; that 501 carries the
// "diagnostic over 404" rationale and stays exclusively on the v3
// surface. v5 simply doesn't expose the route, matching the C# v5
// surface where MVC's "no such action" yields a default 404.
//
// Resource shape: IDENTICAL field-for-field to v3 NotificationResource
// — 30 fields (`link`, 14 `on*` flags, 14 `supportsOn*` flags,
// `testCommand`). v3 + v5 Resource files diff only by namespace,
// `string?` vs `string` nullability annotations, and ConnectionBulkResource
// being empty (no nullable filter fields, vs the indexer/importlist
// families). On the JSON wire — same shape.
//
// TWO status divergences only — same family as J265 (Indexers, but
// without the PUT/bulk inversion since v5 connection has no bulk).
//
// Wave 3 v5 endpoint family port — 154 LOC C# source. J267.
package connection

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is an alias of the v3 NotificationResource (renamed to
// ConnectionResource on the v5 wire surface but field-identical).
type Resource = apiv3.NotificationResource

// Lister is an alias of the v3 read interface.
type Lister = apiv3.NotificationLister

// ByIDGetter is an alias of the v3 by-id read interface.
type ByIDGetter = apiv3.NotificationByIDGetter

// Writer is an alias of the v3 CUD interface.
type Writer = apiv3.NotificationWriter

// Emitter is an alias of the v3 SignalR-fan-out subset.
type Emitter = apiv3.NotificationEmitter

// RegistryChecker is an alias of the v3 provider-registry interface
// (validates Implementation/ConfigContract on Create/Update).
type RegistryChecker = apiv3.NotificationRegistryChecker

// Tester is an alias of the v3 per-row provider Test interface.
type Tester = apiv3.NotificationTester

// Actioner is an alias of the v3 per-row provider RequestAction
// interface.
type Actioner = apiv3.NotificationActioner

// ----- pure-status-match handlers (re-exported verbatim) -----

// ListHandler returns GET /api/v5/connection (200 + []Resource).
// Same as v3 (renamed path only).
var ListHandler = apiv3.NotificationHandler

// ByIDHandler returns GET /api/v5/connection/{id} (200 + Resource
// or 404).
var ByIDHandler = apiv3.NotificationByIDHandler

// SchemaHandler returns GET /api/v5/connection/schema (200 +
// []Resource of schemas).
var SchemaHandler = apiv3.NotificationSchemaHandler

// CreateHandler returns POST /api/v5/connection (201 + Resource).
// The v3 Go-port handler already emits 201 (per `notification.go:181`),
// matching the v5 openapi `Created<>` declaration.
var CreateHandler = apiv3.NotificationCreateHandler

// UpdateHandler returns PUT /api/v5/connection/{id} (202 + Resource).
// The v3 Go-port handler already emits 202 (per `:254`), matching
// the v5 openapi `Accepted<>` declaration.
var UpdateHandler = apiv3.NotificationUpdateHandler

// TestAllHandler returns POST /api/v5/connection/testall (200 +
// array). Same as v3 — mixed-results path still returns 400
// (validation failure aggregate) per v3 semantics.
var TestAllHandler = apiv3.NotificationTestAllHandler

// RequestActionHandler returns POST /api/v5/connection/action/{name}
// (200 + variable body). Same as v3.
var RequestActionHandler = apiv3.NotificationRequestActionHandler

// ----- status-rewriting wrappers -----

// DeleteHandler returns DELETE /api/v5/connection/{id}. v3 emits
// 200 + `{}` (also on idempotent missing-id per J232); v5 emits 204
// + empty (TypedResults.NoContent). Wraps v3 via
// v5util.RewriteSuccessTo204.
//
// J267.
func DeleteHandler(svc Writer, emitter Emitter) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.NotificationDeleteHandler(svc, emitter))
}

// TestHandler returns POST /api/v5/connection/test. v3 emits
// 200 + `{}` on success; v5 emits 204 + empty
// (TypedResults.NoContent). Wraps v3 via v5util.RewriteSuccessTo204.
// Validation-failure cases (400) pass through unchanged because the
// rewriter only matches successful 200 responses.
//
// J267.
func TestHandler(tester Tester) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.NotificationTestHandler(tester))
}
