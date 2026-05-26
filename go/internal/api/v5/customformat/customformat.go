// Package customformat is the v5 wire surface for /api/v5/customformat.
//
// The v5 controller mirrors v3 line-for-line on the request/response
// shapes (`CustomFormatResource` is unchanged; `CustomFormatBulkResource`
// is unchanged) but tightens the response status codes via .NET 8
// `TypedResults`:
//
//	v3 endpoint            v3 status    v5 status
//	DELETE /{id}           200 (empty)  204 NoContent
//	PUT    /bulk           202 Accepted 200 OK
//	DELETE /bulk           200 "{}"     204 NoContent (empty body)
//
// All other endpoints (GET list, GET by-id, POST, PUT /{id}, GET schema)
// produce identical statuses + bodies and are re-exported from v3
// without modification.
//
// Audit row C.1.b (upstream v5 catch-up — closed 5.J222).
package customformat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/httperr"
)

// Resource is an alias of the v3 wire shape. The C# v5
// `CustomFormatResource` declares `Name` as `string?` (nullable) and v3
// declares it as non-nullable, but the Go-port already encodes Name as
// a plain `string` with `json:"name"` (no omitempty), and the only
// observable wire effect is whether `"name": null` ever appears — the
// Go-port's v3 handler always emits a string value, so there's no
// nullable-vs-required divergence to model.
type Resource = apiv3.CustomFormatResource

// BulkResource is an alias of the v3 bulk-body shape (unchanged in v5).
type BulkResource = apiv3.CustomFormatBulkResource

// Lister is the read interface the GET handlers depend on.
// `*customformats.Service` satisfies it via `All`.
type Lister = apiv3.CustomFormatLister

// Writer is the read/write interface the CRUD + bulk handlers depend on.
// `*customformats.Service` satisfies it.
type Writer = apiv3.CustomFormatWriter

// ListHandler returns GET /api/v5/customformat. Wire shape is identical
// to v3 — re-exported.
var ListHandler = apiv3.CustomFormatHandler

// ByIDHandler returns GET /api/v5/customformat/{id}. Wire shape is
// identical to v3 — re-exported.
var ByIDHandler = apiv3.CustomFormatByIDHandler

// CreateHandler returns POST /api/v5/customformat. Wire shape is
// identical to v3 (201 Created + resource body) — re-exported.
var CreateHandler = apiv3.CustomFormatCreateHandler

// UpdateHandler returns PUT /api/v5/customformat/{id}. Wire shape is
// identical to v3 (202 Accepted + resource body) — re-exported.
var UpdateHandler = apiv3.CustomFormatUpdateHandler

// SchemaHandler returns GET /api/v5/customformat/schema. Wire shape is
// identical to v3 — re-exported.
var SchemaHandler = apiv3.CustomFormatSchemaHandler

// DeleteHandler returns DELETE /api/v5/customformat/{id}.
//
// Mirrors `CustomFormatController.DeleteFormat` at
// `src/Sonarr.Api.V5/CustomFormats/CustomFormatController.cs:81-87`:
// `TypedResults.NoContent()` → 204 No Content with empty body. The v3
// handler returns 200 with an empty body; v5 promotes to 204 to match
// the typed-results convention.
func DeleteHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		err := svc.Delete(r.Context(), id)
		if errors.Is(err, customformats.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("customformat %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// BulkUpdateHandler returns PUT /api/v5/customformat/bulk.
//
// Mirrors `CustomFormatController.UpdateBulk` at
// `src/Sonarr.Api.V5/CustomFormats/CustomFormatController.cs:89-109`:
// `Ok<List<CustomFormatResource>>` → 200 OK + the updated resource
// array. The v3 handler returns 202 Accepted; v5 uses the more
// conventional 200 for a synchronous list response.
//
// Body shape + null-coalesce semantic + 400-on-empty-ids + 404-on-
// missing-id all match v3 (and the C# `:91-105` flow) verbatim.
func BulkUpdateHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body BulkResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("customformat: malformed bulk body: %v", err))
			return
		}
		if len(body.Ids) == 0 {
			httperr.BadRequest(w, "ids must be provided")
			return
		}
		out := make([]Resource, 0, len(body.Ids))
		for _, id := range body.Ids {
			f, err := svc.GetByID(r.Context(), id)
			if errors.Is(err, customformats.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("customformat %d not found", id))
				return
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
				return
			}
			if body.IncludeCustomFormatWhenRenaming != nil {
				f.IncludeCustomFormatWhenRenaming = *body.IncludeCustomFormatWhenRenaming
			}
			model, mErr := apiv3.CustomFormatModelFromFormat(f)
			if mErr != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: serialise %d: %w", id, mErr), nil)
				return
			}
			if uErr := svc.Update(r.Context(), &model); uErr != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: update %d: %w", id, uErr), nil)
				return
			}
			out = append(out, apiv3.ToCustomFormatResource(f))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// BulkDeleteHandler returns DELETE /api/v5/customformat/bulk.
//
// Mirrors `CustomFormatController.DeleteBulk` at
// `src/Sonarr.Api.V5/CustomFormats/CustomFormatController.cs:111-118`:
// `TypedResults.NoContent()` → 204 with empty body. The v3 handler
// returns 200 + `{}`; v5 strips the placeholder JSON in favour of an
// empty 204.
//
// Per-id delete posture preserved: first ErrNotFound short-circuits to
// 404 (matches C# `_formatService.Delete(ids.ToList())` which throws
// ModelNotFoundException on the first missing id).
func BulkDeleteHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body BulkResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("customformat: malformed bulk body: %v", err))
			return
		}
		for _, id := range body.Ids {
			err := svc.Delete(r.Context(), id)
			if errors.Is(err, customformats.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("customformat %d not found", id))
				return
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: delete %d: %w", id, err), nil)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// parseID mirrors the v3 customformat parseID helper. Duplicated here
// (rather than calling a v3 helper) so the v5 package can stand alone
// for routing-table purposes.
func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "customformat: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("customformat: invalid id %q", raw))
		return 0, false
	}
	return id, true
}
