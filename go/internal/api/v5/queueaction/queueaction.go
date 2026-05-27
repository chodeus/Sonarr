// Package queueaction is the v5 wire surface for /api/v5/queue/grab
// + /api/v5/queue/grab/bulk — the dispatch endpoints that re-grab a
// pending release from the queue (single id or bulk).
//
// Source: src/Sonarr.Api.V5/Queue/QueueActionController.cs (58 LOC).
// 2 POST endpoints. Body shape on /grab/bulk:
// QueueBulkResource{Ids: int[]} — identical to v3
// QueueBulkGrabResource (per `internal/api/v3/queue_grab.go:48`).
//
// ONE status-only divergence from v3: v5 returns 204 NoContent on
// success where v3 emits 200 + `{}`. Both endpoints wrap v3 via
// v5util.RewriteSuccessTo204 (J253/J269/J272/J277 pattern). v5
// failure paths (404 on missing pending release, 400 on invalid
// id/malformed body, 500 on dispatcher error) pass through
// unchanged because the wrapper predicate only matches 200.
//
// v3 + v5 share the same backing service trio: PendingReleaseLookup
// (the pendingreleases repo) + QueueGrabSeriesLookup (series.Service)
// + QueueGrabDispatcher (ProcessDownloadDecisions). Re-uses the v3
// adapter wiring verbatim.
package queueaction

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// PendingLookup re-exports the v3 PendingReleaseLookup interface.
type PendingLookup = apiv3.PendingReleaseLookup

// SeriesLookup re-exports the v3 QueueGrabSeriesLookup interface.
type SeriesLookup = apiv3.QueueGrabSeriesLookup

// Dispatcher re-exports the v3 QueueGrabDispatcher interface.
type Dispatcher = apiv3.QueueGrabDispatcher

// GrabHandler returns POST /api/v5/queue/grab/{id}. Wraps v3 with
// the 200→204 demote (v5util.RewriteSuccessTo204). Failure codes
// pass through (404 on missing id, 400 on invalid id, 500 on
// dispatcher error).
func GrabHandler(pending PendingLookup, series SeriesLookup, dispatcher Dispatcher) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.QueueGrabHandler(pending, series, dispatcher))
}

// GrabBulkHandler returns POST /api/v5/queue/grab/bulk. Same wrapper
// as GrabHandler; first-failure-aborts-batch semantics inherited from
// v3 verbatim. Body shape: `{ids: int[]}` — QueueBulkGrabResource on
// v3 is structurally identical to v5's QueueBulkResource.
func GrabBulkHandler(pending PendingLookup, series SeriesLookup, dispatcher Dispatcher) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.QueueGrabBulkHandler(pending, series, dispatcher))
}
