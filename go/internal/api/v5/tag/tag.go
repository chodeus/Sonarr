// Package tag is the v5 wire surface for /api/v5/tag and
// /api/v5/tag/detail.
//
// The v5 family (src/Sonarr.Api.V5/Tags/) is 192 LOC of C# across 4
// files — TagController (82), TagDetailsController (31),
// TagResource (35), TagDetailsResource (44).
//
// Seven endpoints across two controllers:
//
//	v5 endpoint                          v5 status   vs v3 Go-port
//	GET    /api/v5/tag                       200       same as /api/v3/tag
//	POST   /api/v5/tag                       201       same (Go-port already emits 201)
//	GET    /api/v5/tag/{id}                  200       same
//	PUT    /api/v5/tag/{id}                  202       same (Go-port already emits 202)
//	**DELETE /api/v5/tag/{id}**              **204**   v3 emits 200 + empty
//	GET    /api/v5/tag/detail                200       same
//	GET    /api/v5/tag/detail/{id}           200/404   same
//
// Resource shapes IDENTICAL field-for-field to v3:
//
//   - TagResource — `{id, label}`. v3 + v5 emit the same 2-field
//     shape; both apply RestResource's `JsonIgnore(WhenWritingDefault)`
//     to id (omitted when zero in PUT responses).
//   - TagDetailsResource — `{id, label, delayProfileIds[],
//     importListIds[], notificationIds[], restrictionIds[],
//     excludedReleaseProfileIds[], indexerIds[], downloadClientIds[],
//     autoTagIds[], seriesIds[]}` — 10 fields, all []int defaulted to
//     `[]` (never null). Same on v5.
//
// One status divergence only: DELETE/{id} 200+empty → 204+empty via
// v5util.RewriteSuccessTo204 (matches the empty-body predicate).
//
// SignalR: v5 TagController declares `IHandle<TagsUpdatedEvent>` +
// `IHandle<AutoTagsUpdatedEvent>` and emits `Sync` on both. The v3
// Go-port wires the equivalent Sync emit via the bus subscription
// in cmd/sonarr/signalr_wiring.go off TagsUpdatedEvent (per
// `internal/api/v3/tag.go:148` comment). AutoTagsUpdatedEvent Sync
// emit on the tag channel is Wave 2 mop-up — not part of this slice.
//
// Wave 3 v5 endpoint family port — 192 LOC C# source. J269.
package tag

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is an alias of the v3 TagResource wire shape (unchanged
// in v5 — 2 fields).
type Resource = apiv3.TagResource

// DetailsResource is an alias of the v3 TagDetailsResource wire shape
// (unchanged in v5 — 10 fields).
type DetailsResource = apiv3.TagDetailsResource

// Lister is an alias of the v3 read interface (tag list).
type Lister = apiv3.TagLister

// Writer is an alias of the v3 CRUD interface (tag CUD + GetByID).
type Writer = apiv3.TagWriter

// DetailLister is an alias of the v3 tag-detail aggregated read
// interface.
type DetailLister = apiv3.TagDetailLister

// DetailByIDGetter is an alias of the v3 tag-detail per-id read
// interface.
type DetailByIDGetter = apiv3.TagDetailByIDGetter

// ----- pure-status-match handlers (re-exported verbatim) -----

// ListHandler returns GET /api/v5/tag. Same envelope + status as v3.
var ListHandler = apiv3.TagHandler

// ByIDHandler returns GET /api/v5/tag/{id}. Same envelope + status
// as v3.
var ByIDHandler = apiv3.TagByIDHandler

// CreateHandler returns POST /api/v5/tag (201 + Resource). The v3
// Go-port handler already emits 201 (per `internal/api/v3/tag.go:164`),
// matching the v5 openapi `Created<>` declaration.
var CreateHandler = apiv3.TagCreateHandler

// UpdateHandler returns PUT /api/v5/tag/{id} (202 + Resource). The
// v3 Go-port handler already emits 202 (per `:202`), matching the v5
// openapi `Accepted<>` declaration.
var UpdateHandler = apiv3.TagUpdateHandler

// DetailListHandler returns GET /api/v5/tag/detail. Same envelope +
// status as v3.
var DetailListHandler = apiv3.TagDetailHandler

// DetailByIDHandler returns GET /api/v5/tag/detail/{id}. Same
// envelope + status (200 or 404) as v3.
var DetailByIDHandler = apiv3.TagDetailByIDHandler

// ----- status-rewriting wrappers -----

// DeleteHandler returns DELETE /api/v5/tag/{id}. v3 emits 200 +
// empty body (raw WriteHeader, no payload); v5 emits 204 + empty
// (TypedResults.NoContent). Wraps v3 via v5util.RewriteSuccessTo204.
//
// J269.
func DeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.TagDeleteHandler(svc))
}
