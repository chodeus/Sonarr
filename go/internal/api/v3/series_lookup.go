package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesSearcher is the metadata-source search the lookup endpoint calls.
// *metadatasource.Proxy satisfies it (skyhook SearchForNewSeries). nil →
// the handler returns an empty list (the pre-G6 stub posture).
type SeriesSearcher interface {
	SearchForNewSeries(ctx context.Context, term string) ([]*series.Series, error)
}

// SeriesLookupHandler returns GET /api/v3/series/lookup. Mirrors C#
// `SeriesLookupController.Search` (`src/Sonarr.Api.V3/Series/
// SeriesLookupController.cs:26-31`): `_searchProxy.SearchForNewSeries(term)`
// → map each result to SeriesResource. G6 (J426) wires the real
// metadatasource proxy (was a hardcoded `[]` stub). Cover URLs stay
// remote (lookup results aren't in the library, so no MapCoversToLocal);
// the C# folder-name preview + statistics placeholder are DEFERRED (the
// React add-series flow derives the folder client-side from rootFolder +
// title).
//
// Required: `term` query → 400 if missing. nil searcher → `[]` (200).
// Search error → 500.
func SeriesLookupHandler(searcher SeriesSearcher) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		term := r.URL.Query().Get("term")
		if term == "" {
			httperr.BadRequest(w, "series/lookup: term is required")
			return
		}
		out := []SeriesResource{}
		if searcher != nil {
			results, err := searcher.SearchForNewSeries(r.Context(), term)
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/lookup: %w", err), nil)
				return
			}
			for _, s := range results {
				if s == nil {
					continue
				}
				out = append(out, ToSeriesResource(*s))
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}
