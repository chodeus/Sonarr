// Package episode is the v5 wire surface for /api/v5/episode and
// /api/v5/episode/monitor.
//
// Source: src/Sonarr.Api.V5/Episodes/EpisodeController.cs (85 LOC).
// 4 endpoints:
//
//   - GET /episode (filtered by seriesId/seasonNumber/episodeIds/episodeFileId)
//   - GET /episode/{id}
//   - PUT /episode/{id}            — single-id monitored set (v5-NEW; v3 has no per-id PUT)
//   - PUT /episode/monitor          — bulk monitored set (v5: 200, v3: 202)
//
// Two divergences from v3:
//
//  1. v5 PUT /episode/monitor returns 200 Ok where v3 returns 202
//     Accepted. Wrapped via v5util.RewriteAcceptedToOK (J253/J265
//     pattern — 202+body → 200+body, body preserved byte-for-byte).
//
//  2. v5 PUT /episode/{id} is NEW — v3 only exposes the bulk
//     /monitor endpoint. The C# v5 implementation calls
//     `_episodeService.SetEpisodeMonitored(id, resource.Monitored)`
//     then re-fetches via GetEpisode and emits 200 + EpisodeResource.
//     New v5-specific handler ports the C# semantics directly.
//
// GET /episode + GET /episode/{id} are pure path-aliases to v3. The
// v5-only `includeSubresources` enum-array query param replaces v3's
// includeSeries/includeEpisodeFile/includeImages bools — invisible at
// the empty-corpus level because v3 Go-port handlers ignore all
// sub-resource flags (DEFERRED — populated-row sub-resource hydration
// needs the customformats calculator + series/episodefile join). Same
// J290 "query-param-shift invisible at stub level" sub-pattern.
//
// SignalR: v5 controller inherits from EpisodeControllerWithSignalR.
// The Updated event broadcast emitter is shared with v3 via the bus
// subscription in cmd/sonarr/signalr_wiring.go — carries through
// unchanged.
package episode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httperr"
)

// Resource is the v5 EpisodeResource — wire-identical to v3 at the
// declared-shape level. Sub-resource hydration (series/episodeFile/
// images) DEFERRED on both surfaces.
type Resource = apiv3.EpisodeResource

// Lister re-exports v3's read-side interface.
type Lister = apiv3.EpisodeBySeriesLister

// ByIDGetter re-exports v3's by-id read interface.
type ByIDGetter = apiv3.EpisodeByIDGetter

// Writer re-exports v3's write surface.
type Writer = apiv3.EpisodeMonitorWriter

// ListHandler aliases v3's GET /api/v3/episode. Same wire shape +
// status (200/400) — re-exported.
var ListHandler = apiv3.EpisodeListHandler

// ByIDHandler aliases v3's GET /api/v3/episode/{id}. Same wire shape
// + status (200/404) — re-exported.
var ByIDHandler = apiv3.EpisodeByIDHandler

// MonitorHandler wraps v3's PUT /api/v3/episode/monitor with the
// 202→200 demote (v5 returns Ok where v3 returned Accepted). Body
// preserved byte-for-byte; failure codes (400 on malformed body /
// missing episodeIds, 500 on service error) pass through unchanged.
func MonitorHandler(svc Writer) http.Handler {
	return v5util.RewriteAcceptedToOK(apiv3.EpisodeMonitorHandler(svc))
}

// ByIDPutHandler returns PUT /api/v5/episode/{id} — the v5-NEW
// per-id monitored-set endpoint. Mirrors
// `EpisodeController.SetEpisodeMonitored(int, EpisodeResource)`:
//
//	_episodeService.SetEpisodeMonitored(id, resource.Monitored);
//	resource = MapToResource(_episodeService.GetEpisode(id), false, false, false);
//	return TypedResults.Ok(resource);
//
// 200 + updated EpisodeResource on success. 400 on invalid id or
// malformed body. 404 on missing episode. 500 on service error.
func ByIDPutHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "episode: invalid id")
			return
		}
		var body Resource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("episode: malformed body: %v", err))
			return
		}
		ctx := r.Context()
		if err := svc.SetEpisodeMonitored(ctx, id, body.Monitored); err != nil {
			if errors.Is(err, episodes.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("episode %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episode: SetEpisodeMonitored %d: %w", id, err), nil)
			return
		}
		// Re-fetch via the batch surface (size-1) and emit the updated
		// row. The batch method is already on EpisodeMonitorWriter; no
		// new interface needed.
		rows, err := svc.GetEpisodes(ctx, []int{id})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("episode: reload %d: %w", id, err), nil)
			return
		}
		if len(rows) == 0 {
			httperr.NotFound(w, fmt.Sprintf("episode %d not found after update", id))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(apiv3.ToEpisodeResource(toBackingEpisode(rows[0])))
		_, _ = w.Write(buf)
	})
}

// toBackingEpisode is the identity adapter — apiv3.ToEpisodeResource
// takes episodes.Episode and rows is already that type. The function
// exists only to type-check the assignment cleanly in case the
// service signature widens later.
func toBackingEpisode(e episodes.Episode) episodes.Episode { return e }

// silence unused context import if a future refactor narrows the
// handler chain.
var _ = context.Background
