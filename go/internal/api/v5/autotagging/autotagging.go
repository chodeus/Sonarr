// Package autotagging is the v5 wire surface for /api/v5/autotagging.
//
// The v5 controller (src/Sonarr.Api.V5/AutoTagging/AutoTaggingController.cs)
// mirrors v3 line-for-line on the request/response shapes
// (`AutoTaggingResource` has the same 5 fields: id, name,
// removeTagsAutomatically, tags, specifications) but tightens ONE
// response status via .NET 8 `TypedResults`:
//
//	v3 endpoint           v3 status        v5 status
//	DELETE /{id}          200 (empty)      204 NoContent
//
// All other endpoints (GET list, GET by-id, GET schema, POST, PUT /{id})
// produce identical statuses + bodies between v3 and v5 and are
// re-exported. AutoTagging has NO bulk endpoints in either version
// (unlike CustomFormat / ImportList), so the new-handler surface for
// v5 is the minimum possible: one handler.
//
// Audit row C.1.e (upstream v5 catch-up — closed 5.J225) — LAST
// new-endpoint slice in the C.1 family.
package autotagging

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/httperr"
)

// Resource is an alias of the v3 wire shape (unchanged in v5).
type Resource = apiv3.AutoTaggingResource

// SpecificationSchema is an alias of the v3 specification-schema shape
// (unchanged in v5).
type SpecificationSchema = apiv3.AutoTaggingSpecificationSchema

// Lister is the read-side interface for GET handlers. Aliased from v3.
type Lister = apiv3.AutoTaggingLister

// Writer is the read/write interface for CRUD handlers. Aliased from v3.
type Writer = apiv3.AutoTaggingWriter

// ListHandler returns GET /api/v5/autotagging. Same wire shape as v3.
var ListHandler = apiv3.AutoTaggingHandler

// ByIDHandler returns GET /api/v5/autotagging/{id}. Same wire shape as v3.
var ByIDHandler = apiv3.AutoTaggingByIDHandler

// SchemaHandler returns GET /api/v5/autotagging/schema. Same wire shape
// as v3.
var SchemaHandler = apiv3.AutoTaggingSchemaHandler

// CreateHandler returns POST /api/v5/autotagging. Same wire shape as v3
// (201 Created + resource body).
var CreateHandler = apiv3.AutoTaggingCreateHandler

// UpdateHandler returns PUT /api/v5/autotagging/{id}. Same wire shape as
// v3 (202 Accepted + resource body).
var UpdateHandler = apiv3.AutoTaggingUpdateHandler

// DeleteHandler returns DELETE /api/v5/autotagging/{id}.
//
// Mirrors `AutoTaggingController.DeleteAutoTagging` at
// `src/Sonarr.Api.V5/AutoTagging/AutoTaggingController.cs:82-88`:
// `TypedResults.NoContent()` → 204 No Content with empty body. The v3
// handler emits 200 with an empty body; v5 promotes to 204 to match
// the typed-results convention.
func DeleteHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		err := svc.Delete(r.Context(), id)
		if errors.Is(err, autotagging.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("autotagging %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("autotagging: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// parseID mirrors the v3 autotagging parseID helper. Duplicated here
// (rather than calling a v3 helper) so the v5 package can stand alone
// for routing-table purposes.
func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "autotagging: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("autotagging: invalid id %q", raw))
		return 0, false
	}
	return id, true
}
