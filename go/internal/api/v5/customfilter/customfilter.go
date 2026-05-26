// Package customfilter is the v5 wire surface for /api/v5/customfilter.
//
// The v5 controller (src/Sonarr.Api.V5/CustomFilters/CustomFilterController.cs)
// inherits from `RestController<CustomFilterResource>` at 100 LOC of
// C# source. The body shape matches v3 line-for-line (id / type / label
// / filters per the openapi v5 CustomFilterResource schema). v5 uses
// `List<ExpandoObject>` for `filters` where v3 uses
// `List<JsonElement>` — both serialise as a generic JSON-object array
// and round-trip identically. The Go-port carries `[]json.RawMessage`
// in both v3 and v5, which serialises to the same shape.
//
// Status-code divergence (the ONLY thing that differs):
//
//	v5 endpoint                  vs v3
//	GET    /api/v5/customfilter        200   same
//	GET    /api/v5/customfilter/{id}   200   same
//	POST   /api/v5/customfilter        201   same (Created via TypedCreated)
//	PUT    /api/v5/customfilter/{id}   202   same (v3 already emits 202 Accepted)
//	DELETE /api/v5/customfilter/{id}   204   **DIFFERENT** (v3 emits 200 + EMPTY body)
//
// Only DELETE needs a wrapper. Per J256's v5util consolidation, the
// `RewriteSuccessTo204` helper now matches 200+EMPTY (not just
// 200+`{}`) — CustomFilter DELETE is the motivating case for that
// expanded predicate.
//
// Wave 3 v5 endpoint family port — 100 LOC C# source. J257.
package customfilter

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is an alias of the v3 wire shape (unchanged in v5).
type Resource = apiv3.CustomFilterResource

// Lister is an alias of the v3 read interface. *customfilters.Service
// satisfies it via Go structural typing.
type Lister = apiv3.CustomFilterLister

// Writer is an alias of the v3 CRUD interface.
type Writer = apiv3.CustomFilterWriter

// ListHandler returns GET /api/v5/customfilter. Same wire shape as v3.
var ListHandler = apiv3.CustomFilterHandler

// ByIDHandler returns GET /api/v5/customfilter/{id}. Same wire shape as v3.
var ByIDHandler = apiv3.CustomFilterByIDHandler

// CreateHandler returns POST /api/v5/customfilter. Same wire shape as v3 (201).
var CreateHandler = apiv3.CustomFilterCreateHandler

// UpdateHandler returns PUT /api/v5/customfilter/{id}. Same wire shape
// as v3 — v3 already emits 202 Accepted matching v5's TypedAccepted.
var UpdateHandler = apiv3.CustomFilterUpdateHandler

// DeleteHandler returns DELETE /api/v5/customfilter/{id}.
//
// Mirrors `CustomFilterController.DeleteCustomResource` at
// `src/Sonarr.Api.V5/CustomFilters/CustomFilterController.cs:49-53`:
// `TypedResults.NoContent()` → 204 No Content with empty body. The v3
// handler emits 200 with EMPTY body (J232 idempotency posture — DELETE
// signals "deleted or already gone"); v5 demotes to 204 + empty.
//
// Uses `v5util.RewriteSuccessTo204` whose predicate matches BOTH `{}`
// AND empty bodies — CustomFilter is the motivating case for the
// expanded predicate (J256). Error paths (400 / 500) pass through
// unchanged.
//
// J257.
func DeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.CustomFilterDeleteHandler(svc))
}
