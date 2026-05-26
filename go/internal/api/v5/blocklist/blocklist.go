// Package blocklist is the v5 wire surface for /api/v5/blocklist.
//
// The v5 controller (src/Sonarr.Api.V5/Blocklist/BlocklistController.cs)
// is at 127 LOC of C# source with 3 endpoints:
//
//	v5 endpoint                    vs v3
//	GET    /api/v5/blocklist           200   same (paged)
//	DELETE /api/v5/blocklist/{id}      **204** (v3 emits 200 + `{}`)
//	DELETE /api/v5/blocklist/bulk      **204** (v3 emits 200 + `{}`)
//
// Body shape on the GET response: the C# v5 `BlocklistResource` has
// 13 fields (id / seriesId / episodeIds / sourceTitle / languages /
// quality / customFormats / date / protocol / indexer / message /
// source / series). The Go-port v3 handler emits a DELIBERATELY
// REDUCED 5-field shape (id / seriesId / title / date / sourceTitle)
// — see `internal/api/v3/blocklist.go:18-23` doc-comment:
//
//	"DEFERRED — STATUS line 9 carries a note"
//
// The deferral chain (same one Parse + Update inherit at J258/J259):
// the custom-format CALCULATOR isn't ported yet, so the
// CustomFormats + CustomFormatScore fields can't be computed; the
// Quality model lookup + language list + Series projection are
// individually doable but not useful without the full record. When
// the calculator port lands, both v3 AND v5 Resources widen.
//
// For this slice, v5 inherits the v3 Go-port's reduced shape via
// Resource alias. Path-alias the GET; wrap the two DELETEs via
// v5util.RewriteSuccessTo204 (per the J257/J260 pattern for
// 200+`{}`→204 demotion).
//
// Wave 3 v5 endpoint family port — 127 LOC C# source. J261.
package blocklist

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is an alias of the v3 wire shape. v3 Go-port + v5 Go-port
// both emit the reduced 5-field shape pending the calculator port
// (see package doc).
type Resource = apiv3.BlocklistResource

// PagedLister is an alias of the v3 read interface.
type PagedLister = apiv3.BlocklistPagedLister

// Writer is an alias of the v3 write interface (Delete + DeleteByIDs).
type Writer = apiv3.BlocklistWriter

// GetHandler returns GET /api/v5/blocklist. Path-alias to v3 — both
// emit the v3-Go-port-reduced shape; widens when calculator lands.
var GetHandler = apiv3.BlocklistHandler

// DeleteHandler returns DELETE /api/v5/blocklist/{id}.
//
// v3 emits 200 + `{}`; v5 emits 204 + empty (TypedResults.NoContent).
// Wraps v3 via v5util.RewriteSuccessTo204.
//
// J261.
func DeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.BlocklistDeleteHandler(svc))
}

// BulkDeleteHandler returns DELETE /api/v5/blocklist/bulk.
//
// v3 emits 200 + `{}`; v5 emits 204 + empty. Same wrapper.
//
// J261.
func BulkDeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.BlocklistBulkDeleteHandler(svc))
}
