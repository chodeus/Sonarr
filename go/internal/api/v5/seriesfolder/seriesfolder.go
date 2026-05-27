// Package seriesfolder is the v5 wire surface for
// /api/v5/series/{id}/folder. Returns the computed series folder
// name (per the naming config) without writing anything to disk.
//
// Source: src/Sonarr.Api.V5/Series/SeriesFolderController.cs
// (34 LOC). Single GET endpoint returning `{folder: "..."}`.
//
// The C# v5 controller is a direct port of v3:
//
//	var series = _seriesService.GetSeries(id);
//	var folder = _fileNameBuilder.GetSeriesFolder(series);
//	return TypedResults.Ok<object>(new { folder });
//
// Status + body shape match v3 — 200 OK + `{"folder":"..."}` happy
// path; 400 on invalid id; 404 on missing series; 500 on naming
// config read failure. Pure path-alias.
package seriesfolder

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// SeriesGetter is the read-side interface (lookup the Series row).
// Aliased from v3.
type SeriesGetter = apiv3.SeriesFolderGetter

// NamingReader is the read-side interface for the naming config
// the folder builder needs. Aliased from v3.
type NamingReader = apiv3.NamingConfigReader

// GetHandler returns GET /api/v5/series/{id}/folder. Same wire shape
// + status codes as v3 — re-exported.
var GetHandler = apiv3.SeriesFolderHandler
