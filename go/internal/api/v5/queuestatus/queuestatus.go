// Package queuestatus is the v5 wire surface for /api/v5/queue/status
// — the lightweight queue summary endpoint (counts + warning/error
// booleans) that drives the React UI's top-bar queue badge.
//
// Source: src/Sonarr.Api.V5/Queue/QueueStatusController.cs (80 LOC).
// Single GET endpoint that snapshots the in-process queue + pending
// release count into 7 fields (totalCount, count, unknownCount,
// errors, warnings, unknownErrors, unknownWarnings).
//
// Pure path-alias slice — same J224/J280/J290 template. v3 already
// exposes /api/v3/queue/status via apiv3.QueueStatusHandler with
// matching field-by-field wire shape, status (200), and SignalR
// integration via the shared Pauser debouncer-bracket pattern (per
// `internal/api/v3/queue_status.go:90-102`).
//
// SMALL WIRE DIVERGENCE (potential): v5 openapi schema includes
// `id` as a property (inherited RestResource.Id), so the v5 wire
// likely emits `"id":0` whereas v3 Go-port's QueueStatusResource
// struct doesn't declare an Id field. The C# RestResource may have
// [JsonIgnore(WhenWritingDefault)] on Id, in which case both
// surfaces emit identical bodies. If a real-world v5 client expects
// `"id":0` in the body, a small field-add wrapper would inject it —
// inverse of the J283 field-strip wrap pattern. Logged as a v3-side
// audit follow-up; current slice ships the path-alias for the
// common-case path.
//
// SignalR: v5 controller declares IHandle<QueueUpdatedEvent> +
// IHandle<PendingReleasesUpdatedEvent> with a 5-second Debouncer
// driving Updated broadcasts (per QueueStatusController.cs:29 +
// :63-66). v3 Go-port wires equivalent broadcasts via the bus
// subscription in cmd/sonarr/signalr_wiring.go — carries through
// unchanged. The Pauser debouncer-bracket pattern (Pause around
// the GET projection, Resume after) prevents the GET handler's own
// projection from re-arming the broadcaster debouncer.
package queuestatus

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource re-exports v3 QueueStatusResource (7 fields; same JSON
// tags + types as v5 schema apart from the potential id field
// noted above).
type Resource = apiv3.QueueStatusResource

// QueueLister is the read-side queue snapshot interface.
type QueueLister = apiv3.QueueLister

// PendingLister is the pending-release count interface.
type PendingLister = apiv3.PendingLister

// Pauser is the SignalR debouncer pause/resume bracket.
type Pauser = apiv3.Pauser

// GetHandler returns GET /api/v5/queue/status. Same wire shape +
// status codes as v3 — re-exported.
var GetHandler = apiv3.QueueStatusHandler
