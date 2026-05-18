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
// The Go-side wanted service hasn't ported yet — for fresh-DB
// byte-eq the stub `emptyWantedLister` in cmd/sonarr/host_wiring.go
// always returns 0 records, which matches the recorded empty-DB
// goldens. Populated-row mapping awaits the wanted-service port
// (DEFERRED — STATUS line 9 carries the follow-up note).
type WantedEpisodeLister interface {
	PagedWanted(ctx context.Context, req PagingRequest, scope WantedScope) ([]EpisodeResource, int, error)
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
// The C# controllers also accept includeSeries / includeEpisodeFile /
// monitored / sortKey overrides; only the defaults are exercised by
// the recorded corpus today, so per-record richness is DEFERRED.
func WantedHandler(lister WantedEpisodeLister, scope WantedScope) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "episodes.airDateUtc")
		records, total, err := lister.PagedWanted(r.Context(), req, scope)
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
