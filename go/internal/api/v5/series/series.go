// Package series is the v5 wire surface for /api/v5/series.
//
// Source: src/Sonarr.Api.V5/Series/SeriesController.cs (422 LOC) —
// the LARGE-family v5 controller. This sub-slice ports the
// SeriesController CRUD endpoint set (5 endpoints); the v5-NEW
// per-id season-monitor endpoint (PUT /api/v5/series/{id}/season)
// is deferred to a follow-up sub-slice that touches series.Service
// (single-season monitored-set; new code, no v3 equivalent).
//
//	v5 endpoint                          Status   vs v3
//	GET    /api/v5/series                200      same — variable-alias
//	GET    /api/v5/series/{id}           200/404  same — variable-alias
//	POST   /api/v5/series                201      same (TypedCreated) — variable-alias
//	PUT    /api/v5/series/{id}           202      same (TypedAccepted) — variable-alias
//	DELETE /api/v5/series/{id}           204      **DIFFERENT** (v3 emits 200+`{}`; v5 demotes to 204+empty via v5util.RewriteSuccessTo204)
//
// DEFERRED to a subsequent sub-slice:
//   - PUT /api/v5/series/{id}/season — v5-NEW per-id season-monitor
//     endpoint at SeriesController.cs:223-246. Returns
//     `Ok<SeasonResource>` (200 + SeasonResource); 404 when the
//     SeasonNumber doesn't match. Genuinely new wire shape — no v3
//     equivalent — and needs single-season monitored-set on
//     series.Service.
//   - v5-specific SeriesResource field divergences: `MalIds`,
//     `AniListIds`, `OriginalCountry`, `IsExcluded` (added in v5);
//     `LanguageProfileId` (computed expr `=> 1` in v3; absent in
//     v5). All v3 `string`/`List` fields are made `nullable` in v5
//     but STJson's `DefaultIgnoreCondition.WhenWritingNull` config
//     (`STJson.cs:27`) makes the wire indistinguishable when the
//     populated values match. Empty-DB Go-port today emits zero
//     rows, so the divergences are unobservable at the
//     shadow-traffic level. When populated-row mappers land, the
//     SeriesResource widening will be a separate slice.
//   - `includeSubresources[]` query-param filtering (C# SeriesSubresource
//     enum at `SeriesSubresource.cs`). Today the v3 Go-port handlers
//     ignore all sub-resource flags (same J290 "query-param-shift
//     invisible at stub level" pattern as v5/episode). When the
//     populated-mapper port lands the v5 list/by-id handlers will
//     route `includeSubresources=seasonImages` through the
//     ToResource(includeSeasonImages: bool) flag — separate slice.
//
// SignalR: v5 controller inherits from RestControllerWithSignalR.
// The Created/Updated/Deleted broadcast emitters are shared with v3
// via cmd/sonarr/signalr_wiring.go — the v5 routes feed the SAME
// `series` SignalR resource name through the shared
// signalr.NewV3Emitter[apiv3.SeriesResource] instance.
//
// Wave 3 LARGE-family v5 port — Series family sub-slice 1 of 2
// (CRUD; SeriesEditor + /{id}/season deferred). J303.
package series

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is the v5 SeriesResource — variable-aliased to v3 at the
// declared-shape level. v5-specific fields (MalIds, AniListIds,
// OriginalCountry, IsExcluded) DEFERRED to the populated-mapper
// slice — empty-DB Go-port today emits zero rows so the divergence
// is unobservable at the shadow-traffic level.
type Resource = apiv3.SeriesResource

// Lister re-exports v3's read-side list interface.
type Lister = apiv3.SeriesLister

// ByIDGetter re-exports v3's by-id read interface.
type ByIDGetter = apiv3.SeriesByIDGetter

// Writer re-exports v3's write surface (POST / PUT / DELETE).
type Writer = apiv3.SeriesWriter

// Emitter re-exports v3's SignalR broadcast surface.
type Emitter = apiv3.SeriesEmitter

// ListHandler aliases v3's GET /api/v3/series. Same wire shape +
// status (200/500). `tvdbId` + `includeSubresources[]` query params
// route through unchanged — sub-resource filtering DEFERRED at the
// v3 layer per the populated-mapper gap.
var ListHandler = apiv3.SeriesListHandler

// ByIDHandler aliases v3's GET /api/v3/series/{id}. Same wire shape
// + status (200/400/404/500).
var ByIDHandler = apiv3.SeriesByIDHandler

// CreateHandler aliases v3's POST /api/v3/series. v3 already emits
// 201 Created + SeriesResource matching v5's
// `TypedCreated<SeriesResource>` — no wrapper needed.
var CreateHandler = apiv3.SeriesCreateHandler

// UpdateHandler aliases v3's PUT /api/v3/series/{id}. v3 already
// emits 202 Accepted + SeriesResource matching v5's
// `TypedResults.Accepted<SeriesResource>` (TypedAccepted helper) —
// no wrapper needed. `moveFiles` query param routes through
// unchanged.
var UpdateHandler = apiv3.SeriesUpdateHandler

// DeleteHandler returns DELETE /api/v5/series/{id}.
//
// Mirrors `SeriesController.DeleteSeries` at lines 248-254:
// `TypedResults.NoContent()` → 204 with empty body. The v3 handler
// emits 200 + body `{}` on success; v5 strips the placeholder JSON
// and emits 204 + empty via v5util.RewriteSuccessTo204. 404
// (missing id — series DELETE is NOT in the J232 idempotency set;
// matches C# `_seriesService.DeleteSeries` throwing
// `ModelNotFoundException` which surfaces as 404 via the global
// exception filter) passes through verbatim. 500 service-error path
// also passes through verbatim. `deleteFiles` +
// `addImportListExclusion` query params route through unchanged.
func DeleteHandler(svc Writer, emitter Emitter) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.SeriesDeleteHandler(svc, emitter))
}
