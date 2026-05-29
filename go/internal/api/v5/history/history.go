// Package history is the v5 wire surface for /api/v5/history.
//
// The v5 family (src/Sonarr.Api.V5/History/) is 232 LOC of C# across
// 3 files — HistoryController (172), HistoryResource (53),
// HistorySubresource enum (7).
//
// Six endpoints:
//
//	v5 endpoint                          v5 status   vs v3 Go-port
//	GET /api/v5/history                     200       same as /api/v3/history (paged envelope)
//	GET /api/v5/history/since               200       same as /api/v3/history/since
//	GET /api/v5/history/series              200       same as /api/v3/history/series
//	GET /api/v5/history/season              200       v3 Go-port has NO handler — new here
//	GET /api/v5/history/episode             200       v3 Go-port has NO handler — new here
//	**POST /api/v5/history/failed/{id}**    **204**   v3 emits 200 + `{}`
//
// **Wire-contract diffs vs v3**:
//
//  1. **HistoryResource widens in v5.** The C# v5 Resource carries
//     14 fields (id, episodeId, seriesId, sourceTitle, languages[],
//     quality, customFormats[], customFormatScore, qualityCutoffNotMet,
//     date, downloadId, eventType, data{}, episode?, series?). The
//     v3 Go-port Resource is DELIBERATELY REDUCED to 7 fields per
//     `internal/api/v3/history.go:25-33` (id/seriesId/episodeId/
//     sourceTitle/date/downloadId/eventType) pending the populated-
//     row mapping port — same deferred chain as J258/J259/J261/J264/J268.
//     At the empty-corpus level (`records: []`) the envelope shape is
//     identical, so path-alias is byte-equal. Populated-row widening
//     lands when the calculator + Languages/Quality joins port.
//
//  2. **Query-param shape differs (history list).** v5 takes
//     `eventType[]`, `episodeId`, `downloadId`, `seriesIds[]`,
//     `languages[]`, `quality[]`, `includeSubresources[]` (enum CSV).
//     v3 Go-port handler today ignores most filters (deferred per
//     `:43-49`). Invisible at the empty-corpus level. Same chain.
//
//  3. **NEW endpoints**: `/history/season` (takes seriesId +
//     seasonNumber) and `/history/episode` (takes episodeId). The v3
//     Go-port doesn't expose these; the underlying
//     `historyService.GetBySeason` + `GetByEpisode` methods are
//     marked DEFERRED in `internal/history/service.go:127` (only
//     GetBySeries is currently ported). For empty-corpus parity, the
//     v5 surface emits `[]` from stub handlers — same J266 pattern
//     ("v3-port-has-no-handler gap closed on v5 side only"). When
//     the service methods land, these handlers can swap from stub to
//     real lookup without rewiring.
//
//  4. **Status divergence**: POST /history/failed/{id} v5 returns
//     204 (TypedResults.NoContent); v3 Go-port emits 200 + `{}`.
//     Wraps v3 via v5util.RewriteSuccessTo204.
//
// Wave 3 v5 endpoint family port — 232 LOC C# source. J270.
package history

import (
	"net/http"
	"strconv"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
	"sonarr.tv/sonarr/internal/httperr"
)

// Resource is an alias of the v3 (reduced) HistoryResource wire
// shape. Populated-row widening (languages/quality/customFormats/
// data/episode/series) deferred per the package doc above.
type Resource = apiv3.HistoryResource

// PagedLister is an alias of the v3 read interface used by the list
// endpoint.
type PagedLister = apiv3.HistoryPagedLister

// RangeReader is an alias of the v3 since/series read interface.
type RangeReader = apiv3.HistoryRangeReader

// FailedDownloadMarker is an alias of the v3 MarkAsFailed interface.
type FailedDownloadMarker = apiv3.FailedDownloadMarker

// ----- path-alias handlers (list/since/series) -----

// historySubresourceMapping maps the v5 HistorySubresource enum tokens
// (Series/Episode) onto the v3 include-flag query keys.
var historySubresourceMapping = map[string]string{
	"series":  "includeSeries",
	"episode": "includeEpisode",
}

// ListHandler returns GET /api/v5/history. Same paged envelope +
// default sort ("date" DESC) + status (200) as v3. Translates the v5
// `?includeSubresources=series,episode` enum-array param onto v3's
// include bools (J418).
func ListHandler(lister apiv3.HistoryPagedLister) http.Handler {
	return v5util.TranslateIncludeSubresources(apiv3.HistoryHandler(lister), historySubresourceMapping)
}

// SinceHandler returns GET /api/v5/history/since. Same `[]Resource`
// envelope + required `date` query + status (200) as v3.
var SinceHandler = apiv3.HistorySinceHandler

// SeriesHandler returns GET /api/v5/history/series. Same envelope +
// required `seriesId` query + status (200) as v3. seasonNumber
// filter is DEFERRED in v3 (per `:117-118`); v5 has a dedicated
// /season endpoint instead (see below).
var SeriesHandler = apiv3.HistorySeriesHandler

// ----- new v5-specific stub handlers (v3 has no equivalent) -----

// SeasonHandler returns GET /api/v5/history/season. Mirrors
// `HistoryController.GetSeasonHistory` at
// `src/Sonarr.Api.V5/History/HistoryController.cs:118-130`:
// historyService.GetBySeason(seriesId, seasonNumber, eventType).
//
// **Empty-corpus stub** — the underlying `history.Service.GetBySeason`
// is DEFERRED in the Go port (per `internal/history/service.go:127`
// — same package-level deferred-methods list); only GetBySeries is
// ported today. For empty-corpus parity this handler validates the
// `seriesId` + `seasonNumber` query params (400 on missing/invalid)
// then emits `[]`. When GetBySeason lands, swap the body to a real
// service call without rewiring.
//
// J270.
func SeasonHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seriesIDStr := r.URL.Query().Get("seriesId")
		if seriesIDStr == "" {
			httperr.BadRequest(w, "history/season: missing seriesId")
			return
		}
		if id, err := strconv.Atoi(seriesIDStr); err != nil || id <= 0 {
			httperr.BadRequest(w, "history/season: invalid seriesId")
			return
		}
		seasonStr := r.URL.Query().Get("seasonNumber")
		if seasonStr == "" {
			httperr.BadRequest(w, "history/season: missing seasonNumber")
			return
		}
		if _, err := strconv.Atoi(seasonStr); err != nil {
			httperr.BadRequest(w, "history/season: invalid seasonNumber")
			return
		}
		writeEmptyHistoryList(w)
	})
}

// EpisodeHandler returns GET /api/v5/history/episode. Mirrors
// `HistoryController.GetEpisodeHistory` at `:132-146`:
// historyService.GetByEpisode(episodeId, eventType).
//
// **Empty-corpus stub** — same deferral as SeasonHandler. Validates
// the `episodeId` query param (400 on missing/invalid) then emits
// `[]`.
//
// J270.
func EpisodeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idStr := r.URL.Query().Get("episodeId")
		if idStr == "" {
			httperr.BadRequest(w, "history/episode: missing episodeId")
			return
		}
		if id, err := strconv.Atoi(idStr); err != nil || id <= 0 {
			httperr.BadRequest(w, "history/episode: invalid episodeId")
			return
		}
		writeEmptyHistoryList(w)
	})
}

// ----- status-rewriting wrapper -----

// MarkAsFailedHandler returns POST /api/v5/history/failed/{id}. v3
// emits 200 + `{}`; v5 emits 204 + empty (TypedResults.NoContent).
// Wraps v3 via v5util.RewriteSuccessTo204.
//
// J270.
func MarkAsFailedHandler(svc FailedDownloadMarker) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.HistoryMarkAsFailedHandler(svc))
}

// ----- private helpers -----

func writeEmptyHistoryList(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("[]"))
}
