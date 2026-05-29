// Package wanted is the v5 wire surface for /api/v5/wanted/missing
// and /api/v5/wanted/cutoff (+ /{id} variants).
//
// The v5 controllers
// (src/Sonarr.Api.V5/Wanted/MissingController.cs +
// src/Sonarr.Api.V5/Wanted/CutoffController.cs) total ~136 LOC of C#
// source. Both inherit from EpisodeControllerWithSignalR, which in
// turn inherits RestControllerWithSignalR — same as v3.
//
// Wire-contract diff vs v3 (per `src/Sonarr.Api.V5/openapi.json`
// :1237-1410 + :4117-4222):
//
//  1. **Status / envelope / Resource: IDENTICAL.** Both versions
//     return 200 OK + `PagingResource<EpisodeResource>` envelope for
//     the list endpoints and a single `EpisodeResource` (or 404) for
//     the `/{id}` endpoints. Sort defaults match: sortKey
//     `episodes.airDateUtc`, sortDirection Ascending (v3 +
//     v5 — `MapToPagingSpec(..., "episodes.airDateUtc", SortDirection.Ascending)`).
//
//  2. **Query-param SHAPE differs.** v3 takes three independent
//     bools — `includeSeries` / `includeImages` /
//     `includeEpisodeFile` (cutoff also). v5 collapses to one CSV
//     enum array `includeSubresources` of `MissingSubresource` (Series,
//     Images) or `CutoffSubresource` (Series, EpisodeFile, Images).
//     v5 also adds `includeSpecials` (default true) on missing only.
//
//     This divergence is INVISIBLE at the empty-corpus level — the
//     v3 Go-port stub (`emptyWantedLister` + `wantedRepoLister`)
//     ignores all sub-resource flags and emits the base
//     EpisodeResource projection regardless. Both v3 and v5 wires
//     therefore yield the same response on the recorded corpus. When
//     sub-resource hydration is ported (Wave 1 deferred —
//     custom-format calculator + Series/EpisodeFile join), v5 will
//     need a `includeSubresources` CSV→bool projection helper; the
//     C# v5 controller's `includeSubresources.Contains(...)`
//     translates directly. DEFERRED.
//
//  3. **EpisodeResource shape widens in v5.** v5 adds `EndTime?` +
//     `GrabDate?` (per `src/Sonarr.Api.V5/Episodes/EpisodeResource.cs`
//     vs v3); v5 also marks nullable strings explicitly. At the
//     empty-corpus level (records: []) the envelope shape is
//     identical, so path-alias is safe. Populated-row v5 widening
//     is part of the same Wave 1 deferred chain as J258 (Parse) /
//     J259 (Update unaffected) / J261 (Blocklist) — once
//     custom-format calculator lands, v5 EpisodeResource gets its
//     own projector and this package switches from alias to v5-
//     specific. Until then path-alias matches the recorded golden.
//
// Pure path-alias slice — same pattern as J251 (DiskSpace), J252
// (FileSystem), J259 (Update).
//
// Wave 3 v5 endpoint family port — ~136 LOC C# source. J264.
package wanted

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource is an alias of the v3 wire shape.
//
// At the empty-DB / no-subresource-hydration corpus level this
// matches the v5 openapi `EpisodeResource` envelope. Populated-row
// widening (EndTime/GrabDate) deferred per the package doc above.
type Resource = apiv3.EpisodeResource

// Scope distinguishes the missing vs cutoff dataset — alias of the
// v3 discriminant so the v5 host wiring can re-use the same handler
// factory.
type Scope = apiv3.WantedScope

const (
	// ScopeMissing — `WantedMissing` in v3 / `IEpisodeService.EpisodesWithoutFiles` in C# v5.
	ScopeMissing = apiv3.WantedMissing
	// ScopeCutoff — `WantedCutoff` in v3 / `IEpisodeCutoffService.EpisodesWhereCutoffUnmet` in C# v5.
	ScopeCutoff = apiv3.WantedCutoff
)

// EpisodeLister is an alias of the v3 read interface.
type EpisodeLister = apiv3.WantedEpisodeLister

// EpisodeByIDGetter is the by-ID read interface — re-exported from
// v3. Backs `ByIDHandler`.
type EpisodeByIDGetter = apiv3.EpisodeByIDGetter

// wantedMissingMapping / wantedCutoffMapping map the v5 Missing/Cutoff
// Subresource enum tokens onto the v3 include-flag query keys. They
// differ: MissingSubresource is {Series, Images}; CutoffSubresource is
// {Series, EpisodeFile, Images}.
var wantedMissingMapping = map[string]string{
	"series": "includeSeries",
	"images": "includeImages",
}

var wantedCutoffMapping = map[string]string{
	"series":      "includeSeries",
	"episodefile": "includeEpisodeFile",
	"images":      "includeImages",
}

// ListHandler returns GET /api/v5/wanted/{missing,cutoff}. Same
// envelope/status/defaults as v3, translating the v5
// `?includeSubresources=…` enum-array param onto v3's include bools
// (J418). The token→flag mapping is scope-dependent (cutoff adds
// episodeFile).
func ListHandler(lister apiv3.WantedEpisodeLister, scope Scope) http.Handler {
	mapping := wantedMissingMapping
	if scope == apiv3.WantedCutoff {
		mapping = wantedCutoffMapping
	}
	return v5util.TranslateIncludeSubresources(apiv3.WantedHandler(lister, scope), mapping)
}

// ByIDHandler returns GET /api/v5/wanted/{missing,cutoff}/{id}. The
// v3 + v5 inherit GetResourceById from RestController — both 200 +
// EpisodeResource on found, 404 on miss. Re-exported verbatim.
var ByIDHandler = apiv3.EpisodeByIDHandler
