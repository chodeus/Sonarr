package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// ManualImportResource is the per-file wire shape for GET
// /api/v3/manualimport. Mirrors C#
// `src/Sonarr.Api.V3/ManualImport/ManualImportResource.cs:17-38`.
//
// `Id` is `HashConverter.GetHashInt31(Path)` — a stable per-path id the
// React UI uses as the table-row key (the file has no DB row yet).
// `Quality` / `Languages` use the same expanded wire shapes the
// reprocess item carries (`*QualityModelResource`, `[]LanguageRef`) so
// the UI round-trips them unchanged into the ManualImport command.
//
// J387a fills the disk-decision subset: Id / Path / RelativePath /
// FolderName / Name / Size / Series / SeasonNumber / Episodes /
// ReleaseGroup / Quality / Languages / IndexerFlags / ReleaseType /
// Rejections. QualityWeight (J387c) and CustomFormats + CustomFormatScore
// (the LocalEpisodeFormatCalculator pass — deferred) emit their zero
// values for now.
type ManualImportResource struct {
	ID                int                             `json:"id"`
	Path              string                          `json:"path"`
	RelativePath      string                          `json:"relativePath"`
	FolderName        string                          `json:"folderName"`
	Name              string                          `json:"name"`
	Size              int64                           `json:"size"`
	Series            *SeriesResource                 `json:"series,omitempty"`
	SeasonNumber      *int                            `json:"seasonNumber,omitempty"`
	Episodes          []EpisodeResource               `json:"episodes"`
	EpisodeFileID     *int                            `json:"episodeFileId,omitempty"`
	ReleaseGroup      string                          `json:"releaseGroup"`
	Quality           *QualityModelResource           `json:"quality,omitempty"`
	Languages         []LanguageRef                   `json:"languages"`
	QualityWeight     int                             `json:"qualityWeight"`
	DownloadID        string                          `json:"downloadId,omitempty"`
	CustomFormats     []CustomFormatResource          `json:"customFormats"`
	CustomFormatScore int                             `json:"customFormatScore"`
	IndexerFlags      int                             `json:"indexerFlags"`
	ReleaseType       string                          `json:"releaseType"`
	Rejections        []ManualImportRejectionResource `json:"rejections"`
}

// ManualImportQuery carries the parsed GET /api/v3/manualimport query
// parameters into the [ManualImportLister]. Mirrors the C#
// `ManualImportController.GetMediaFiles` signature at
// `ManualImportController.cs:27`.
type ManualImportQuery struct {
	Folder              string
	DownloadID          string
	SeriesID            *int
	SeasonNumber        *int
	FilterExistingFiles bool
}

// ManualImportLister is the optional-extension surface for the GET
// handler's disk-walk + decision evaluation. The cmd/sonarr wrapper
// (which carries the production ImportDecisionMaker + AggregationService
// + disk/parsing/series/episode lookups) implements it; the handler
// discovers it via a nil-check so test fakes / hosts without the wiring
// keep the pre-J387 `[]` behaviour. Returns wire resources directly so
// the api/v3 layer stays free of episodeimport domain imports.
type ManualImportLister interface {
	GetMediaFiles(ctx context.Context, q ManualImportQuery) ([]ManualImportResource, error)
}

// ManualImportHandler returns GET /api/v3/manualimport. Mirrors C#
// `ManualImportController.GetMediaFiles` at
// `src/Sonarr.Api.V3/ManualImport/ManualImportController.cs:25-36`
// which dispatches on (seriesId + seasonNumber) vs (folder/downloadId
// /seriesId/filterExistingFiles) and returns ManualImportResource[].
//
// When lister is nil the handler emits `[]` (the pre-J387 stub posture
// — a 200 for the UI's folder browser). When wired, it parses the query
// params and delegates the disk walk + decision evaluation + mapping to
// the lister. `filterExistingFiles` defaults to true (C# default arg at
// `ManualImportController.cs:27`).
func ManualImportHandler(lister ManualImportLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seriesID, ok := parseOptionalIntParam(w, q, "seriesId")
		if !ok {
			return
		}
		seasonNumber, ok := parseOptionalIntParam(w, q, "seasonNumber")
		if !ok {
			return
		}

		if lister == nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}

		res, err := lister.GetMediaFiles(r.Context(), ManualImportQuery{
			Folder:              q.Get("folder"),
			DownloadID:          q.Get("downloadId"),
			SeriesID:            seriesID,
			SeasonNumber:        seasonNumber,
			FilterExistingFiles: q.Get("filterExistingFiles") != "false",
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("manualimport: %w", err), nil)
			return
		}
		if res == nil {
			res = []ManualImportResource{}
		}
		addManualImportQualityWeight(res)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// addManualImportQualityWeight fills each resource's QualityWeight from
// its quality, porting C# `ManualImportController.AddQualityWeight`
// (`ManualImportController.cs:82-92`): the static default-definition
// weight + Revision.Real*10 + Revision.Version. Applied centrally over
// the lister output so all modes (disk / library / unmapped) match the
// C# controller's `Select(AddQualityWeight)`.
func addManualImportQualityWeight(items []ManualImportResource) {
	for i := range items {
		q := items[i].Quality
		if q == nil {
			continue
		}
		qual, _ := quality.FindByID(q.Quality.ID)
		items[i].QualityWeight = quality.QualityWeight(qual) + q.Revision.Real*10 + q.Revision.Version
	}
}

// parseOptionalIntParam reads an optional integer query param. Returns
// (nil, true) when absent, (ptr, true) when a valid int, and writes a
// 400 + returns (nil, false) when present but non-numeric.
func parseOptionalIntParam(w http.ResponseWriter, q url.Values, key string) (*int, bool) {
	v := q.Get(key)
	if v == "" {
		return nil, true
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		httperr.BadRequest(w, "manualimport: invalid "+key)
		return nil, false
	}
	return &n, true
}

// ManualImportReprocessItem is the per-item wire shape for POST
// /api/v3/manualimport. Mirrors C#
// `src/Sonarr.Api.V3/ManualImport/ManualImportReprocessResource.cs`.
//
// Wire-shape note: `Quality` is a `*quality.Model` pointer so the
// "Quality.Quality == Unknown" check at C# `ManualImportController.cs:65`
// can distinguish "field absent" from "field present with Unknown
// quality" — Go's zero-value-vs-nil distinction maps to C#'s null-check.
// `Languages` is a slice of typed LanguageRef entries (J314 helper
// type) — wire shape `[{"id":N,"name":"X"}]` matching the C#
// `List<Language>` projection at C# `ManualImportReprocessResource.cs:13`.
// J332 widened from []int to []LanguageRef; the C# `:60` count
// guard maps to len(item.Languages) <= 1 + Languages[0].ID == 0
// (Unknown) on the Go side. Body-decode of `"languages":[]`
// (empty array) decodes to a zero-length []LanguageRef; this is
// backwards-compatible with the prior []int decode. Populated
// body shape changes from `"languages":[N,N]` to `"languages":
// [{"id":N,"name":"X"},...]` matching C# v3+v5 — no current Go-
// port test exercises populated bodies so no test churn.
type ManualImportReprocessItem struct {
	Path              string                          `json:"path"`
	SeriesID          int                             `json:"seriesId"`
	SeasonNumber      *int                            `json:"seasonNumber,omitempty"`
	Episodes          []manualImportEpisodeStub       `json:"episodes,omitempty"`
	EpisodeIDs        []int                           `json:"episodeIds,omitempty"`
	Quality           *QualityModelResource           `json:"quality,omitempty"`
	Languages         []LanguageRef                   `json:"languages"`
	ReleaseGroup      string                          `json:"releaseGroup"`
	DownloadID        string                          `json:"downloadId,omitempty"`
	CustomFormats     []CustomFormatResource          `json:"customFormats"`
	CustomFormatScore int                             `json:"customFormatScore"`
	IndexerFlags      int                             `json:"indexerFlags"`
	ReleaseType       string                          `json:"releaseType"`
	Rejections        []ManualImportRejectionResource `json:"rejections"`
}

// ManualImportRejectionResource mirrors C#
// `src/Sonarr.Api.V3/ManualImport/ManualImportResource.cs:84-105`
// (`ImportRejectionResource`) — the per-item rejection wire shape the
// reprocess + list endpoints emit. `Reason` carries the human-readable
// rejection message (C# `ToResource` maps `rejection.Message` → the
// `reason` field, NOT the categorical reason enum); `Type` is the
// camelCase RejectionType enum string (`"permanent"` / `"temporary"`,
// per `openapi.json` `RejectionType: {enum: [permanent, temporary]}`).
type ManualImportRejectionResource struct {
	Reason string `json:"reason"`
	Type   string `json:"type"`
}

// Rejection-type wire constants for [ManualImportRejectionResource.Type].
// Mirror the camelCase StringEnumConverter projection of C#
// `DecisionEngine.RejectionType` (Permanent=0, Temporary=1).
const (
	manualImportRejectionTypePermanent = "permanent"
	manualImportRejectionTypeTemporary = "temporary"
)

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

// ManualImportCustomFormatCalculator is the optional-extension surface
// for the reprocess handler's per-item CustomFormats + Score
// computation (C# `ManualImportController.cs:62-63`). The wrapper in
// cmd/sonarr resolves the series + format catalogue internally and
// runs the title-based calculator (customformats
// .ParseCustomFormatForRemoteEpisode) against the parsed-path info +
// the item's resolved languages + indexer flags. Nil-tolerant: when
// nil (or the underlying deps are missing), the handler keeps the
// pre-J346 empty-slice fallback.
//
// The calculation is the same title-based approximation the Parse
// handler uses (J345) — the full ImportDecisionMaker pipeline (which
// would feed file-size + mediainfo into the size/source specs) stays
// deferred; this closes the release-title-derivable subset.
//
// J346.
type ManualImportCustomFormatCalculator interface {
	CalculateForManualImport(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo, seriesID int, languages []language.Language, indexerFlags int) ([]CustomFormatResource, int)
}

// ManualImportDecisionRejector is the optional-extension surface for
// the reprocess handler's episodeIds-branch Rejections list (C#
// `ManualImportService.cs:157-200`). The reprocess handler discovers it
// via type-assertion on the `calc` dep (J338/J345 idiom) — when the
// supplied calculator ALSO implements this interface, the episodeIds
// branch runs the full ImportDecisionMaker pipeline (aggregation + the
// 13 wired specs) over an assembled LocalEpisode and surfaces its
// rejections; when it doesn't (nil calc, or a fake that only implements
// the CustomFormat calculator), the branch emits `[]` as before.
//
// The wrapper (cmd/sonarr) resolves the series + full episodes by id,
// reads the file size, reconstructs Quality / Languages / ReleaseGroup /
// IndexerFlags / ReleaseType from the (already-reprocessed) item, runs
// the SAME production AggregationService the real download-import flow
// uses (so manual-import rejections stay consistent with that path),
// then ImportDecisionMaker.GetDecision. episodeIDs carries the user's
// originally-selected episode ids (the handler clears item.EpisodeIDs
// before this fires, so they're passed explicitly).
//
// J356.
type ManualImportDecisionRejector interface {
	ReprocessDecisionRejections(ctx context.Context, item *ManualImportReprocessItem, parsed *parser.ParsedEpisodeInfo, episodeIDs []int) []ManualImportRejectionResource
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
//   - Rejections list. PORTED (J355 + J356). The deterministic
//     NoEpisodes "Episodes not selected" arm (C#
//     `ManualImportService.cs:206-230`, fired when the user picked a
//     season but no episodes) landed in J355. The episodeIds-driven
//     GetDecision rejections (C# `:157-200`, which runs the full
//     ImportDecisionMaker pipeline over an assembled + aggregated
//     LocalEpisode) land in J356 via the optional-extension
//     [ManualImportDecisionRejector] discovered on the calc dep — when
//     the wired calculator also implements it, the episodeIds branch
//     surfaces the real spec rejections; otherwise the branch emits
//     `[]`. The parsed==nil UnableToParse rejection remains deferred
//     (the early-return branch below).
//   - CustomFormats + CustomFormatScore re-calculation. J346 wires the
//     title-based subset via the optional ManualImportCustomFormatCalculator
//     dep (reuses the J345 customformats.ParseCustomFormatForRemoteEpisode
//     calculator against the parsed-path info + resolved series +
//     languages + indexerFlags). The full ImportDecisionMaker pipeline
//     — which feeds file-size + mediainfo into the size/source specs —
//     stays DEFERRED; the bare-path endpoint can't synthesise those
//     inputs. When the calculator dep is nil, the pre-J346 empty-slice
//     fallback fires.
//   - Full EpisodeResource projection on each item.Episodes entry.
//     The minimal `{id, seasonNumber, episodeNumber, title}` shape is
//     emitted; the full ~30-field projection is the same DEFERRED
//     surface other v3 handlers carry.
//
// Audit row C.1.i-follow-5b (J238). CustomFormats sub-arc closed
// (title-based) at J346; Rejections + full Episode projection remain.
func ManualImportReprocessHandler(seriesLookup ManualImportSeriesLookup, episodeLookup ManualImportEpisodeLookup, calc ManualImportCustomFormatCalculator) http.Handler {
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
			reprocessOne(ctx, &items[i], seriesLookup, episodeLookup, calc)
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
func reprocessOne(ctx context.Context, item *ManualImportReprocessItem, seriesLookup ManualImportSeriesLookup, episodeLookup ManualImportEpisodeLookup, calc ManualImportCustomFormatCalculator) {
	// Capture the user's INPUT selection before reprocessing overwrites
	// item.SeasonNumber (from parsed info, below) and clears
	// item.EpisodeIDs. C# `ReprocessItem` branches on the input
	// `episodeIds.Any()` / `seasonNumber.HasValue` (the user's choice),
	// not the reprocessed values, at `ManualImportService.cs:157,206`.
	hadEpisodeIDs := len(item.EpisodeIDs) > 0
	inputSeasonProvided := item.SeasonNumber != nil
	// Capture the user's originally-selected episode ids before the
	// reprocess clears item.EpisodeIDs (C# `:76`) — the J356 decision
	// rejector needs them to resolve the full episodes for GetDecision.
	originalEpisodeIDs := append([]int(nil), item.EpisodeIDs...)

	parsed := parser.ParsePath(item.Path)
	if parsed == nil {
		// C# `_manualImportService.ReprocessItem` returns a result
		// with empty Episodes + a "Unable to parse" rejection. Without
		// the ImportDecisionMaker we can't synthesise that rejection;
		// leave item.Episodes nil and item.Rejections `[]`.
		item.Episodes = nil
		item.EpisodeIDs = nil
		if item.Rejections == nil {
			item.Rejections = []ManualImportRejectionResource{}
		}
		if item.CustomFormats == nil {
			item.CustomFormats = []CustomFormatResource{}
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
	// returned a non-empty list. Mirror. J332: item.Languages widened
	// from []int to []LanguageRef — Unknown check moves to the .ID
	// field of the LanguageRef struct; rebuilt list uses ToLanguageRef
	// per persisted language.Language entry.
	if len(item.Languages) <= 1 && (len(item.Languages) == 0 || item.Languages[0].ID == 0 /* Unknown */) {
		fresh := parser.ParseLanguages(parsed.ReleaseTitle)
		if len(fresh) > 0 {
			out := make([]LanguageRef, 0, len(fresh))
			for _, l := range fresh {
				out = append(out, ToLanguageRef(l.ID))
			}
			item.Languages = out
		}
	}
	// Quality — C# `:65-68` reassigns ONLY when item.Quality is non-
	// nil but its inner .Quality.ID == Unknown.ID (0). Mirror. J333:
	// item.Quality widened from *quality.Model (persistence-reduced
	// wire form `{quality:int,revision:{...}}`) to
	// *QualityModelResource (expanded wire form `{quality:{id,name,
	// source,resolution},revision:{...}}` matching C# default
	// serialization). Unknown check moves from .Quality (the embedded
	// quality.Quality) to .Quality.ID (the inner QualityValue.ID
	// field on the new typed DTO); reassigned via
	// ToQualityModelResource from the parsed persistence model.
	if item.Quality != nil && item.Quality.Quality.ID == quality.Unknown.ID {
		// Take the parsed Quality verbatim (Quality + Revision)
		// via the typed projection.
		item.Quality = ToQualityModelResource(quality.Model{
			Quality:  parsed.Quality.Quality,
			Revision: parsed.Quality.Revision,
		})
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
	// CustomFormats + Score — J346 title-based calculation. C#
	// `ManualImportController.cs:62-63`. When the calculator dep is
	// wired AND the item has a resolved series, run the calc against
	// the parsed-path info + the item's resolved languages +
	// indexerFlags. The bare-path endpoint can't feed file-size /
	// mediainfo into the size/source specs (those need the full
	// ImportDecisionMaker pipeline), so this covers the release-title-
	// derivable subset only.
	if calc != nil && item.SeriesID > 0 {
		langs := languagesFromLanguageRefs(item.Languages)
		cf, score := calc.CalculateForManualImport(ctx, parsed, item.SeriesID, langs, item.IndexerFlags)
		item.CustomFormats = cf
		item.CustomFormatScore = score
	}
	// Rejections.
	//
	// NoEpisodes arm (J355): C# `ManualImportService.cs:206-230` — when
	// the user selected a season but NOT any episodes within it
	// (`!episodeIds.Any() && seasonNumber.HasValue`), the item is
	// rejected with `ImportRejectionReason.NoEpisodes` "Episodes not
	// selected" rather than silently importing the whole season. The
	// item's Episodes list is emptied (the user's season selection
	// persists in SeasonNumber; Episodes stays `[]` so the UI prompts
	// for an explicit episode pick). This arm needs no decision engine —
	// it's a pure input-shape check.
	//
	// The episodeIds-driven GetDecision rejections (C# `:157-200`, which
	// runs the full ImportDecisionMaker spec pipeline over an aggregated
	// LocalEpisode) stay DEFERRED to a follow-up slice — that requires
	// assembling a LocalEpisode + threading the orchestrator into the
	// reprocess handler. Until then the episodeIds branch emits `[]`.
	if !hadEpisodeIDs && inputSeasonProvided {
		item.Episodes = []manualImportEpisodeStub{}
		item.Rejections = []ManualImportRejectionResource{{
			Reason: "Episodes not selected",
			Type:   manualImportRejectionTypePermanent,
		}}
	} else if hadEpisodeIDs {
		// episodeIds GetDecision rejections (J356). Discovered via
		// optional-extension type-assertion on the calc dep so the
		// handler signature is unchanged; a nil calc / a fake that only
		// implements the CustomFormat calculator simply skips this and
		// leaves the empty-slice fallback. Mirrors C#
		// `ManualImportService.cs:157-200`.
		if rej, ok := calc.(ManualImportDecisionRejector); ok && rej != nil {
			item.Rejections = rej.ReprocessDecisionRejections(ctx, item, parsed, originalEpisodeIDs)
		}
	}
	if item.Rejections == nil {
		item.Rejections = []ManualImportRejectionResource{}
	}
	if item.CustomFormats == nil {
		item.CustomFormats = []CustomFormatResource{}
	}
}

// languagesFromLanguageRefs maps the item's []LanguageRef back to the
// typed []language.Language the custom-format calculator consumes.
// Unknown / unresolvable IDs are skipped (matches the calculator's
// tolerance for partial language data).
func languagesFromLanguageRefs(refs []LanguageRef) []language.Language {
	if len(refs) == 0 {
		return nil
	}
	out := make([]language.Language, 0, len(refs))
	for _, ref := range refs {
		if l, ok := language.FindByID(ref.ID); ok {
			out = append(out, l)
		}
	}
	return out
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
