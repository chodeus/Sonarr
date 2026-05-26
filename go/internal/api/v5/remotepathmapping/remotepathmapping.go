// Package remotepathmapping is the v5 wire surface for
// /api/v5/remotepathmapping.
//
// The v5 controller (src/Sonarr.Api.V5/RemotePathMappings/RemotePathMappingController.cs)
// inherits from `RestController<RemotePathMappingResource>` at 122 LOC
// of C# source. Body shape matches v3 line-for-line per openapi v5
// (id / host / remotePath / localPath).
//
// TWO status divergences vs v3 — first Wave 3 slice to use TWO
// v5util wrappers in a single family:
//
//	v5 endpoint                     vs v3
//	GET    /api/v5/remotepathmapping       200   same
//	GET    /api/v5/remotepathmapping/{id}  200   same
//	POST   /api/v5/remotepathmapping       201   same
//	PUT    /api/v5/remotepathmapping/{id}  **200** (v3 emits 202 + body) — TypedResults.Ok
//	DELETE /api/v5/remotepathmapping/{id}  **204** (v3 emits 200 + EMPTY body) — TypedResults.NoContent
//
// Unusual quirk: most provider-base PUTs return 202 in v5 too
// (mirroring v3); RemotePathMapping is one of the few v5 controllers
// that uses `TypedResults.Ok<...>` on PUT instead of TypedAccepted.
// Per the C# source `:84-91`:
//
//	[RestPutById]
//	public Results<Ok<RemotePathMappingResource>, NotFound> UpdateMapping([FromBody] RemotePathMappingResource resource)
//	{
//	    var mapping = resource.ToModel();
//	    return TypedResults.Ok(_remotePathMappingService.Update(mapping).ToResource());
//	}
//
// `Ok<>` not `Accepted<>`. That's the v5 wire contract here.
//
// Combo: PUT uses RewriteAcceptedToOK (J256 helper); DELETE uses
// RewriteSuccessTo204. List / ByID / Create are pure path-aliases.
//
// Wave 3 v5 endpoint family port — 122 LOC C# source. J260.
package remotepathmapping

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is an alias of the v3 wire shape (unchanged in v5).
type Resource = apiv3.RemotePathMappingResource

// Lister is an alias of the v3 read interface.
type Lister = apiv3.RemotePathMappingLister

// Writer is an alias of the v3 CRUD interface.
type Writer = apiv3.RemotePathMappingWriter

// ListHandler returns GET /api/v5/remotepathmapping. Path-alias to v3.
var ListHandler = apiv3.RemotePathMappingHandler

// ByIDHandler returns GET /api/v5/remotepathmapping/{id}. Path-alias to v3.
var ByIDHandler = apiv3.RemotePathMappingByIDHandler

// CreateHandler returns POST /api/v5/remotepathmapping. Path-alias to v3 (201).
var CreateHandler = apiv3.RemotePathMappingCreateHandler

// UpdateHandler returns PUT /api/v5/remotepathmapping/{id}.
//
// v3 emits 202 Accepted + body; v5 emits 200 OK + body
// (`TypedResults.Ok<RemotePathMappingResource>` per the C# source).
// Wraps v3 via `v5util.RewriteAcceptedToOK` which demotes 202→200
// while preserving the body byte-for-byte.
//
// J260.
func UpdateHandler(svc Writer) http.Handler {
	return v5util.RewriteAcceptedToOK(apiv3.RemotePathMappingUpdateHandler(svc))
}

// DeleteHandler returns DELETE /api/v5/remotepathmapping/{id}.
//
// v3 emits 200 + EMPTY body (J232 idempotency posture); v5 emits 204
// NoContent + empty body (`TypedResults.NoContent`). Wraps v3 via
// `v5util.RewriteSuccessTo204` (whose predicate matches both `{}`
// AND empty bodies per J256).
//
// J260.
func DeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.RemotePathMappingDeleteHandler(svc))
}
