// Package seasonpass is the v5 wire surface for /api/v5/seasonpass.
//
// The v5 controller (src/Sonarr.Api.V5/SeasonPass/SeasonPassController.cs)
// is a single-POST endpoint at 99 LOC of C# source. The C# implementation
// mirrors v3 line-for-line on body shape (`SeasonPassResource` with
// `Series` + nullable `MonitoringOptions`); the ONLY divergence is the
// success status code:
//
//	v3 endpoint  : POST /api/v3/seasonpass → 202 Accepted + body `{}`
//	v5 endpoint  : POST /api/v5/seasonpass → 204 NoContent + empty body
//
// Per the Wave 3 pattern from J253/J254 STATUS notes: status difference
// only → use a `rewriteAcceptedTo204` wrapper (new variant of J253's
// `rewriteSuccessTo204` — same idea but matches 202 instead of 200).
//
// Type aliases re-export the v3 wire shapes verbatim so the v5
// namespace is importable as a stable surface; if the v5 body shape
// ever diverges (currently identical per openapi v5 SeasonPassResource
// schema), the alias can fork without breaking importers.
//
// Wave 3 v5 endpoint family port — 99 LOC C# source. J255.
package seasonpass

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is the v5 SeasonPassResource wire shape. Aliased from v3
// because the openapi v5 schema is identical (Series + MonitoringOptions).
type Resource = apiv3.SeasonPassResource

// SeriesResource is one row in the body's `series[]` array. Aliased
// from v3.
//
// Note: the C# v5 type is named `SeasonPassSeriesResource`; the Go
// alias re-uses the v3 name `SeasonPassSeriesResource` since the
// `seasonpass` package qualifier already disambiguates.
type SeriesResource = apiv3.SeasonPassSeriesResource

// SeasonOnWrite is the write-side projection of a single season's
// monitored flag inside the body. Aliased from v3.
type SeasonOnWrite = apiv3.SeasonPassSeasonOnWrite

// MonitoringOptions is the embedded `monitoringOptions` body field
// (cascade behaviour driver). Aliased from v3.
type MonitoringOptions = apiv3.MonitoringOptions

// SeriesWriter / SeriesEmitter / EpisodeMonitorCascader are aliased
// from v3 so cmd/sonarr can wire the v5 route using the same
// dependency interfaces.
type SeriesWriter = apiv3.SeriesWriter
type SeriesEmitter = apiv3.SeriesEmitter
type EpisodeMonitorCascader = apiv3.EpisodeMonitorCascader

// PostHandler returns POST /api/v5/seasonpass.
//
// Mirrors `SeasonPassController.UpdateAll` at
// `src/Sonarr.Api.V5/SeasonPass/SeasonPassController.cs:18-58`:
// `TypedResults.NoContent()` → 204 with empty body. The v3 handler
// emits 202 Accepted with body `{}`; v5 demotes status to 204 and
// strips the body.
//
// Implementation: delegate to v3 (which preserves the full
// per-series load + Monitored-flag cascade + cascader integration +
// SignalR Updated broadcasts) and rewrite the successful
// 202-with-`{}`-body to 204-empty via the local `rewriteAcceptedTo204`
// middleware. Error paths (400 / 404 / 500) pass through unchanged.
//
// J255.
func PostHandler(svc SeriesWriter, emitter SeriesEmitter, cascader EpisodeMonitorCascader) http.Handler {
	return v5util.RewriteAcceptedTo204(apiv3.SeasonPassHandler(svc, emitter, cascader))
}

// Response-rewriting middleware moved to internal/api/v5/internal/v5util
// at J256 (consolidation). Handler above uses v5util.RewriteAcceptedTo204.
