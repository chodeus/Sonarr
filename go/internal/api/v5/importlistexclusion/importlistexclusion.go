// Package importlistexclusion is the v5 wire surface for
// /api/v5/importlistexclusion — the per-series-tvdbId exclusion
// list that import-list syncs honour (skips matching titles).
//
// Source: src/Sonarr.Api.V5/ImportLists/ImportListExclusionController.cs
// (88 LOC). 6 endpoints — GET (paged) / GET /{id} / POST / PUT /
// DELETE /{id} / DELETE /bulk.
//
// PATH MAP CHANGE from v3:
//
//   - v3 has TWO list endpoints: GET /importlistexclusion (full list,
//     non-paged) AND GET /importlistexclusion/paged (paged variant).
//   - v5 collapses these — GET /importlistexclusion IS the paged
//     variant; the non-paged list endpoint is REMOVED from v5.
//
// Strategy: v5's GET aliases v3's PagedHandler (NOT the non-paged
// list handler).
//
// TWO status divergences from v3 (both DELETE endpoints):
//
//   - DELETE /{id}: v5 → 204 NoContent; v3 → 200+empty.
//   - DELETE /bulk: v5 → 204 NoContent; v3 → 200+empty.
//
// Both wrapped via v5util.RewriteSuccessTo204 (J253/J269/J272 pattern).
// Other status codes match v3 directly: GET → 200, POST → 201
// Created, PUT → 202 Accepted.
//
// Resource shape byte-identical between v3 and v5 (3 fields: id,
// tvdbId, title). BulkResource also identical (`{ids: int[]}`).
//
// Validation chain: v5 C# adds ImportListExclusionExistsValidator
// (per controller :24-26) which checks tvdbId uniqueness server-side
// before insert. v3 Go-port handles the same uniqueness check at the
// service layer (insert returns ErrAlreadyExists which the handler
// maps to 409); same wire-level enforcement, just enforced at a
// different code-path layer.
package importlistexclusion

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource re-exports v3's wire shape (3 fields).
type Resource = apiv3.ImportListExclusionResource

// BulkResource re-exports v3's `{ids: int[]}` body shape.
type BulkResource = apiv3.ImportListExclusionBulkResource

// Store re-exports v3's ImportListExclusionStore — the persistence
// interface that *importlistexclusions.Repo satisfies.
type Store = apiv3.ImportListExclusionStore

// GetHandler returns GET /api/v5/importlistexclusion (paged). Aliases
// v3's PagedHandler because v5 removes the non-paged variant and
// makes the root endpoint paged.
var GetHandler = apiv3.ImportListExclusionPagedHandler

// ByIDHandler aliases v3's GET /api/v3/importlistexclusion/{id} —
// same wire shape + status (200/404).
var ByIDHandler = apiv3.ImportListExclusionByIDHandler

// CreateHandler aliases v3's POST /api/v3/importlistexclusion — same
// wire shape + status (201 Created).
var CreateHandler = apiv3.ImportListExclusionCreateHandler

// UpdateHandler aliases v3's PUT /api/v3/importlistexclusion/{id} —
// same wire shape + status (202 Accepted).
var UpdateHandler = apiv3.ImportListExclusionUpdateHandler

// DeleteHandler wraps v3's delete handler with the 200→204 demote.
// v3 emits 200+empty on success; v5 wants 204+empty. Wrapper
// predicate only matches 200; failure codes (400 on invalid id,
// 404 on missing, 500 on store error) pass through unchanged.
func DeleteHandler(store Store) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.ImportListExclusionDeleteHandler(store))
}

// BulkDeleteHandler wraps v3's bulk-delete handler with the same
// 200→204 demote. Body shape (`{ids: int[]}`) byte-identical
// between v3 and v5.
func BulkDeleteHandler(store Store) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.ImportListExclusionBulkDeleteHandler(store))
}
