// Package health is the v5 wire surface for /api/v5/health.
//
// The v5 controller (src/Sonarr.Api.V5/Health/HealthController.cs)
// inherits from RestControllerWithSignalR but overrides the by-id
// resolver with `[NonAction]` — v5 has no GET /{id} endpoint, only
// the GET list endpoint. Single-endpoint family at 82 LOC of C#
// source.
//
// The v5 HealthResource shape DIFFERS from v3 in one field: v5
// adds `reason` (HealthCheckReason enum, emitted camelCase per the
// openapi v5 spec). v3 carries Source / Type / Message / WikiURL
// only. So this is NOT a pure path-alias — the v5 package owns its
// own Resource type + List handler that maps healthcheck.HealthCheck
// → v5.Resource including the Reason field.
//
// Wire-format choices for the v5 surface:
//   - `type`   — camelCase enum name via Result.String()
//                ("ok" / "notice" / "warning" / "error").
//   - `reason` — camelCase enum name via Reason.String() (J254 added
//                the reverse-lookup table to healthcheck/healthcheck.go).
//
// The v3 Go-port emits Type as string too (a pre-existing divergence
// from C# v3's default int emission); v5 follows the openapi-v5 spec
// which IS explicit about camelCase strings for both enums.
//
// Wave 3 v5 endpoint family port — 82 LOC of C# source. J254.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/healthcheck"
)

// Resource is the v5 HealthResource wire shape. Distinct from
// apiv3.HealthResource because v5 adds the `reason` field per the
// openapi v5 spec.
//
// JSON omitempty notes:
//   - id is omitempty so empty-DB / on-the-fly health entries that
//     never get a row id don't emit `"id": 0`. Matches the C#
//     RestResource convention (Id is non-nullable int but C# treats
//     zero as "not yet persisted").
type Resource struct {
	ID      int    `json:"id,omitempty"`
	Source  string `json:"source"`
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	WikiURL string `json:"wikiUrl"`
}

// Lister is an alias of the v3 read interface. *healthcheck.Service
// satisfies it via Go structural typing.
type Lister = apiv3.HealthResultsLister

// ListHandler returns GET /api/v5/health. Mirrors
// `HealthController.GetHealth` at
// `src/Sonarr.Api.V5/Health/HealthController.cs:39-44`:
// `TypedResults.Ok(_healthCheckService.Results().ToResource())`.
//
// Sort matches v3's deterministic order (Result-DESC then Source-ASC)
// — see apiv3.HealthHandler doc-comment at health.go:60-66 for why
// the Go-port imposes this rather than relying on the underlying
// map iteration order.
//
// J254.
func ListHandler(lister Lister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = context.Background() // future-proof; if Results() grows ctx use the request's
		results := lister.Results()
		sort.SliceStable(results, func(i, j int) bool {
			if results[i].Type != results[j].Type {
				return results[i].Type > results[j].Type
			}
			return results[i].Source < results[j].Source
		})
		out := make([]Resource, 0, len(results))
		for _, hc := range results {
			out = append(out, toResource(hc))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// toResource projects a healthcheck.HealthCheck onto the v5 wire
// shape. Source / Message / WikiURL are 1:1; Type + Reason emit the
// camelCase enum names per the openapi v5 spec.
//
// J254.
func toResource(hc healthcheck.HealthCheck) Resource {
	return Resource{
		Source:  hc.Source,
		Type:    hc.Type.String(),
		Reason:  hc.Reason.String(),
		Message: hc.Message,
		WikiURL: hc.WikiURL,
	}
}
