// Package serieslookup is the v5 wire surface for
// /api/v5/series/lookup — the metadata-source search endpoint that
// the React UI's "add new series" wizard hits to populate the
// pickable-series list from a user-typed term.
//
// Source: src/Sonarr.Api.V5/Series/SeriesLookupController.cs (58 LOC).
// Single GET endpoint that takes `?term=` and returns the search
// results as List<SeriesResource> with cover-URL rewrites, folder-
// name preview, and IsExcluded import-list-exclusion check.
//
// Pure path-alias slice — same J286/J287 minimum-effort pattern. v3
// already exposes the same endpoint at /api/v3/series/lookup via
// apiv3.SeriesLookupHandler with byte-identical wire shape (200 +
// `[]` deferred stub today). When the metadatasource Proxy
// integration lands (the SearchForNewSeries-result pipeline is
// already wired, only the cover-URL rewrite + folder-name preview
// + IsExcluded check are pending), both v3 and v5 surfaces widen
// via the shared handler.
//
// Status codes: GET → 200 + `[]` (deferred stub). 400 on missing
// `term` query.
package serieslookup

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// GetHandler returns GET /api/v5/series/lookup. Same wire shape +
// status codes as v3 — re-exported.
var GetHandler = apiv3.SeriesLookupHandler
