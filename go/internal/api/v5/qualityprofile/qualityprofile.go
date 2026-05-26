// Package qualityprofile ports the v5 QualityProfileController +
// QualityProfileSchemaController families at /api/v5/qualityprofile.
//
// Source: src/Sonarr.Api.V5/Profiles/Quality/QualityProfileController.cs
// (88 LOC) + QualityProfileSchemaController.cs (28 LOC) +
// QualityProfileResource.cs (126 LOC).
//
// 6 endpoints (4 GET + 1 POST + 1 PUT + 1 DELETE). ONE wire-shape
// divergence from v3: DELETE /qualityprofile/{id} returns 204
// NoContent on v5 vs v3's 200+empty. Wrapped via
// v5util.RewriteSuccessTo204. Other 5 endpoints are pure
// path-aliases — v3 Go-port already emits the matching codes
// (POST→201, PUT→202, GET→200, schema→200).
//
// QualityProfileResource shape byte-identical between v3 and v5
// (8 top-level fields + 7-field nested QualityProfileItemResource +
// 3-field nested ProfileFormatItemResource). Type aliases are
// wire-safe.
//
// v3 DELETE strict-409 carry-through: v3 handler returns 409 on
// ErrQualityProfileInUse (qualityprofile.go:407-409). The wrapper's
// 200→204 predicate only matches 200; 409 passes through unchanged
// — both v3 and v5 surfaces emit 409 for in-use profile deletion.
package qualityprofile

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is the v5 QualityProfileResource — wire-identical to v3.
type Resource = apiv3.QualityProfileResource

// ItemResource is the per-entry view inside Resource.Items.
type ItemResource = apiv3.QualityProfileItemResource

// FormatItemResource is the per-entry view inside Resource.FormatItems.
type FormatItemResource = apiv3.ProfileFormatItemResource

// Lister is the read-list surface for GET /qualityprofile.
type Lister = apiv3.QualityProfileLister

// SchemaLister is the read surface for GET /qualityprofile/schema.
type SchemaLister = apiv3.QualityProfileSchemaLister

// Writer is the read+CRUD surface for the byid + write handlers.
type Writer = apiv3.QualityProfileWriter

// CustomFormatNameLister is the format-name join surface used by
// every handler that projects Resource.FormatItems[].Name.
type CustomFormatNameLister = apiv3.CustomFormatNameLister

// ListHandler aliases GET /api/v3/qualityprofile — identical wire
// (200 + list).
var ListHandler = apiv3.QualityProfileListHandler

// ByIDHandler aliases GET /api/v3/qualityprofile/{id} — identical
// wire (200 / 404).
var ByIDHandler = apiv3.QualityProfileByIDHandler

// SchemaHandler aliases GET /api/v3/qualityprofile/schema — identical
// wire (200).
var SchemaHandler = apiv3.QualityProfileSchemaHandler

// CreateHandler aliases POST /api/v3/qualityprofile — identical wire
// (201 Created on v3 already matches v5's TypedCreated semantics).
var CreateHandler = apiv3.QualityProfileCreateHandler

// UpdateHandler aliases PUT /api/v3/qualityprofile/{id} — identical
// wire (202 Accepted on v3 already matches v5's TypedAccepted).
var UpdateHandler = apiv3.QualityProfileUpdateHandler

// DeleteHandler wraps the v3 delete handler with the 200→204
// rewriter. v3 emits 200+empty on success and 409 on
// ErrQualityProfileInUse; the wrapper predicate only matches 200,
// so 409 (and 500-class errors) pass through unchanged.
func DeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.QualityProfileDeleteHandler(svc))
}
