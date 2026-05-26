// Package manualimport is the v5 wire surface for
// /api/v5/manualimport.
//
// The v5 family (src/Sonarr.Api.V5/ManualImport/) is 236 LOC of C#
// across 3 files — ManualImportController (114),
// ManualImportResource (95), ManualImportReprocessResource (27).
//
// Two endpoints:
//
//	v5 endpoint                  v5 status   vs v3 Go-port
//	GET  /api/v5/manualimport       200       same as /api/v3/manualimport
//	POST /api/v5/manualimport       200       same as /api/v3/manualimport
//
// **Wire-contract diffs vs v3**:
//
//  1. **ManualImportResource widens in v5.** v5 carries 20 fields
//     (path, relativePath, folderName, name, size, series, seasonNumber,
//     episodes[], episodeFileId, releaseGroup, quality, languages[],
//     qualityWeight, downloadId, customFormats[], customFormatScore,
//     indexerFlags, releaseType, rejections[]) plus inherited id.
//     The v3 Go-port handler currently emits `[]` for empty-corpus
//     (the production manual-import disk-walk + parser + decision-
//     engine pipeline isn't ported per `internal/api/v3/manual_import.go:26-32`).
//     Same Wave 1 deferred chain as J258/J259/J261/J264/J268/J270 —
//     when the import-decision pipeline + custom-format calculator
//     land, both v3 and v5 widen via the shared handler.
//
//  2. **ManualImportReprocessResource POST body widens in v5.** v5
//     adds `relativePath` (string?) to the 14-field v3 ReprocessItem.
//     The v3 POST handler uses `DisallowUnknownFields()` (per
//     `:150`), so a real v5 client sending `relativePath` would get
//     a 400. This is a known follow-up tied to the same calculator
//     chain — the empty-corpus contract-replay tests never POST a
//     `relativePath` field. When v5 frontend lands real reprocess
//     calls, the v3 decoder needs to relax + the reprocess logic
//     needs the relativePath fallback wired (`ManualImportController.cs:91-94`
//     `if (item.RelativePath.IsNotNullOrWhiteSpace()) processedItem.RelativePath = item.RelativePath`).
//     DEFERRED — annotated in the Wave 4 audit row.
//
//  3. **NEW v5 query param**: `filterExistingFiles` (default true) on
//     GET. v3 Go-port handler validates int params but ignores all
//     query params — invisible at empty-corpus, same chain.
//
// Pure path-alias slice for both endpoints at the empty-corpus
// level. Both endpoints share status (200) + envelope shape (`[]`
// for GET; `[items...]` for POST).
//
// Wave 3 v5 endpoint family port — 236 LOC C# source. J271.
package manualimport

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// (No `Resource` type alias — v3 GET handler emits `[]` from a stub
// and doesn't declare a per-row struct yet. When the manual-import
// service port lands, both v3 + v5 surfaces will gain a shared
// Resource type aliased here.)

// ReprocessItem is an alias of the v3 POST body shape.
type ReprocessItem = apiv3.ManualImportReprocessItem

// SeriesLookup is an alias of the v3 series-by-id lookup interface.
type SeriesLookup = apiv3.ManualImportSeriesLookup

// EpisodeLookup is an alias of the v3 episode-resolution lookup
// interface.
type EpisodeLookup = apiv3.ManualImportEpisodeLookup

// ----- handlers (both path-aliases) -----

// GetHandler returns GET /api/v5/manualimport. Same status + envelope
// as v3 — v3 stub emits `[]` for empty corpus; populated-row mapping
// awaits the manual-import service port.
var GetHandler = apiv3.ManualImportHandler

// ReprocessHandler returns POST /api/v5/manualimport. Same status
// (200) + envelope as v3 — empty-corpus replay never exercises the
// `relativePath` widening so path-alias is byte-equal for the
// recorded corpus. Real v5 clients sending `relativePath` would
// currently hit v3's DisallowUnknownFields 400 — see package doc.
var ReprocessHandler = apiv3.ManualImportReprocessHandler
