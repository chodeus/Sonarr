package v3

import (
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
)

// SeriesLookupHandler returns GET /api/v3/series/lookup. Mirrors C#
// `SeriesLookupController.Search` at
// `src/Sonarr.Api.V3/Series/SeriesLookupController.cs:26-31` which
// calls `_searchProxy.SearchForNewSeries(term)` + maps the metadata-
// source results to SeriesResource (with cover-URL rewrite + folder-
// name preview + statistics placeholder).
//
// DEFERRED with rationale: the search-proxy integration. The
// metadatasource.Proxy ports (slice 4.AS - 4.AW) wired
// SearchForNewSeries but the cover-URL rewrite + folder-name
// builder integration with the SearchForNewSeries result pipeline
// hasn't ported. Today returns `[]` matching the "no results"
// wire shape so the React UI's "add new series" search box gets
// 200 instead of 404.
//
// Required: `term` query. Missing term -> 400.
func SeriesLookupHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("term") == "" {
			httperr.BadRequest(w, "series/lookup: term is required")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
}
