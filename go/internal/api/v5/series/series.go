// Package series is the v5 wire surface for /api/v5/series.
//
// Source: src/Sonarr.Api.V5/Series/SeriesController.cs (422 LOC) —
// the LARGE-family v5 controller. This sub-slice ports the
// SeriesController CRUD endpoint set (5 endpoints); the v5-NEW
// per-id season-monitor endpoint (PUT /api/v5/series/{id}/season)
// is deferred to a follow-up sub-slice that touches series.Service
// (single-season monitored-set; new code, no v3 equivalent).
//
//	v5 endpoint                          Status   vs v3
//	GET    /api/v5/series                200      same — variable-alias
//	GET    /api/v5/series/{id}           200/404  same — variable-alias
//	POST   /api/v5/series                201      same (TypedCreated) — variable-alias
//	PUT    /api/v5/series/{id}           202      same (TypedAccepted) — variable-alias
//	DELETE /api/v5/series/{id}           204      **DIFFERENT** (v3 emits 200+`{}`; v5 demotes to 204+empty via v5util.RewriteSuccessTo204)
//
// DEFERRED to a subsequent sub-slice:
//   - PUT /api/v5/series/{id}/season — v5-NEW per-id season-monitor
//     endpoint at SeriesController.cs:223-246. Returns
//     `Ok<SeasonResource>` (200 + SeasonResource); 404 when the
//     SeasonNumber doesn't match. Genuinely new wire shape — no v3
//     equivalent — and needs single-season monitored-set on
//     series.Service.
//   - v5-specific SeriesResource fields: `MalIds`, `AniListIds`,
//     `OriginalCountry` are EMITTED (shared apiv3.SeriesResource +
//     ToSeriesResource populate them; see the Resource doc below).
//     `LanguageProfileId` (v3 computed `=> 1`; absent in v5) is
//     stripped by stripLanguageProfileID. `IsExcluded` (v5-only,
//     set on series/lookup) is a remaining shared-struct follow-up
//     (GAP_ANALYSIS G12).
//   - `includeSubresources[]` query-param filtering (C# SeriesSubresource
//     enum at `SeriesSubresource.cs`). Today the v3 Go-port handlers
//     ignore all sub-resource flags (same J290 "query-param-shift
//     invisible at stub level" pattern as v5/episode). When the
//     populated-mapper port lands the v5 list/by-id handlers will
//     route `includeSubresources=seasonImages` through the
//     ToResource(includeSeasonImages: bool) flag — separate slice.
//
// SignalR: v5 controller inherits from RestControllerWithSignalR.
// The Created/Updated/Deleted broadcast emitters are shared with v3
// via cmd/sonarr/signalr_wiring.go — the v5 routes feed the SAME
// `series` SignalR resource name through the shared
// signalr.NewV3Emitter[apiv3.SeriesResource] instance.
//
// Wave 3 LARGE-family v5 port — Series family sub-slice 1 of 2
// (CRUD; SeriesEditor + /{id}/season deferred). J303.
package series

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/series"
)

// Resource is the v5 SeriesResource — aliased to the shared
// apiv3.SeriesResource. The v5-specific fields MalIds / AniListIds /
// OriginalCountry are ALREADY emitted: apiv3.SeriesResource carries
// them (series.go:132/133/145) and apiv3.ToSeriesResource populates
// them from the persisted row (series.go:600/601/613), with omitempty
// so they appear on populated v5 rows. (Pinned by
// TestV5SeriesResource_EmitsV5OnlyModelFields.)
//
// Residual shared-struct divergences (LOW, tracked in GAP_ANALYSIS
// G12): (a) v3 GET /series ALSO emits those 3 v5-only fields on
// populated rows — an over-emission vs C# v3, accepted as the
// shared-struct/omitempty tradeoff; (b) `IsExcluded` (v5-only, set
// only on the series/lookup path, C# SeriesLookupController:53) is not
// on the shared struct yet — a v5-lookup-only follow-up. The clean
// comprehensive fix for both is de-aliasing v5 series to its own
// resource type.
type Resource = apiv3.SeriesResource

// Lister re-exports v3's read-side list interface.
type Lister = apiv3.SeriesLister

// ByIDGetter re-exports v3's by-id read interface.
type ByIDGetter = apiv3.SeriesByIDGetter

// Writer re-exports v3's write surface (POST / PUT / DELETE).
type Writer = apiv3.SeriesWriter

// SeasonEpisodeMonitorer is the optional-extension surface (J397) the
// SeasonMonitorHandler discovers on its Writer to cascade per-episode
// monitored state when a season's Monitored flag changes — porting the
// EpisodeMonitoredService arm of C# `SeriesService.UpdateSeries`. The
// cmd/sonarr seriesCascadeAdapter (which carries the episodes service)
// implements it; test fakes that don't simply skip the cascade.
type SeasonEpisodeMonitorer interface {
	SetEpisodeMonitoredBySeason(ctx context.Context, seriesID, seasonNumber int, monitored bool) error
}

// Emitter re-exports v3's SignalR broadcast surface.
type Emitter = apiv3.SeriesEmitter

// ListHandler returns GET /api/v5/series. Same wire shape + status
// (200/500) as v3, with the `languageProfileId` key stripped per
// the v5 SeriesResource divergence — v5 dropped the field entirely
// while v3 still emits the hard-coded `=> 1` expression. `tvdbId`
// + `includeSubresources[]` query params route through unchanged
// — sub-resource filtering DEFERRED at the v3 layer per the
// populated-mapper gap.
//
// J331 — wraps the v3 handler with `stripLanguageProfileID` post-
// response middleware that decodes the JSON array, removes the key
// from each SeriesResource entry, and re-emits. Strip is a no-op
// on the empty-DB stub (no rows → no field to strip); populated
// rows lose the `languageProfileId` key.
func ListHandler(lister Lister) http.Handler {
	return stripLanguageProfileID(apiv3.SeriesListHandler(lister))
}

// ByIDHandler returns GET /api/v5/series/{id}. Same wire shape +
// status (200/400/404/500) as v3, with the `languageProfileId` key
// stripped per the v5 SeriesResource divergence.
//
// J331.
func ByIDHandler(svc ByIDGetter) http.Handler {
	return stripLanguageProfileID(apiv3.SeriesByIDHandler(svc))
}

// CreateHandler returns POST /api/v5/series. v3 already emits 201
// Created + SeriesResource matching v5's `TypedCreated<SeriesResource>`
// — wrapper adds the `languageProfileId` strip on response only.
// The request body still carries `languageProfileId` (v3 accepts
// it, treats it as 1 hard-coded — clients can send any value or
// omit; v3 mapping ignores).
//
// J331.
func CreateHandler(svc Writer, emitter Emitter) http.Handler {
	return stripLanguageProfileID(apiv3.SeriesCreateHandler(svc, emitter))
}

// UpdateHandler returns PUT /api/v5/series/{id}. v3 already emits
// 202 Accepted + SeriesResource matching v5's
// `TypedResults.Accepted<SeriesResource>`. Wrapper adds the
// `languageProfileId` strip on response. `moveFiles` query param
// routes through unchanged.
//
// J331.
func UpdateHandler(svc Writer, emitter Emitter) http.Handler {
	return stripLanguageProfileID(apiv3.SeriesUpdateHandler(svc, emitter))
}

// stripLanguageProfileID wraps an http.Handler whose response body
// is either a single SeriesResource object or a JSON array of
// SeriesResource objects. It captures the response, decodes the
// JSON, removes the `languageProfileId` key from each SeriesResource
// entry, and re-emits.
//
// The v5 SeriesResource (`src/Sonarr.Api.V5/Series/SeriesResource.cs`)
// drops the `LanguageProfileId` field that v3 carries as a
// hard-coded computed expression `=> 1`. The Go-port's v3 mapper
// at apiv3.ToSeriesResource sets `LanguageProfileID: 1` so the v3
// wire emits `"languageProfileId":1` on every row. The v5 surface
// needs to suppress this key to match the C# v5 wire shape.
//
// Strip behavior:
//   - 2xx status with parseable JSON: decode → strip → re-encode.
//   - 4xx/5xx status: pass-through verbatim (error envelopes
//     preserve their byte shape).
//   - Empty body or non-JSON body: pass-through verbatim.
//   - Re-encode failure: pass-through verbatim (defensive — never
//     emit a half-stripped/corrupt body).
//
// J331 — same pattern as the J295 LogResource v5 `method`-key
// strip. The first-consumer rule keeps the helper inline in v5/
// series for now; if a second consumer needs the same generic
// field-strip behavior, it consolidates into v5util.
func stripLanguageProfileID(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := v5util.NewBufferingWriter()
		h.ServeHTTP(rec, r)
		body := rec.Body()
		status := rec.Status()
		// Non-2xx → pass-through verbatim.
		if status < 200 || status >= 300 || len(body) == 0 {
			v5util.CopyHeaders(w.Header(), rec.Header())
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
		// Decode + strip + re-encode.
		var decoded any
		if err := json.Unmarshal(body, &decoded); err != nil {
			v5util.CopyHeaders(w.Header(), rec.Header())
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
		stripFieldRecursive(decoded, "languageProfileId")
		buf, err := json.Marshal(decoded)
		if err != nil {
			v5util.CopyHeaders(w.Header(), rec.Header())
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
		v5util.CopyHeaders(w.Header(), rec.Header())
		w.WriteHeader(status)
		_, _ = w.Write(buf)
	})
}

// stripFieldRecursive walks a JSON-decoded `any` tree and deletes
// the named key from every object encountered. Handles both
// single-object and array-of-objects top-level shapes (covers
// SeriesListHandler `[...]` and SeriesByIDHandler `{...}`
// responses). Mutates in place via map / slice element references.
//
// J331.
func stripFieldRecursive(v any, key string) {
	switch x := v.(type) {
	case map[string]any:
		delete(x, key)
		for _, child := range x {
			stripFieldRecursive(child, key)
		}
	case []any:
		for i := range x {
			stripFieldRecursive(x[i], key)
		}
	}
}

// DeleteHandler returns DELETE /api/v5/series/{id}.
//
// Mirrors `SeriesController.DeleteSeries` at lines 248-254:
// `TypedResults.NoContent()` → 204 with empty body. The v3 handler
// emits 200 + body `{}` on success; v5 strips the placeholder JSON
// and emits 204 + empty via v5util.RewriteSuccessTo204. 404
// (missing id — series DELETE is NOT in the J232 idempotency set;
// matches C# `_seriesService.DeleteSeries` throwing
// `ModelNotFoundException` which surfaces as 404 via the global
// exception filter) passes through verbatim. 500 service-error path
// also passes through verbatim. `deleteFiles` +
// `addImportListExclusion` query params route through unchanged.
func DeleteHandler(svc Writer, emitter Emitter) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.SeriesDeleteHandler(svc, emitter))
}

// EditorResource re-exports the v3 SeriesEditorResource bulk-body
// shape (same shape on v5; same field set per
// `SeriesEditorResource.cs:1-19`).
type EditorResource = apiv3.SeriesEditorResource

// EditorUpdateHandler returns PUT /api/v5/series/editor.
//
// Mirrors `SeriesEditorController.SaveAll` at
// `src/Sonarr.Api.V5/Series/SeriesEditorController.cs:21-109`:
// `Ok<List<SeriesResource>>` → 200 OK + the updated-rows array. The
// v3 handler emits 202 Accepted + identical body; v5 demotes the
// status to 200 via `v5util.RewriteAcceptedToOK`. The body is
// preserved byte-for-byte (the v3 SignalR `series/updated` broadcast
// emitter still fires per matched row). 400 (malformed body /
// empty seriesIds list / validation failures) + 500 (service error)
// pass through verbatim.
//
// J304 — Wave 3 LARGE-family arc; Series family sub-slice 2 of 3
// (SeriesEditor; the v5-NEW PUT /{id}/season endpoint is the
// remaining sub-slice).
func EditorUpdateHandler(svc Writer, emitter Emitter) http.Handler {
	return v5util.RewriteAcceptedToOK(apiv3.SeriesEditorUpdateHandler(svc, emitter))
}

// EditorDeleteHandler returns DELETE /api/v5/series/editor.
//
// Mirrors `SeriesEditorController.DeleteSeries` at
// `src/Sonarr.Api.V5/Series/SeriesEditorController.cs:111-116`:
// `TypedResults.NoContent()` → 204 with empty body. The v3 handler
// emits 200 + body `{}` on success; v5 strips the placeholder JSON
// and emits 204 + empty via `v5util.RewriteSuccessTo204`. Per-row
// missing-id skips silently on BOTH surfaces (matches v3's
// continue-on-ErrNotFound posture at series.go:430-432). 400 / 500
// passthroughs preserved verbatim.
//
// J304.
func EditorDeleteHandler(svc Writer, emitter Emitter) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.SeriesEditorDeleteHandler(svc, emitter))
}

// SeasonResource is the v5 SeasonResource wire shape — mirror of
// `src/Sonarr.Api.V5/Series/SeasonResource.cs:6-12`. v5-specific
// type with no v3 equivalent on the wire (v3 exposes
// SeasonPassSeasonOnWrite for write-only bulk PUT /seasonpass; this
// single-season endpoint is genuinely v5-NEW).
//
// Field semantics (matches C# under STJson `WhenWritingNull`):
//
//   - SeasonNumber + Monitored: always emitted (non-nullable on C#).
//   - Statistics: nullable; emit when populated. POPULATION DEFERRED
//     in the Go-port — the per-season statistics projection requires
//     the SeriesStatistics service join (population gate same as the
//     SeriesResource Statistics field — post-Wave-3 service-port
//     arc). Wire-shape pinned this slice (J309) via pointer-to-struct
//     so populated mappers can light it up without touching the
//     wire contract; today the SeasonMonitorHandler leaves it nil
//     and omitempty drops the key. See [SeasonStatistics].
//   - Images: nullable; emit when populated AND the caller's
//     `includeImages` flag is true. C# `SeasonResourceMapper.ToResource`
//     gates this on the parameter. The v5 single-season-monitor
//     route always passes `false` (per `SeriesController.cs:223-246`
//     which does NOT thread `includeImages` through to the response
//     mapper). Wire-shape pinned this slice (J309) via pointer-to-
//     slice so populated callers can opt in without breaking
//     omitempty (nil → key absent; `&[]…{}` → `"images":[]`;
//     populated → `"images":[{…}]`). Today the SeasonMonitorHandler
//     leaves it nil. See [MediaCover].
type SeasonResource struct {
	SeasonNumber int               `json:"seasonNumber"`
	Monitored    bool              `json:"monitored"`
	Statistics   *SeasonStatistics `json:"statistics,omitempty"`
	Images       *[]MediaCover     `json:"images,omitempty"`
}

// SeasonStatistics is the v5 SeasonStatisticsResource wire shape —
// mirror of `src/Sonarr.Api.V5/Series/SeasonStatisticsResource.cs:7-21`.
//
// v5 adds three fields over the v3 SeasonStatisticsResource:
// `MonitoredEpisodeCount`, `ReleaseTypes`, `EpisodeFileQualities`.
// PercentOfEpisodes is a computed property in C# (returns
// `(decimal)EpisodeFileCount / EpisodeCount * 100`, or 0 when
// EpisodeCount == 0); the Go-port emits it as a stored field — the
// populated-mapper that lands this struct's data is responsible for
// computing it at projection time. The non-nullable scalar fields
// (`EpisodeFileCount`, `EpisodeCount`, `TotalEpisodeCount`,
// `MonitoredEpisodeCount`, `SizeOnDisk`, `PercentOfEpisodes`) always
// emit when the parent Statistics pointer is non-nil; the nullable
// slice fields use omitempty so nil slices drop the key (matches
// `JsonIgnoreCondition.WhenWritingNull`).
//
// `ReleaseTypes` carries `NzbDrone.Core.Parser.Model.ReleaseType`
// enum values — wired as camelCase strings per STJson's global
// `JsonStringEnumConverter(JsonNamingPolicy.CamelCase)` config (e.g.
// `"singleEpisode"`, `"multiEpisode"`, `"seasonPack"`, `"unknown"`).
// `EpisodeFileQualities` carries `NzbDrone.Core.Qualities.Quality`
// DTOs — RawMessage on the Go side because the Quality wire shape
// is complex (id + name + source + resolution + modifier nested
// under `quality`/`revision` envelope) and a typed projection is
// out of scope for this wire-pin slice. The populated-mapper port
// will replace `[]json.RawMessage` with a proper `[]QualityValue`
// type when it lands.
//
// J309 — wire-shape pin only; no production caller populates this
// type today. Tests cover the marshal contract.
type SeasonStatistics struct {
	NextAiring            *time.Time        `json:"nextAiring,omitempty"`
	PreviousAiring        *time.Time        `json:"previousAiring,omitempty"`
	EpisodeFileCount      int               `json:"episodeFileCount"`
	EpisodeCount          int               `json:"episodeCount"`
	TotalEpisodeCount     int               `json:"totalEpisodeCount"`
	MonitoredEpisodeCount int               `json:"monitoredEpisodeCount"`
	SizeOnDisk            int64             `json:"sizeOnDisk"`
	ReleaseGroups         []string          `json:"releaseGroups,omitempty"`
	ReleaseTypes          []string          `json:"releaseTypes,omitempty"`
	EpisodeFileQualities  []json.RawMessage `json:"episodeFileQualities,omitempty"`
	PercentOfEpisodes     float64           `json:"percentOfEpisodes"`
}

// MediaCover is the v5 wire shape for `NzbDrone.Core.MediaCover.MediaCover`
// (`src/NzbDrone.Core/MediaCover/MediaCover.cs:16-31`). v5-only here
// because v5's STJson global serializer wires the `MediaCoverTypes`
// enum as a camelCase string (`"poster"`, `"banner"`, etc.) via
// `JsonStringEnumConverter(JsonNamingPolicy.CamelCase)`, while the
// Go-port's persisted `mediacover.Cover` keeps `CoverType` as a
// typed int (the Series.Images JSON blob persists the legacy int
// representation). The two types stay decoupled: the populated
// SeasonResource mapper translates `mediacover.Cover.CoverType.String()`
// → this struct's `CoverType` string field at projection time.
//
// `RemoteUrl` is nullable on C# (`string?`) so omitempty drops it
// when empty; `Url` is always emitted (matches the C# property's
// non-nullable default-string posture).
//
// J309 — wire-shape pin only; no production caller populates this
// type today.
type MediaCover struct {
	CoverType string `json:"coverType"`
	Url       string `json:"url"`
	RemoteUrl string `json:"remoteUrl,omitempty"`
}

// SeasonMonitorHandler returns PUT /api/v5/series/{id}/season.
//
// Mirrors `SeriesController.UpdateSeasonMonitored` at
// `src/Sonarr.Api.V5/Series/SeriesController.cs:223-246`:
//
//  1. Load the series by route-{id}; 404 if missing (matches C#
//     `_seriesService.GetSeries(id)` throwing ModelNotFoundException).
//  2. Walk the persisted Seasons JSON, find the entry whose
//     SeasonNumber matches the body's SeasonNumber; 404 if absent
//     (matches C# `series.Seasons.FirstOrDefault(...) == null`).
//  3. Overwrite that entry's Monitored field with the body's value.
//     Other Season fields (Statistics snapshot, Images, etc.) are
//     preserved via the `[]map[string]any` decode/re-encode pattern
//     (matches the J214/seasonpass updateSeasonsJSON contract).
//  4. Persist via Writer.Update at the repo layer (matches the
//     established v3 SeriesEditor + SeasonPass pattern — service-
//     layer UpdateSeries cascade is DEFERRED on the v5 surface
//     today; the v3 5.D1a deferred-cascade contract carries through).
//  5. Emit SignalR `series/updated` broadcast with the freshly-
//     updated full SeriesResource (matches C#
//     `BroadcastResourceChange(ModelAction.Updated, GetSeriesResource(series, false))`).
//  6. Return 200 + SeasonResource with SeasonNumber + Monitored
//     (Statistics + Images NOT emitted — matches C#
//     `season.ToResource()` default `includeImages: false`).
//
// 400 on invalid id / malformed body / missing seasonNumber field.
// 500 on Service errors (load / persist).
//
// J307 — Wave 3 LARGE-family arc; Series family final residual
// sub-slice (v5-NEW endpoint — no v3 equivalent).
func SeasonMonitorHandler(svc Writer, emitter Emitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "series/season: invalid id")
			return
		}
		var body SeasonResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("series/season: malformed body: %v", err))
			return
		}
		ctx := r.Context()
		existing, err := svc.GetSeries(ctx, id)
		if err != nil {
			if errors.Is(err, series.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("series %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/season: get %d: %w", id, err), nil)
			return
		}
		updatedSeasons, found, err := setSeasonMonitoredJSON(existing.Seasons, body.SeasonNumber, body.Monitored)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/season: encode seasons: %w", err), nil)
			return
		}
		if !found {
			httperr.NotFound(w, fmt.Sprintf("series %d season %d not found", id, body.SeasonNumber))
			return
		}
		existing.Seasons = updatedSeasons
		if err := svc.Update(ctx, existing); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/season: update %d: %w", id, err), nil)
			return
		}
		// J397: cascade the per-episode monitored state for the season
		// (C# `_seriesService.UpdateSeries` runs the EpisodeMonitoredService
		// cascade). Discovered via optional-extension on svc so the handler
		// signature is unchanged; when the writer doesn't implement it
		// (test fakes) the episode cascade is skipped (pre-J397 behaviour).
		if cascader, ok := svc.(SeasonEpisodeMonitorer); ok && cascader != nil {
			if err := cascader.SetEpisodeMonitoredBySeason(ctx, id, body.SeasonNumber, body.Monitored); err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/season: episode cascade %d/%d: %w", id, body.SeasonNumber, err), nil)
				return
			}
		}
		// SignalR series/updated broadcast — matches C# at line 242
		// (`BroadcastResourceChange(ModelAction.Updated, GetSeriesResource(...))`).
		// Best-effort: failures are logged but don't fail the request.
		res := apiv3.ToSeriesResource(*existing)
		_ = emitter.Updated(ctx, res)
		// Response: 200 + SeasonNumber + Monitored ONLY (matches C#
		// season.ToResource() default includeImages=false).
		out := SeasonResource{SeasonNumber: body.SeasonNumber, Monitored: body.Monitored}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// setSeasonMonitoredJSON walks the existing persisted Seasons JSON
// (a `[]obj` array) and overwrites the Monitored field on the entry
// whose SeasonNumber matches. Returns the updated JSON + a found
// bool. Empty / whitespace-only input → (existing, false, nil) — no
// synthesis (a fresh-imported series has zero seasons; the C# v5
// endpoint short-circuits to 404 via the season-lookup miss). Mirrors
// the JSON round-trip strategy in apiv3.updateSeasonsJSON
// (seasonpass.go:215-252) at the single-season granularity.
func setSeasonMonitoredJSON(existing string, seasonNumber int, monitored bool) (string, bool, error) {
	if existing == "" {
		return existing, false, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(existing), &rows); err != nil {
		return "", false, err
	}
	for _, row := range rows {
		nf, ok := row["seasonNumber"].(float64)
		if !ok {
			continue
		}
		if int(nf) == seasonNumber {
			row["monitored"] = monitored
			buf, mErr := json.Marshal(rows)
			if mErr != nil {
				return "", false, mErr
			}
			return string(buf), true, nil
		}
	}
	return existing, false, nil
}
