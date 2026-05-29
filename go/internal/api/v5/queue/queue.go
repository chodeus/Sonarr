// Package queue is the v5 wire surface for /api/v5/queue.
//
// Source: src/Sonarr.Api.V5/Queue/QueueController.cs (410 LOC) —
// the LARGE-family v5 controller. Three routed endpoints, plus
// three sibling sub-controllers already ported under their own
// packages:
//
//	QueueActionController  (POST /queue/grab/{id} + /grab/bulk) → v5/queueaction (J289)
//	QueueDetailsController (GET  /queue/details)                → v5/queuedetails (J290)
//	QueueStatusController  (GET  /queue/status)                 → v5/queuestatus  (J291)
//
// This slice covers the main QueueController endpoints — paged
// list + per-id and bulk delete:
//
//	v5 endpoint                          Status   vs v3
//	GET    /api/v5/queue                 200      same — variable-alias
//	DELETE /api/v5/queue/{id}            204      **DIFFERENT** (v3 emits 200+`{}`; v5 demotes to 204+empty via v5util.RewriteSuccessTo204)
//	DELETE /api/v5/queue/bulk            204      **DIFFERENT** (v3 emits 200+`{}`; v5 demotes to 204+empty via v5util.RewriteSuccessTo204)
//
// The QueueResource wire shape is shared between v3 and v5 at the
// declared-shape level. v5-specific divergences (e.g. the v5
// `QueueSubresource` enum-array query param replacing v3's
// includeSeries/includeEpisode bools; v5 surface additions such as
// `addedToClient` / `protocol` filter expansion) are unobservable
// at the empty-DB Go-port level — `QueueHandler` today emits
// `{records:[]}` via the emptyQueuePagedLister stub, and the
// populated-row branch is a post-Wave-3 service-port arc.
//
// SignalR: v5 controller inherits from RestControllerWithSignalR.
// The QueueUpdatedEvent / PendingReleasesUpdatedEvent broadcast
// emitters are wired in cmd/sonarr/signalr_wiring.go against the
// shared apiv3.QueueResource projection — same emitter feeds both
// /signalr/messages (v3) and (future) /api/v5/signalr/messages
// channels.
//
// Wave 3 LARGE-family v5 port — Queue family (J306). Final-LOC
// queued LARGE family before the residual smaller-scope sub-slices
// (Series /{id}/season + Release POST).
package queue

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is the v5 QueueResource — variable-aliased to v3 at the
// declared-shape level. v5 sub-resource hydration
// (`includeSubresources=Series/Episodes`) DEFERRED — empty-DB
// Go-port emits zero records so divergence is unobservable at
// shadow-traffic level until populated-row service ports land.
type Resource = apiv3.QueueResource

// PagedLister re-exports v3's read-side paged interface.
type PagedLister = apiv3.QueuePagedLister

// Writer re-exports v3's write surface for the per-id + bulk
// delete handlers.
type Writer = apiv3.QueueWriter

// BulkDeleteResource re-exports v3's bulk-body shape (mirror of
// C# `QueueBulkResource`, just `{ids: int[]}`). Same shape on v5
// (QueueBulkResource.cs:1-7).
type BulkDeleteResource = apiv3.QueueBulkDeleteResource

// ListHandler is the v5 GET /api/v5/queue handler. J352 switches it
// from the v3 alias to the v5-specific `apiv3.QueueHandlerV5`, which
// parses the v5 `includeSubresources[]` enum-array query param
// (`QueueSubresource{Series, Episodes}`) in place of v3's
// `includeSeries` + `includeEpisode` bool pair, and strips the
// QueueResource to the v5 surface shape (plural Episodes gated on the
// enum; singular Episode dropped). The remaining query params
// (`includeUnknownSeriesItems` / `protocol` / `seriesIds` /
// `languages` / `quality` / `status` + the paging block) carry the
// same DEFERRED-filter posture as the v3 handler.
// ListHandler returns GET /api/v5/queue. QueueHandlerV5 already parses
// the v5 `?includeSubresources=series,episodes` enum-array param
// natively (J352) — unlike the other v5 GET surfaces it is a dedicated
// v5-aware handler, not a v3-bool alias — so no translation wrapper is
// needed here.
var ListHandler = apiv3.QueueHandlerV5

// DeleteHandler returns DELETE /api/v5/queue/{id}.
//
// Mirrors `QueueController.RemoveAction` at
// `src/Sonarr.Api.V5/Queue/QueueController.cs:72-95`:
// `Results<NoContent, NotFound>` → 204 with empty body on success
// or 404 when neither pending-release nor tracked-download row
// matches the id. The v3 handler emits 200 + body `{}` on success
// + 404 with envelope on miss; v5 strips the placeholder JSON via
// `v5util.RewriteSuccessTo204` and emits 204 + empty. 404 / 400 /
// 500 paths pass through verbatim. Side-effect query params
// (`message`, `removeFromClient`, `blocklist`, `skipRedownload`,
// `changeCategory`) PARSED-AND-IGNORED in both surfaces today —
// the v3 handler doesn't wire upstream-client side-effects
// (queue.go:40-42 doc-comment on the v3 5.E2a deferred work);
// v5 inherits the same behaviour through the alias.
func DeleteHandler(svc Writer, broadcastDeleted func(id int)) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.QueueDeleteHandler(svc, broadcastDeleted))
}

// BulkDeleteHandler returns DELETE /api/v5/queue/bulk.
//
// Mirrors `QueueController.RemoveMany` at
// `src/Sonarr.Api.V5/Queue/QueueController.cs:97-137`:
// `NoContent` → 204 with empty body. The v3 handler emits 200 +
// body `{}`; v5 demotes to 204+empty via `v5util.RewriteSuccessTo204`.
// 400 (malformed body or empty `ids` array) + 500 pass through
// verbatim. Per-id missing-row skip-silently posture preserved
// (matches v3 queue_write.go:109-117 — silent skip on Remove returning
// false, with SignalR `queue/deleted` broadcast only firing on
// actual remove).
func BulkDeleteHandler(svc Writer, broadcastDeleted func(id int)) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.QueueBulkDeleteHandler(svc, broadcastDeleted))
}
