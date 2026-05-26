// Package diskspace is the v5 wire surface for /api/v5/diskspace.
//
// The v5 controller (src/Sonarr.Api.V5/DiskSpace/DiskSpaceController.cs)
// mirrors v3 line-for-line on the wire shape — same single GET
// endpoint, same `DiskSpaceResource` shape (path / label / freeSpace /
// totalSpace), same 200 status code via .NET 8 `TypedResults.Ok`.
// There is NO bulk endpoint, NO DELETE endpoint, NO status-code
// tightening on this family.
//
// All routes re-export the v3 handler. Type aliases keep the package
// importable as a stable v5 namespace for future divergence (e.g. if
// v5 ever adds the C# "Optional" mount-table walk that v3 defers).
//
// Wave 3 v5 endpoint family port — smallest family in the v5 surface
// (50 LOC C# source). J251.
package diskspace

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 wire shape (unchanged in v5).
type Resource = apiv3.DiskSpaceResource

// FreeSpaceLister is an alias of the v3 read-surface interface
// (unchanged in v5).
type FreeSpaceLister = apiv3.FreeSpaceLister

// GetHandler returns GET /api/v5/diskspace. Same wire shape as v3 —
// `apiv3.DiskSpaceHandler` is re-exported verbatim. The v3 handler
// already emits the OrderBy-Path-OrdinalIgnoreCase ordering + the
// empty-list `[]` (not `null`) shape both versions need; no v5
// wrapper required.
var GetHandler = apiv3.DiskSpaceHandler
