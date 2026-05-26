// Package update is the v5 wire surface for /api/v5/update.
//
// The v5 controller (src/Sonarr.Api.V5/Update/UpdateController.cs)
// is a single GET endpoint at 121 LOC of C# source. Mirrors v3
// line-for-line on wire — same `UpdateResource` shape per openapi v5
// (id / version / branch / releaseDate / fileName / url / installed /
// installedOn / installable / latest / changes / hash), same 200
// status via `TypedResults.Ok<List<UpdateResource>>`. No bulk
// endpoint, no DELETE, no status-code tightening.
//
// Pure path-alias slice — same pattern as J251 (DiskSpace) and J252
// (FileSystem).
//
// Wave 3 v5 endpoint family port — 121 LOC C# source. J259.
package update

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 wire shape (unchanged in v5).
type Resource = apiv3.UpdateResource

// Changes is an alias of the v3 inner wire shape (unchanged in v5).
type Changes = apiv3.UpdateChanges

// Lister is an alias of the v3 read interface.
type Lister = apiv3.UpdateLister

// GetHandler returns GET /api/v5/update. Same wire shape as v3 —
// `apiv3.UpdateHandler` is re-exported verbatim. The v3 handler
// already emits the OrderBy-Version-DESC sort + the `installed` /
// `installable` / `latest` flag computation that the v5 C# code
// duplicates from the v3 base; no v5 wrapper required.
var GetHandler = apiv3.UpdateHandler
