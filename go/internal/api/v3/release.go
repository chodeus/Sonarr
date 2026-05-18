package v3

import (
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
)

// ReleaseHandler returns GET /api/v3/release. Mirrors C#
// `ReleaseController.GetReleases` at `ReleaseController.cs:171-186`
// which dispatches on episodeId / (seriesId + seasonNumber) / RSS-feed
// branches.
//
// DEFERRED with rationale: the actual indexer-driven search itself
// (the C# `EpisodeSearchService.GetEpisodeRss` / `Season` / `Episode`
// implementations). The Go port has indexers + decision-engine pieces
// scattered (slice 3.AK + 3.AL + 4.A) but the orchestration driver
// hasn't been assembled yet. Today this handler returns `[]`
// matching the empty-DB / no-indexers-configured wire shape so the
// React UI's manual-search button gets a 200 instead of 404.
//
// Query parameters are validated but otherwise ignored.
func ReleaseHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// Validate any int-typed query params if present.
		for _, key := range []string{"seriesId", "episodeId", "seasonNumber"} {
			if v := q.Get(key); v != "" {
				if _, err := strconv.Atoi(v); err != nil {
					httperr.BadRequest(w, "release: invalid "+key)
					return
				}
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
}
