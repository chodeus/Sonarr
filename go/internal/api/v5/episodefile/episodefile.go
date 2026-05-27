// Package episodefile is the v5 wire surface for /api/v5/episodefile.
//
// Source: src/Sonarr.Api.V5/EpisodeFiles/EpisodeFileController.cs
// (205 LOC). Six endpoints — three pure variable-aliases to v3, three
// wrapped to demote the status code per the C# `TypedResults` posture:
//
//	v5 endpoint                          Status   vs v3
//	GET    /api/v5/episodefile            200      same — variable-alias
//	GET    /api/v5/episodefile/{id}       200      same — variable-alias
//	PUT    /api/v5/episodefile/{id}       202      same (TypedAccepted) — variable-alias
//	PUT    /api/v5/episodefile/bulk       200      **DIFFERENT** (v3 emits 202 + body; v5 demotes to 200 + body via v5util.RewriteAcceptedToOK)
//	DELETE /api/v5/episodefile/{id}       204      **DIFFERENT** (v3 emits 200 + `{}`; v5 demotes to 204 + empty via v5util.RewriteSuccessTo204)
//	DELETE /api/v5/episodefile/bulk       204      **DIFFERENT** (v3 emits 200 + `{}`; v5 demotes to 204 + empty via v5util.RewriteSuccessTo204)
//
// The v3-only `PUT /api/v3/episodefile/editor` overload (marked
// `[Obsolete]` in v3 and dropped entirely in v5) is NOT routed on the
// v5 surface — the C# v5 controller has no `[HttpPut("editor")]`
// action. Operators migrating from v3 should use PUT /bulk.
//
// The EpisodeFileResource wire shape was widened at J298 with
// `customFormats[]` + `customFormatScore` (Wave 1 calculator wire-
// through arc slice 3). v5 inherits the widened type unchanged via
// the alias.
//
// SignalR: v5 controller inherits from RestControllerWithSignalR.
// EpisodeFileAddedEvent + EpisodeFileDeletedEvent broadcasts are
// wired in cmd/sonarr/signalr_wiring.go against the shared
// apiv3.EpisodeFileResource projection — same emitter feeds both
// /signalr/messages (v3) and (future) /api/v5/signalr/messages
// channels.
//
// Wave 3 LARGE-family v5 port — slice 1 (J301). Smallest of the five
// LARGE families queued by the post-calculator loop scope.
package episodefile

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is the v5 EpisodeFileResource — wire-identical to v3 at
// the declared-shape level (customFormats[] + customFormatScore
// widened at J298). Populated-row joins (Languages, Quality,
// MediaInfo, IndexerFlags, ReleaseType) remain DEFERRED on both
// surfaces pending the episode-file populated mapper port.
type Resource = apiv3.EpisodeFileResource

// Reader re-exports v3's read surface for GET list / GET /{id}.
type Reader = apiv3.EpisodeFileReader

// Writer re-exports v3's write surface for PUT/DELETE handlers.
type Writer = apiv3.EpisodeFileWriter

// SeriesExistsChecker re-exports v3's series-id precondition
// checker (used by the list handler's seriesId arm to surface 404
// when the supplied seriesId doesn't resolve to a persisted row —
// matches C# `ModelNotFoundException` posture).
type SeriesExistsChecker = apiv3.SeriesExistsChecker

// ListHandler aliases v3's GET /api/v3/episodefile. Same wire shape +
// status (200/400/404). The query-param contract matches: requires
// either `seriesId` OR repeated `episodeFileIds` query params.
var ListHandler = apiv3.EpisodeFileListHandler

// ByIDHandler aliases v3's GET /api/v3/episodefile/{id}. Same wire
// shape + status (200/400/404).
var ByIDHandler = apiv3.EpisodeFileByIDHandler

// UpdateHandler aliases v3's PUT /api/v3/episodefile/{id}. v3 already
// emits 202 Accepted + EpisodeFileResource which matches v5's
// `TypedResults.Accepted<EpisodeFileResource>` — no wrapper needed.
var UpdateHandler = apiv3.EpisodeFileUpdateHandler

// BulkUpdateHandler returns PUT /api/v5/episodefile/bulk.
//
// Mirrors `EpisodeFileController.SetPropertiesBulk` at lines 145-192:
// `Ok<List<EpisodeFileResource>>` → 200 OK + body. The v3 handler
// emits 202 Accepted + identical body; v5 demotes the status to 200
// via `v5util.RewriteAcceptedToOK`. Error paths (400 malformed body /
// 404 no matching files / 500 service error) pass through verbatim.
func BulkUpdateHandler(svc Writer) http.Handler {
	return v5util.RewriteAcceptedToOK(apiv3.EpisodeFileBulkUpdateHandler(svc))
}

// DeleteHandler returns DELETE /api/v5/episodefile/{id}.
//
// Mirrors `EpisodeFileController.DeleteEpisodeFile` at lines 113-128:
// `TypedResults.NoContent()` → 204 with empty body. The v3 handler
// emits 200 + body `{}` on success; v5 strips the placeholder JSON
// and emits 204 + empty. 404 (missing-id NOT idempotent — episodefile
// is outside the J232 idempotency-aligned set; matches C# explicit
// `NzbDroneClientException(HttpStatusCode.NotFound)` throw) passes
// through verbatim. 500 service-error path passes through verbatim.
func DeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.EpisodeFileDeleteHandler(svc))
}

// BulkDeleteHandler returns DELETE /api/v5/episodefile/bulk.
//
// Mirrors `EpisodeFileController.DeleteEpisodeFiles` at lines 130-143:
// `TypedResults.NoContent()` → 204 with empty body. The v3 handler
// emits 200 + body `{}` on success (including the empty-id-list
// short-circuit); v5 strips both shapes. 400 (malformed body) / 404
// (no matching files) / 500 (service error) pass through verbatim.
func BulkDeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.EpisodeFileBulkDeleteHandler(svc))
}
