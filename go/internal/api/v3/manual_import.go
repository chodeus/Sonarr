package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// ManualImportHandler returns GET /api/v3/manualimport. Mirrors C#
// `ManualImportController.GetMediaFiles` at
// `src/Sonarr.Api.V3/ManualImport/ManualImportController.cs:25-36`
// which dispatches on (seriesId + seasonNumber) vs (folder/downloadId
// /seriesId/filterExistingFiles) and returns ManualImportResource[]
// — a per-file projection with quality/language inference + rejection
// list.
//
// DEFERRED with rationale: the manual-import service that performs
// the disk walk + parser + decision-engine evaluation isn't ported.
// Today this handler emits `[]` matching the no-results wire shape
// so the React UI's manual-import folder browser gets 200 instead of
// 404. The POST companion (reprocessItem) is intentionally NOT
// stubbed - silently no-opping a POST that the user expects to
// import files would be misleading.
//
// Query params are validated but otherwise ignored.
func ManualImportHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// Validate int params if present.
		for _, key := range []string{"seriesId", "seasonNumber"} {
			if v := q.Get(key); v != "" {
				if _, err := strconv.Atoi(v); err != nil {
					httperr.BadRequest(w, "manualimport: invalid "+key)
					return
				}
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
}

// ManualImportReprocessItem is the per-item wire shape for POST
// /api/v3/manualimport. Mirrors C#
// `src/Sonarr.Api.V3/ManualImport/ManualImportReprocessResource.cs`.
//
// Wire-shape note: `Quality` is a `*quality.Model` pointer so the
// "Quality.Quality == Unknown" check at C# `ManualImportController.cs:65`
// can distinguish "field absent" from "field present with Unknown
// quality" — Go's zero-value-vs-nil distinction maps to C#'s null-check.
// `Languages` is a slice (a nil slice and an empty `[]` array both
// pass the C# `<= 1` count guard at `:60`).
type ManualImportReprocessItem struct {
	Path              string         `json:"path"`
	SeriesID          int            `json:"seriesId"`
	SeasonNumber      *int           `json:"seasonNumber,omitempty"`
	Episodes          []manualImportEpisodeStub `json:"episodes,omitempty"`
	EpisodeIDs        []int          `json:"episodeIds,omitempty"`
	Quality           *quality.Model `json:"quality,omitempty"`
	Languages         []int          `json:"languages"`
	ReleaseGroup      string         `json:"releaseGroup"`
	DownloadID        string         `json:"downloadId,omitempty"`
	CustomFormats     []any          `json:"customFormats"`
	CustomFormatScore int            `json:"customFormatScore"`
	IndexerFlags      int            `json:"indexerFlags"`
	ReleaseType       string         `json:"releaseType"`
	Rejections        []any          `json:"rejections"`
}

// manualImportEpisodeStub mirrors the EpisodeResource fields the C#
// pipeline emits in the reprocess response. Initial port emits the
// minimum 3-field subset (id, seasonNumber, episodeNumber, title)
// matching what the UI table renders; the full ~30-field
// EpisodeResource projection is deferred.
type manualImportEpisodeStub struct {
	ID            int    `json:"id"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Title         string `json:"title,omitempty"`
}

// ManualImportSeriesLookup is the narrow series-by-id interface the
// reprocess handler depends on. `*series.Service.GetSeries` satisfies
// it. Decoupled from QueueGrabSeriesLookup so the two handler families
// don't share a single interface name (per the narrow-interfaces
// posture other v3 handlers use).
type ManualImportSeriesLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// ManualImportEpisodeLookup is the narrow episode-by-id interface.
// `*episodes.Service.GetEpisode` satisfies it.
type ManualImportEpisodeLookup interface {
	GetEpisode(ctx context.Context, id int) (*episodes.Episode, error)
}

// ManualImportReprocessHandler returns POST /api/v3/manualimport.
//
// Mirrors `ManualImportController.ReprocessItems` at C#
// `src/Sonarr.Api.V3/ManualImport/ManualImportController.cs:37-80`:
//
//   - Empty body → 400 "items must be provided" (C# `:41-44`).
//   - For each item: re-parse the path via parser.ParsePath, resolve
//     series (by SeriesID body field, when set), resolve episodes (by
//     EpisodeIds body field when set, else from ParsedEpisodeInfo +
//     Series), recompute the per-item ReleaseType / Languages /
//     Quality / ReleaseGroup fields per the C# precedence rules at
//     `:50-73`.
//   - Return the updated array.
//
// This endpoint does NOT trigger an actual import — that's a separate
// `POST /api/v3/command {name: "ManualImport"}` path. The reprocess
// action exists to let the UI refresh its in-progress import-table
// state when the user changes per-row inputs (manually picking a
// different episode, overriding Quality, etc.). Re-evaluation only;
// no disk I/O, no decision-engine, no dispatch.
//
// DEFERRED sub-arcs:
//
//   - Rejections list. C# pulls from `ManualImportService.ReprocessItem`
//     which runs the full ImportDecisionMaker pipeline (~15 spec
//     classes). The Go-port hasn't ported ImportDecisionMaker yet
//     (it's the analogous-to-download.ProcessDecisions surface for
//     local files). Rejections emit as `[]` for this initial port.
//   - CustomFormats + CustomFormatScore re-calculation. The Go-port
//     has `customformats.ParseCustomFormatForFile` but it requires an
//     `*episodefiles.File` (with size + path), not the bare path string
//     this endpoint receives. A path → File adapter is a sibling slice.
//     Emits 0 / `[]` for this port.
//   - Full EpisodeResource projection on each item.Episodes entry.
//     The minimal `{id, seasonNumber, episodeNumber, title}` shape is
//     emitted; the full ~30-field projection is the same DEFERRED
//     surface other v3 handlers carry.
//
// Audit row C.1.i-follow-5b (J238). 3 deferred sub-arcs documented.
func ManualImportReprocessHandler(seriesLookup ManualImportSeriesLookup, episodeLookup ManualImportEpisodeLookup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items []ManualImportReprocessItem
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&items); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("manualimport: malformed body: %v", err))
			return
		}
		if len(items) == 0 {
			httperr.BadRequest(w, "items must be provided")
			return
		}
		ctx := r.Context()
		for i := range items {
			reprocessOne(ctx, &items[i], seriesLookup, episodeLookup)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(items)
		_, _ = w.Write(buf)
	})
}

// reprocessOne mutates the supplied item in place per the C#
// precedence rules at `ManualImportController.cs:46-77`. Pulls out
// the parsed-episode-info, resolves series + episodes, applies the
// quality / language / release-group fallbacks, and clears the
// EpisodeIds field (C# `:76`) so the UI re-fetches from item.Episodes
// instead.
func reprocessOne(ctx context.Context, item *ManualImportReprocessItem, seriesLookup ManualImportSeriesLookup, episodeLookup ManualImportEpisodeLookup) {
	parsed := parser.ParsePath(item.Path)
	if parsed == nil {
		// C# `_manualImportService.ReprocessItem` returns a result
		// with empty Episodes + a "Unable to parse" rejection. Without
		// the ImportDecisionMaker we can't synthesise that rejection;
		// leave item.Episodes nil and item.Rejections `[]`.
		item.Episodes = nil
		item.EpisodeIDs = nil
		if item.Rejections == nil {
			item.Rejections = []any{}
		}
		return
	}
	// ReleaseType — C# `:51` reads from `processedItem.ReleaseType`
	// which the service computes per the parsed-info shape. Mirror.
	item.ReleaseType = manualImportReleaseTypeFor(parsed)
	// SeasonNumber — C# `:50` overwrites item.SeasonNumber with
	// processedItem.SeasonNumber. Match.
	if parsed.SeasonNumber > 0 {
		sn := parsed.SeasonNumber
		item.SeasonNumber = &sn
	}
	// Languages — C# `:60-63` reassigns ONLY when item.Languages has
	// <= 1 entry AND that single entry is Unknown AND processedItem
	// returned a non-empty list. Mirror.
	if len(item.Languages) <= 1 && (len(item.Languages) == 0 || item.Languages[0] == 0 /* Unknown */) {
		fresh := parser.ParseLanguages(parsed.ReleaseTitle)
		if len(fresh) > 0 {
			out := make([]int, 0, len(fresh))
			for _, l := range fresh {
				out = append(out, l.ID)
			}
			item.Languages = out
		}
	}
	// Quality — C# `:65-68` reassigns ONLY when item.Quality is non-
	// nil but its .Quality == Unknown. Mirror.
	if item.Quality != nil && item.Quality.Quality == quality.Unknown {
		// Take the parsed Quality verbatim (Quality + Revision).
		item.Quality = &quality.Model{
			Quality:  parsed.Quality.Quality,
			Revision: parsed.Quality.Revision,
		}
	}
	// ReleaseGroup — C# `:70-72` falls back to processedItem.ReleaseGroup
	// only when item.ReleaseGroup IsNullOrWhiteSpace. Mirror.
	if strings.TrimSpace(item.ReleaseGroup) == "" {
		item.ReleaseGroup = parsed.ReleaseGroup
	}
	// Episodes — resolve from EpisodeIds if provided, else build from
	// ParsedEpisodeInfo + Series. C# `:51` reads processedItem.Episodes
	// regardless of input.
	resolved := resolveManualImportEpisodes(ctx, item, parsed, seriesLookup, episodeLookup)
	item.Episodes = resolved
	// Clear EpisodeIds in favour of the full episode (C# `:76`).
	item.EpisodeIDs = nil
	// Rejections + CustomFormats + Score — DEFERRED per handler
	// doc-comment. Emit empty slices / zero score for wire-shape
	// completeness.
	if item.Rejections == nil {
		item.Rejections = []any{}
	}
	if item.CustomFormats == nil {
		item.CustomFormats = []any{}
	}
}

// manualImportReleaseTypeFor maps the parsed-info shape to the
// ReleaseType enum string emitted in the wire response.
func manualImportReleaseTypeFor(p *parser.ParsedEpisodeInfo) string {
	if p == nil {
		return CamelCaseEnumName(parser.ReleaseTypeUnknown.String())
	}
	switch {
	case p.FullSeason:
		return CamelCaseEnumName(parser.ReleaseTypeSeasonPack.String())
	case len(p.EpisodeNumbers) >= 2:
		return CamelCaseEnumName(parser.ReleaseTypeMultiEpisode.String())
	case len(p.EpisodeNumbers) == 1:
		return CamelCaseEnumName(parser.ReleaseTypeSingleEpisode.String())
	}
	return CamelCaseEnumName(parser.ReleaseTypeUnknown.String())
}

// resolveManualImportEpisodes returns the episode-stub list for the
// reprocessed item. Resolution priority:
//
//  1. If item.EpisodeIDs is non-empty AND episodeLookup is wired,
//     look each up by id (matches C# `:51` which uses the cached
//     Episodes from processedItem regardless of input).
//  2. Else if series + parsed info give a (SeriesID, SeasonNumber,
//     EpisodeNumbers) tuple AND episodeLookup is wired, look each up
//     by series+season+number. DEFERRED — the Go-port episodes.Service
//     exposes Get(id) but not Get(seriesID, season, episode) at this
//     layering; deferred to a follow-up sub-arc.
//  3. Else nil (UI shows "no episodes resolved").
func resolveManualImportEpisodes(ctx context.Context, item *ManualImportReprocessItem, parsed *parser.ParsedEpisodeInfo, seriesLookup ManualImportSeriesLookup, episodeLookup ManualImportEpisodeLookup) []manualImportEpisodeStub {
	if episodeLookup == nil {
		return nil
	}
	if len(item.EpisodeIDs) > 0 {
		out := make([]manualImportEpisodeStub, 0, len(item.EpisodeIDs))
		for _, id := range item.EpisodeIDs {
			ep, err := episodeLookup.GetEpisode(ctx, id)
			if err != nil || ep == nil {
				continue
			}
			out = append(out, manualImportEpisodeStub{
				ID: ep.ID, SeasonNumber: ep.SeasonNumber, EpisodeNumber: ep.EpisodeNumber, Title: ep.Title,
			})
		}
		return out
	}
	// SeriesID + parsed-info-based resolution deferred.
	_ = parsed
	_ = seriesLookup
	return nil
}
