// Package queuedetails is the v5 wire surface for
// /api/v5/queue/details — the per-series / per-episode queue
// projection (paged-queue's sibling that's NOT paged).
//
// Source: src/Sonarr.Api.V5/Queue/QueueDetailsController.cs (77 LOC).
// Single GET endpoint that takes optional ?seriesId + ?episodeIds[]
// + ?includeSubresources[] filters and returns a List<QueueResource>.
//
// Pure path-alias slice — same J224/J280/J284 template. v3 already
// exposes /api/v3/queue/details via apiv3.QueueDetailsHandler with
// byte-identical wire shape (200 + `[]` at empty-corpus today; the
// underlying queue + pending-release projection chain is DEFERRED
// per `internal/api/v3/queue.go:46-57` — production wiring substitutes
// `emptyQueueDetailsLister` matching the recorded empty-DB shape).
//
// QUERY-PARAM SHIFT (invisible at deferred-empty level): v5 collapses
// v3's `includeSeries` + `includeEpisode` bools into a single
// `includeSubresources` enum array (QueueSubresource.Series /
// QueueSubresource.Episodes). v3 Go-port handler ignores all
// sub-resource flags (per the deferred state); v5 handler ignores
// `includeSubresources` for the same reason. When the populated-row
// projection lands, both surfaces gain the per-row Series/Episodes
// hydration via a shared widening at the apiv3.QueueDetailsLister
// service-impl side; the v5 wire flag (`includeSubresources`) and v3
// wire flags (`includeSeries` + `includeEpisode`) get parsed into the
// same internal include-bitmap.
//
// SignalR: v5 controller declares IHandle<QueueUpdatedEvent> +
// IHandle<PendingReleasesUpdatedEvent> with Sync broadcasts. v3
// Go-port wires equivalent broadcasts via the bus subscription in
// cmd/sonarr/signalr_wiring.go — carries through unchanged because
// both versions share the same backing emitter channel.
package queuedetails

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource re-exports v3 QueueResource (unchanged in v5 at the
// declared-shape level — additionalProperties:false on both sides).
type Resource = apiv3.QueueResource

// Lister re-exports v3 QueueDetailsLister.
type Lister = apiv3.QueueDetailsLister

// GetHandler returns GET /api/v5/queue/details. Same wire shape +
// status codes as v3 — re-exported.
var GetHandler = apiv3.QueueDetailsHandler
