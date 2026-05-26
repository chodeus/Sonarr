// Package qualitydefinition is the v5 wire surface for
// /api/v5/qualitydefinition.
//
// The v5 controller (src/Sonarr.Api.V5/Qualities/QualityDefinitionController.cs)
// is at 135 LOC of C# source. SIGNIFICANT divergence from v3 — three
// distinct wire-contract shifts in one family:
//
//  1. **Resource shape REDUCED.** v5 QualityDefinitionResource drops
//     `minSize` / `maxSize` / `preferredSize` (3 fields). v3 carries
//     all three (per `src/Sonarr.Api.V3/Qualities/QualityDefinitionResource.cs`).
//     The C# v5 source confirms: `Quality? / Title? / Weight` only —
//     no nullable size fields on the Resource.
//
//     The Go-port v3 Resource keeps the size fields (see
//     `internal/api/v3/qualitydefinition.go:37-45`) because v3 contract
//     requires them. v5 here matches the slimmer C# v5 shape.
//
//  2. **Route map changed.** v3 has `/qualitydefinition/update` for
//     the bulk UpdateMany; v5 puts UpdateMany at PUT `/qualitydefinition`
//     (the bare path). v3 has GET `/qualitydefinition/limits`; v5
//     removed that endpoint entirely.
//
//  3. **Status code differs on UpdateMany.** v3 emits 202 Accepted +
//     body for both single Update + UpdateMany. v5 emits 202 for
//     single Update (same) BUT 200 OK for UpdateMany (TypedResults.Ok
//     vs v3's TypedAccepted).
//
// Because the Resource shape itself differs (drops 3 fields), all 4
// handlers (List / ByID / Update / UpdateMany) are v5-specific —
// path-aliasing to v3 would emit the v3-with-size-fields shape on the
// wire. Same J254/J262 pattern.
//
// Side effect note: the v5 C# Update controller still calls
// `_qualityProfileService.UpdateAllSizeLimits(...)` when
// `model.MinSize.HasValue || model.MaxSize.HasValue || model.PreferredSize.HasValue`
// (per `:42-45`). But since the v5 Resource DOESN'T carry the size
// fields, deserialised v5 models always have those nullables as null,
// so the branch never fires. v5 size-limit propagation is therefore
// DEAD CODE in C# — the Go-port v5 handlers skip it.
//
// Wave 3 v5 endpoint family port — 135 LOC C# source. J263.
package qualitydefinition

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
)

// Resource is the v5 QualityDefinitionResource wire shape. Reduced
// vs v3 — only 4 fields (id / quality / title / weight). No size
// fields.
type Resource struct {
	ID      int             `json:"id"`
	Quality QualityResource `json:"quality"`
	Title   string          `json:"title"`
	Weight  int             `json:"weight"`
}

// QualityResource is the inline Quality DTO that Resource embeds.
// Same shape as v3's private `qualityResource` (id / name / source /
// resolution) but exported on the v5 surface for callers that want
// to build the type explicitly.
//
// Source is the camelCase enum name from quality.QualitySource via
// the existing `apiv3.QualitySourceName` helper.
type QualityResource struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	Resolution int    `json:"resolution"`
}

// Lister is an alias of the v3 read interface.
type Lister = apiv3.QualityDefinitionLister

// Writer is an alias of the v3 write interface.
type Writer = apiv3.QualityDefinitionWriter

// toResource projects a `qualitydefinitions.Decorated` row onto the
// v5 wire shape — same field set as v3 minus the 3 size fields.
//
// J263.
func toResource(d qualitydefinitions.Decorated) Resource {
	return Resource{
		ID: d.ID,
		Quality: QualityResource{
			ID:         d.Quality.ID,
			Name:       d.Quality.Name,
			Source:     apiv3.QualitySourceName(d.Quality.Source),
			Resolution: d.Quality.Resolution,
		},
		Title:  d.Title,
		Weight: d.Weight,
	}
}

// ListHandler returns GET /api/v5/qualitydefinition.
//
// Mirrors `QualityDefinitionController.GetAll` at
// `src/Sonarr.Api.V5/Qualities/QualityDefinitionController.cs:55-59`:
// `TypedResults.Ok(_qualityDefinitionService.All().ToResource())`.
// Service-side `.All()` already sorts by Weight ASC.
//
// J263.
func ListHandler(lister Lister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: %w", err), nil)
			return
		}
		out := make([]Resource, len(rows))
		for i, row := range rows {
			out[i] = toResource(row)
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// ByIDHandler returns GET /api/v5/qualitydefinition/{id}.
//
// Mirrors `GetResourceById` (inherited from RestController) at `:50-53`.
//
// J263.
func ByIDHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		d, err := svc.GetByID(r.Context(), id)
		if errors.Is(err, qualitydefinitions.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("qualitydefinition %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: %w", err), nil)
			return
		}
		writeJSON(w, http.StatusOK, toResource(*d))
	})
}

// UpdateHandler returns PUT /api/v5/qualitydefinition/{id}.
//
// Mirrors `Update` at `:36-48`: `TypedAccepted(model.Id)` → 202 +
// body. The C# v5 inspects model.MinSize/MaxSize/PreferredSize via
// HasValue to decide whether to call UpdateAllSizeLimits, but since
// the v5 Resource omits those fields, the deserialised model never
// has them set — that branch is dead code on the wire path. v5
// handler skips it.
//
// J263.
func UpdateHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		var body Resource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("qualitydefinition: malformed body: %v", err))
			return
		}
		if body.Title == "" {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "Title",
				ErrorMessage: "'Title' must not be empty.",
				Severity:     "error",
			}})
			return
		}
		def := &qualitydefinitions.Definition{
			ID:      id,
			Quality: body.Quality.ID,
			Title:   body.Title,
		}
		if err := svc.Update(r.Context(), def); err != nil {
			if errors.Is(err, qualitydefinitions.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("qualitydefinition %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: %w", err), nil)
			return
		}
		// Re-read so the response carries the canonical Decorated shape.
		d, err := svc.GetByID(r.Context(), id)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: reload %d: %w", id, err), nil)
			return
		}
		writeJSON(w, http.StatusAccepted, toResource(*d))
	})
}

// UpdateManyHandler returns PUT /api/v5/qualitydefinition.
//
// Mirrors `UpdateMany` at `:61-81`: `TypedResults.Ok(...)` → 200 OK
// + body (NOT v3's 202 Accepted). Body is `[]Resource`; each row
// updated in order; response is re-read full list via `lister.All`.
//
// Same dead-code note as UpdateHandler — v5 Resource omits size
// fields so the UpdateAllSizeLimits cascade never fires.
//
// J263.
func UpdateManyHandler(svc Writer, lister Lister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []Resource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("qualitydefinition: malformed body: %v", err))
			return
		}
		for _, res := range body {
			def := &qualitydefinitions.Definition{
				ID:      res.ID,
				Quality: res.Quality.ID,
				Title:   res.Title,
			}
			if err := svc.Update(r.Context(), def); err != nil {
				if errors.Is(err, qualitydefinitions.ErrNotFound) {
					httperr.NotFound(w, fmt.Sprintf("qualitydefinition %d not found", res.ID))
					return
				}
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: %w", err), nil)
				return
			}
		}
		// Re-read everything (matches C# `:79-80` Ok(All().ToResource())).
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: reload: %w", err), nil)
			return
		}
		out := make([]Resource, len(rows))
		for i, row := range rows {
			out[i] = toResource(row)
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// ----- private helpers -----

func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "qualitydefinition: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, "qualitydefinition: invalid id")
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(v)
	_, _ = w.Write(buf)
}
