package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
)

// WantedEpisodeLister is the read side both wanted endpoints depend
// on. Mirrors the C# `IWantedEpisodeService` (Missing/Cutoff variants).
// The production lister (`wantedRepoLister` in cmd/sonarr) hydrates real
// rows; the contract fixtures return 0 records for fresh-DB byte-eq.
//
// J383: the includeSeries / includeImages flags are threaded INTO the
// interface (like the J380 paged-History lister) so the production lister
// can hydrate the BASE Series embed + the Images projection CONDITIONALLY
// — no per-row series lookup when the client didn't ask. Mirrors C#
// EpisodeControllerWithSignalR.MapToResource(v, includeSeries, _,
// includeImages). includeEpisodeFile (Cutoff-only in C#) stays DEFERRED —
// it needs the heavier J340/J342 episodeFile populator hydration.
type WantedEpisodeLister interface {
	PagedWanted(ctx context.Context, req PagingRequest, scope WantedScope, includeSeries, includeImages bool) ([]EpisodeResource, int, error)
}

// WantedScope distinguishes the missing vs cutoff-not-met dataset.
// C# routes them via separate controllers/services; the Go port
// collapses to one handler factory + a scope discriminant since the
// envelope shape and parameter handling are identical.
type WantedScope int

const (
	// WantedMissing — `IWantedEpisodeService.Missing` in C#.
	WantedMissing WantedScope = iota
	// WantedCutoff — `IWantedEpisodeService.CutoffUnmet` in C#.
	WantedCutoff
)

// WantedHandler returns the http.Handler for both /api/v3/wanted/missing
// and /api/v3/wanted/cutoff. Mirrors `MissingController.GetMissing`
// and `CutoffController.GetCutoff` — both share PagingRequest with
// default sortKey "episodes.airDateUtc" and descending direction.
//
// The C# controllers accept includeSeries / includeEpisodeFile /
// includeImages / monitored / sortKey overrides. J383 parses
// includeSeries + includeImages and passes them to the lister so the
// BASE Series embed + Images projection hydrate conditionally (mirrors
// C# MapToResource). includeEpisodeFile + monitored + SQL-level sort
// remain DEFERRED.
func WantedHandler(lister WantedEpisodeLister, scope WantedScope) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "episodes.airDateUtc")
		includeSeries := r.URL.Query().Get("includeSeries") == "true"
		includeImages := r.URL.Query().Get("includeImages") == "true"
		records, total, err := lister.PagedWanted(r.Context(), req, scope, includeSeries, includeImages)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("wanted: %w", err), nil)
			return
		}
		if records == nil {
			records = []EpisodeResource{}
		}
		resp := PagingResource[EpisodeResource]{
			Page: req.Page, PageSize: req.PageSize,
			Records: records, SortDirection: req.SortDirection,
			SortKey: req.SortKey, TotalRecords: total,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(resp)
		_, _ = w.Write(buf)
	})
}
