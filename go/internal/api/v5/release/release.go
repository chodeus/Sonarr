// Package release is the v5 wire surface for /api/v5/release.
//
// Source: src/Sonarr.Api.V5/Release/ReleaseController.cs (343 LOC).
// Two endpoints on the v5 ReleaseController:
//
//	v5 endpoint                    Status   vs v3                   slice
//	GET    /api/v5/release         200      same — variable-alias   J305 (this slice)
//	POST   /api/v5/release         200      **WIRE-SHAPE DIVERGENT** J306 (queued)
//
// Plus the sibling ReleasePushController which routes under
// /api/v5/release/push — already ported at J294 in package
// internal/api/v5/releasepush (POST /push + GET /push/{id}).
//
// Why POST is deferred: v3 accepts a `ReleaseResource`-shaped body
// (~50 fields, with override fields flattened on the root); v5
// accepts a slimmer `ReleaseGrabResource` body with the override
// fields grouped under a nested `override` object + a nested
// `searchInfo` object. The grouping changes the JSON contract on
// the wire — not a status divergence but a *body* divergence. v5
// also requires a new translation step from `ReleaseGrabResource`
// into the v3 dispatcher inputs. New v5-specific handler code per
// the loop-scope sub-slicing guidance ("New v5-specific handler
// code last").
//
// GET /api/v5/release maps cleanly to v3 — the C# v5 endpoint at
// `ReleaseController.cs:190-205` is byte-for-byte the same wire
// shape as v3 (`Ok<List<ReleaseResource>>` with the same
// `seriesId? + episodeId? + seasonNumber?` query-param triple). The
// underlying `ReleaseResource` projection is shared via the apiv3
// type alias.
//
// Wave 3 LARGE-family v5 port — Release family sub-slice 1 of 2
// (GET; POST queued at J306). J305.
package release

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is the v5 ReleaseResource — variable-aliased to v3 at
// the declared-shape level. Per-decision projection is shared via
// the same `toReleaseResource` mapper at internal/api/v3/release.go.
// Field divergences between v3 and v5 (e.g. the v5
// `ReleaseHistoryResource` sub-shape) are unobservable today: the
// empty-DB Go-port returns zero decisions in the deferred branch
// catalogue, and the populated branch is a separate service-port
// arc (downloadDecisionService is wired but the RSS-feed branch
// still stubs at the empty-array level per the J255-era handler
// doc-comment).
type Resource = apiv3.ReleaseResource

// Searcher re-exports v3's read-side searcher interface (the
// IndexerSearch service satisfies it).
type Searcher = apiv3.ReleaseSearcher

// ListHandler aliases v3's GET /api/v3/release. Same wire shape +
// status (200/400/500). `seriesId` / `episodeId` / `seasonNumber`
// query params route through unchanged; the three-branch dispatch
// (episode-search / season-search / rss) matches C# v5's
// `GetReleases` switch at ReleaseController.cs:192-205.
var ListHandler = apiv3.ReleaseHandler
