// Package command is the v5 wire surface for /api/v5/command.
//
// The v5 family (src/Sonarr.Api.V5/Commands/) is 247 LOC of C# across
// 2 files — CommandController (130), CommandResource (117).
//
// Four endpoints:
//
//	v5 endpoint                  v5 status   vs v3 Go-port (command.go)
//	GET    /api/v5/command           200       same as /api/v3/command list
//	POST   /api/v5/command           201       same (v3 Go-port already emits 201)
//	GET    /api/v5/command/{id}      200       same
//	**DELETE /api/v5/command/{id}**  **204**   v3 emits 200 + `{}`
//
// **Resource shape** — IDENTICAL field-for-field to v3 CommandResource:
//
// v5 carries: id, name, commandName, message, body (Command? polymorphic
// payload), priority, status, result, queued, started?, ended?,
// duration?, exception, trigger, clientUserAgent, stateChangeTime?
// (computed from started ?? ended), sendUpdatesToClient (computed
// from body), updateScheduledTask (computed from body), lastExecutionTime?
// — plus `completionMessage` declared with `[JsonIgnore]` so it never
// hits the wire. v3 Go-port carries the same fields with the same
// JSON tags + same computed-field semantics; the C# computed
// properties simply project the same precedence the v3 mapper hand-
// rolls. JSON output: byte-identical at populated AND empty corpus.
//
// ONE status divergence only: DELETE/{id} 200+`{}` → 204+empty via
// v5util.RewriteSuccessTo204. Other 3 endpoints path-alias — v3
// Go-port's POST already emits 201 matching v5; GET endpoints share
// 200 directly.
//
// SignalR: v5 CommandController declares `IHandle<CommandUpdatedEvent>`
// with a 100ms debouncer that pushes per-id updates via
// `BroadcastResourceChange(ModelAction.Updated, ...)`. The v3 Go-port
// wires the equivalent broadcast off the command-bus event subscription
// in cmd/sonarr/signalr_wiring.go; that carries through to the v5
// surface unchanged because both versions share the same backing
// emitter channel. The Wave 2 SignalR mop-up has a separate
// MessagingCleanupCommand Sync side-effect that the v5 C# adds at
// `:120-122` (Sync broadcast when MessagingCleanupCommand completes)
// — not yet wired on either the v3 or v5 Go-port side; tracked under
// Wave 2.
//
// Wave 3 v5 endpoint family port — 247 LOC C# source. J272.
package command

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is an alias of the v3 CommandResource wire shape
// (unchanged in v5 — same fields, same JSON tags, same computed-
// property semantics).
type Resource = apiv3.CommandResource

// Service is an alias of the v3 read-and-CUD interface.
type Service = apiv3.CommandService

// NameResolver is an alias of the v3 case-insensitive name
// canonicalizer.
type NameResolver = apiv3.CommandNameResolver

// ----- pure-status-match handlers (re-exported verbatim) -----

// ListHandler returns GET /api/v5/command. Same envelope + status as
// v3 (priority-then-status sort happens at the service layer).
var ListHandler = apiv3.CommandListHandler

// ByIDHandler returns GET /api/v5/command/{id}. Same envelope +
// status (200 or 404) as v3.
var ByIDHandler = apiv3.CommandByIDHandler

// CreateHandler returns POST /api/v5/command (201 + Resource). The
// v3 Go-port handler already emits 201 (per `internal/api/v3/command.go:161`),
// matching the v5 openapi `Created<>` declaration.
var CreateHandler = apiv3.CommandCreateHandler

// ----- status-rewriting wrapper -----

// DeleteHandler returns DELETE /api/v5/command/{id}. v3 emits
// 200 + `{}` (per `:241`); v5 emits 204 + empty (TypedResults.NoContent).
// Wraps v3 via v5util.RewriteSuccessTo204.
//
// J272.
func DeleteHandler(svc Service) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.CommandDeleteHandler(svc))
}
