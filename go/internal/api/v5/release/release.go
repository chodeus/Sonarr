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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/releasecache"
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

// OverrideResource mirrors C# `OverrideReleaseResource` at
// `src/Sonarr.Api.V5/Release/ReleaseGrabResource.cs:14-21`. The
// override sub-block of the v5 grab body — when populated, the
// handler swaps Subject.Series / Episodes / Quality / Languages
// before dispatch (matches C# `ReleaseController.cs:101-110`).
type OverrideResource struct {
	SeriesID         *int            `json:"seriesId,omitempty"`
	EpisodeIDs       []int           `json:"episodeIds"`
	DownloadClientID *int            `json:"downloadClientId,omitempty"`
	Quality          json.RawMessage `json:"quality,omitempty"`
	Languages        json.RawMessage `json:"languages,omitempty"`
}

// SearchInfoResource mirrors C# `SearchInfoResource` at
// `src/Sonarr.Api.V5/Release/ReleaseGrabResource.cs:23-28`. The
// fallback lookup used when the cached Subject has no Series /
// Episodes attached (matches C# `ReleaseController.cs:135-159`).
//
// **DEFERRED on the Go-port**: today's cached Decision always carries
// a populated Series + Episodes via the decision-engine pipeline, so
// the SearchInfo fallback branch is unreachable in practice. The
// field is decoded for wire-shape parity (so a v5 client sending
// `searchInfo` doesn't get rejected by `DisallowUnknownFields`) but
// the populated branch lookup is gated on a future
// parsingService.GetEpisodes port. When that port lands, the lookup
// surfaces via a new interface dependency on the handler.
type SearchInfoResource struct {
	SeriesID     *int `json:"seriesId,omitempty"`
	SeasonNumber *int `json:"seasonNumber,omitempty"`
	EpisodeID    *int `json:"episodeId,omitempty"`
}

// GrabResource is the v5 POST body shape — mirror of C#
// `ReleaseGrabResource` at
// `src/Sonarr.Api.V5/Release/ReleaseGrabResource.cs:6-12`. Slimmer
// than v3's `ReleaseDownloadBody` (which flattens override-info on
// the root); v5 groups override under `override` + search-info
// under `searchInfo`. The wire JSON contract differs — this struct
// is the genuine v5-NEW body type with no v3 equivalent.
type GrabResource struct {
	Guid       string              `json:"guid"`
	IndexerID  int                 `json:"indexerId"`
	Override   *OverrideResource   `json:"override,omitempty"`
	SearchInfo *SearchInfoResource `json:"searchInfo,omitempty"`
}

// DownloadHandler returns POST /api/v5/release.
//
// Mirrors `ReleaseController.DownloadRelease` at
// `src/Sonarr.Api.V5/Release/ReleaseController.cs:86-188`:
// `Ok<ReleaseGrabResource>` (200 + the request body verbatim) on
// successful dispatch; `NotFound` when the cache misses or the
// override's series/episode lookups don't resolve; 400 on
// validation failure (incomplete override).
//
// Strategy: body-translation adapter. The v5 wire shape diverges
// from v3's flattened `ReleaseDownloadBody`, but the underlying
// orchestration (cache lookup → ShouldOverride apply → dispatch)
// is byte-identical. This handler decodes the v5 body, validates,
// translates to a v3 `ReleaseDownloadBody`, runs the v3 handler
// via a BufferingWriter to capture the result, then either writes
// the v5 body back verbatim on 200 success or passes through the
// v3 error envelope.
//
// The v5 `searchInfo` fallback branch (C# lines 133-159 — used when
// the cached Subject has no Series attached) is DEFERRED on the
// Go-port: today's cached Decision always carries populated Series
// + Episodes via the decision-engine pipeline, making the fallback
// unreachable. When `parsingService.GetEpisodes` ports in a future
// slice, the SearchInfo arm wires through a new handler dependency.
//
// J308 — Wave 3 LARGE-family arc final residual (Release POST). The
// arc DRAINS at this slice.
func DownloadHandler(
	cache *releasecache.Cache,
	dispatcher apiv3.ReleaseDispatcher,
	seriesLookup apiv3.ReleaseOverrideSeriesLookup,
	episodeLookup apiv3.ReleaseOverrideEpisodeLookup,
) http.Handler {
	v3Handler := apiv3.ReleaseDownloadHandler(cache, dispatcher, seriesLookup, episodeLookup)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body GrabResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("release: malformed body: %v", err))
			return
		}
		// Translate v5 body → v3 body, then re-encode for the v3
		// handler's `DisallowUnknownFields` decoder. Field-by-field
		// mapping per C# `ReleaseController.cs:101-110`:
		//   - guid / indexerId — direct copy.
		//   - override != nil → ShouldOverride=true + copy
		//     override.SeriesID / EpisodeIDs / Quality / Languages.
		//     downloadClientId surfaces on a deferred sub-arc; the
		//     v3 ReleaseDownloadBody has DownloadClientID + DownloadClient
		//     fields but the C# v5 handler at line 179 reads it ONLY
		//     for the post-dispatch `release.Override?.DownloadClientId`
		//     reference — which the Go-port v3 handler doesn't yet
		//     consume. Pass through for wire-shape parity.
		v3Body := apiv3.ReleaseDownloadBody{
			Guid:      body.Guid,
			IndexerID: body.IndexerID,
		}
		if body.Override != nil {
			v3Body.ShouldOverride = true
			if body.Override.SeriesID != nil {
				v3Body.SeriesID = *body.Override.SeriesID
			}
			v3Body.EpisodeIDs = body.Override.EpisodeIDs
			if body.Override.DownloadClientID != nil {
				v3Body.DownloadClientID = *body.Override.DownloadClientID
			}
			v3Body.Quality = body.Override.Quality
			v3Body.Languages = body.Override.Languages
		}
		// Re-encode for the v3 handler. The v3 handler decodes via
		// json.NewDecoder + DisallowUnknownFields, so we need a
		// faithful JSON byte stream — including the omitted fields
		// the v3 decoder expects to be absent (DownloadClient is
		// `string` — empty string is fine; the v3 PostValidator
		// only checks Guid + IndexerID).
		v3JSON, err := json.Marshal(v3Body)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("release: encode v3 body: %w", err), nil)
			return
		}
		// Build a synthetic request with the translated body and
		// invoke the v3 handler against a BufferingWriter. The
		// v3 handler emits 200 + the v3 body on success, or
		// 4xx/5xx with an error envelope on failure.
		v3Req := r.Clone(r.Context())
		v3Req.Body = io.NopCloser(bytes.NewReader(v3JSON))
		v3Req.ContentLength = int64(len(v3JSON))
		v3Req.Header = r.Header.Clone()
		rec := v5util.NewBufferingWriter()
		v3Handler.ServeHTTP(rec, v3Req)

		// Forward headers (Content-Type already set by v3 handler).
		v5util.CopyHeaders(w.Header(), rec.Header())

		if rec.Status() == http.StatusOK {
			// Success: discard the v3 echo body, write the v5 body
			// verbatim (matches C# `return TypedResults.Ok(release)`
			// at ReleaseController.cs:187).
			v5JSON, mErr := json.Marshal(body)
			if mErr != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("release: encode v5 body: %w", mErr), nil)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			// Update Content-Length if the v3 wrote one (the
			// BufferingWriter captures whatever the v3 handler set).
			w.Header().Del("Content-Length")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(v5JSON)
			return
		}
		// Failure path — forward the v3 status + envelope verbatim.
		// 400 (validation), 404 (cache miss), 500 (lookup / encode)
		// all pass through unchanged.
		w.WriteHeader(rec.Status())
		_, _ = w.Write(rec.Body())
	})
}
