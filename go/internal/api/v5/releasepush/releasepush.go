// Package releasepush is the v5 wire surface for /api/v5/release/push
// — the push endpoint that lets external tools submit a release
// (with URL + title + protocol metadata) for Sonarr to evaluate via
// the decision engine and dispatch to the configured download client.
//
// Source: src/Sonarr.Api.V5/Release/ReleasePushController.cs (120 LOC).
// 2 endpoints:
//
//   - POST /release/push       — push a release for evaluation + dispatch
//   - GET  /release/push/{id}  — inherited RestController endpoint (no-op)
//
// POST /release/push is a pure path-alias to v3's ReleasePushHandler.
// Both v3 and v5 emit 200 OK + ReleaseResource on success; 400 on
// validation failure (Title/DownloadUrl/MagnetUrl/Protocol/PublishDate
// per the C# PostValidator). The ReleaseResource projection is part
// of the J237 deferred chain — current v3 Go-port emits a stub
// projection; v5 inherits the same.
//
// GET /release/push/{id} is the inherited RestController<TResource>
// endpoint that the C# v5 controller does NOT override. Push
// operations are NOT persisted — there's no underlying lookup that
// makes sense. The C# v5 RestController base throws/returns null
// which surfaces as 404 NotFound. v5 Go-port emits 404 directly to
// match this contract — same J266/J270/J274 "v3-port-has-no-handler
// gap → v5 ByID stub" pattern.
//
// SignalR: ReleasePushController does NOT declare any IHandle<>
// interfaces — push operations don't broadcast their own events
// (the underlying dispatcher's grab event flows through the existing
// command-bus subscription).
package releasepush

import (
	"fmt"
	"net/http"
	"strconv"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/httperr"
)

// PushBody re-exports v3's ReleasePushBody (the input body shape) —
// 13 fields, matches the v5 ReleasePushResource fields used by the
// C# Create method.
type PushBody = apiv3.ReleasePushBody

// DecisionMaker re-exports v3's narrow decision-engine interface.
type DecisionMaker = apiv3.ReleaseDecisionMaker

// Dispatcher re-exports v3's narrow dispatch interface.
type Dispatcher = apiv3.ReleaseDispatcher

// IndexerLookup re-exports v3's narrow indexer-resolution interface.
type IndexerLookup = apiv3.ReleasePushIndexerLookup

// DownloadClientLookup re-exports v3's narrow download-client-resolution interface.
type DownloadClientLookup = apiv3.ReleasePushDownloadClientLookup

// PostHandler aliases v3's POST /api/v3/release/push — same wire
// shape + status (200 / 400) + validation chain. ReleaseResource
// projection inherits the J237 deferred state from v3.
var PostHandler = apiv3.ReleasePushHandler

// ByIDHandler returns GET /api/v5/release/push/{id}. v5 inherits this
// endpoint from RestController<ReleasePushResource> but the C#
// controller does not override GetResourceById — push operations are
// not persisted so there's no underlying lookup. Always emits 404
// NotFound (matching the C# inherited behaviour which throws when
// the base resolution returns null).
//
// 400 on invalid id (non-positive int).
func ByIDHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "release/push: invalid id")
			return
		}
		// Push releases aren't persisted; no underlying lookup exists.
		// The C# v5 RestController<ReleasePushResource> base returns
		// null from GetResourceById which propagates to 404 — match
		// the wire contract directly.
		httperr.NotFound(w, fmt.Sprintf("release/push %d not found (pushes are not persisted)", id))
	})
}
