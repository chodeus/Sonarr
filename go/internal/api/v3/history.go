package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/httperr"
)

// HistoryResource is the per-record shape in the
// PagingResource[HistoryResource] envelope. Mirrors
// `src/Sonarr.Api.V3/History/HistoryResource.cs`.
//
// Skeleton shape — populated-row mapping (languages/quality slices,
// optional series/episode when includeSeries/includeEpisode=true)
// lands with the paged surface on history.Service (DEFERRED). The
// customFormats[] + customFormatScore fields are wired at the struct
// level here (J296, Wave 1 calculator wire-through slice 1) so
// future populated-row mappers can emit the C# wire shape without
// re-widening; the underlying calculator engine
// ([sonarr.tv/sonarr/internal/customformats.Calculate] +
// [sonarr.tv/sonarr/internal/qualityprofiles.CalculateCustomFormatScore])
// is already ported. Empty-DB golden has zero records so the gap is
// unobservable today; constructors should default CustomFormats to
// `[]CustomFormatResource{}` (not nil) so a future populated row
// without matched formats still emits `customFormats: []` rather
// than `null` — matches the C# Newtonsoft default for non-null
// `List<CustomFormatResource>`.
//
// J312 widening: QualityCutoffNotMet (always emit, C#
// non-nullable bool — populated post-mapping by
// HistoryController.cs:57 via `_upgradableSpecification.QualityCutoffNotMet`;
// the v3 Go-port defaults to false until the upgradable-spec port
// is wired through to this handler — separate slice). Episode +
// Series subresources (pointer-to-existing-Resource with omitempty
// — C# nullable `EpisodeResource?` + `SeriesResource?`; populated
// when the `includeEpisode=true` / `includeSeries=true` query
// params are set, currently DEFERRED on both surfaces). Pointer-
// to-struct preserves the WhenWritingNull wire posture: nil drops
// the key entirely; non-nil emits the full nested shape. Future
// populated-mapper port allocates `&apiv3.ToSeriesResource(s)` etc.
// to light up the subresources without re-widening.
//
// The Data field (J310 widening) is the per-history grab-time
// metadata dictionary the C# tree stamps onto every row
// (`releaseGroup`, `indexer`, `downloadClient`, `size`,
// `indexerFlags`, `releaseType`, etc.). C# `EpisodeHistory.Data`
// defaults to `new Dictionary<string,string>()` so the wire emits
// `"data":{}` for a row with no stamped metadata. Go-port: nil
// e.Data → omitempty drops the key (matches STJson's global
// `JsonIgnoreCondition.WhenWritingNull`); populated map →
// `"data":{...}`. Callers wanting the C# default-emit posture must
// allocate `map[string]string{}` explicitly. The CustomFormat
// calculator engine ALREADY reads this dictionary
// ([sonarr.tv/sonarr/internal/customformats.ParseCustomFormatForHistory]
// at calculate_history.go:36-84 → historyDataFlags at :101-117 →
// extracts size + indexerFlags + releaseType for the matcher input)
// so emitting it on the wire closes the wire-shape gap that the
// J296 customFormats wire-through opened (clients can now read the
// same fields the matcher reads).
type HistoryResource struct {
	ID                  int                    `json:"id"`
	SeriesID            int                    `json:"seriesId"`
	EpisodeID           int                    `json:"episodeId"`
	SourceTitle         string                 `json:"sourceTitle"`
	Date                string                 `json:"date"`
	DownloadID          string                 `json:"downloadId"`
	EventType           string                 `json:"eventType"`
	Languages           []LanguageRef          `json:"languages"`
	Quality             *QualityModelResource  `json:"quality,omitempty"`
	Data                map[string]string      `json:"data,omitempty"`
	QualityCutoffNotMet bool                   `json:"qualityCutoffNotMet"`
	CustomFormats       []CustomFormatResource `json:"customFormats"`
	CustomFormatScore   int                    `json:"customFormatScore"`
	Episode             *EpisodeResource       `json:"episode,omitempty"`
	Series              *SeriesResource        `json:"series,omitempty"`
}

// HistoryPagedLister is the read side the handler depends on. Same
// shape as BlocklistPagedLister — both endpoints share the paging
// envelope and only differ in the per-record resource type.
//
// J380: the includeSeries / includeEpisode flags are threaded INTO
// the interface (rather than the Queue J348 always-hydrate-then-strip
// pattern) so the paged adapter can hydrate the BASE Series / Episode
// embeds CONDITIONALLY — when the client didn't ask, no per-row
// series/episode DB lookup is incurred. The model-based since/series
// path threads the same flags through writeHistoryEntries (J377); the
// paged path threads them here because the lister is its hydration
// boundary.
type HistoryPagedLister interface {
	PagedHistory(ctx context.Context, req PagingRequest, includeSeries, includeEpisode bool) ([]HistoryResource, int, error)
}

// HistoryHandler returns the http.Handler for GET /api/v3/history.
// Mirrors `HistoryController.GetHistory` at
// `src/Sonarr.Api.V3/History/HistoryController.cs:14-35`. Default
// sortKey is "date"; direction defaults to "descending".
//
// The C# version also accepts eventType / episodeId / downloadId /
// seriesIds / languages / quality filters. Filter handling lands
// with the paged-service port (DEFERRED).
//
// J380: the includeSeries / includeEpisode query flags are parsed
// here and passed to the lister so the BASE Series / Episode embeds
// hydrate conditionally (mirrors the C# MapToResource include flags;
// matches the since/series path's J377 flags).
func HistoryHandler(lister HistoryPagedLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "date")
		includeSeries := r.URL.Query().Get("includeSeries") == "true"
		includeEpisode := r.URL.Query().Get("includeEpisode") == "true"
		records, total, err := lister.PagedHistory(r.Context(), req, includeSeries, includeEpisode)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("history: %w", err), nil)
			return
		}
		if records == nil {
			records = []HistoryResource{}
		}
		resp := PagingResource[HistoryResource]{
			Page: req.Page, PageSize: req.PageSize,
			Records: records, SortDirection: req.SortDirection,
			SortKey: req.SortKey, TotalRecords: total,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(resp)
		_, _ = w.Write(buf)
	})
}

// HistoryRangeReader is the read surface for /api/v3/history/since +
// /series. Satisfied by *history.Service.
type HistoryRangeReader interface {
	Since(ctx context.Context, cutoff time.Time, eventType *history.EventType) ([]history.Entry, error)
	GetBySeries(ctx context.Context, seriesID int, eventType *history.EventType) ([]history.Entry, error)
}

// HistorySinceHandler returns GET /api/v3/history/since. Mirrors C#
// `HistoryController.GetHistorySince` at `:100-105`:
//
//	historyService.Since(date, eventType).Select(MapToResource).ToList()
//
// Required query: `date` (ISO-8601 / RFC3339). Optional: `eventType`
// (integer matching history.EventType enum). `includeSeries` /
// `includeEpisode` flags hydrate the BASE Series / Episode embeds
// (J377) when the reader implements the HistorySeriesEmbedder /
// HistoryEpisodeEmbedder extensions.
func HistorySinceHandler(svc HistoryRangeReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dateStr := r.URL.Query().Get("date")
		if dateStr == "" {
			httperr.BadRequest(w, "history/since: missing date")
			return
		}
		cutoff, err := time.Parse(time.RFC3339, dateStr)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("history/since: invalid date %q (RFC3339 required)", dateStr))
			return
		}
		eventType, ok := parseEventTypeQuery(w, r)
		if !ok {
			return
		}
		rows, err := svc.Since(r.Context(), cutoff, eventType)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("history/since: %w", err), nil)
			return
		}
		// J338 populator + J377 includeSeries/includeEpisode embeds are
		// discovered inside writeHistoryEntries via type-assertion on
		// svc (the production *history.Service is composed with a
		// wrapper implementing those extensions; test fixtures aren't,
		// so the empty/nil fallbacks fire).
		includeSeries := r.URL.Query().Get("includeSeries") == "true"
		includeEpisode := r.URL.Query().Get("includeEpisode") == "true"
		writeHistoryEntries(r.Context(), w, rows, svc, includeSeries, includeEpisode)
	})
}

// HistorySeriesHandler returns GET /api/v3/history/series. Mirrors C#
// `HistoryController.GetSeriesHistory` at `:107-129`. Required query:
// `seriesId`. Optional: `eventType`. The C# action also accepts
// `seasonNumber` to filter to a single season — DEFERRED in the Go
// port (needs an episode-table join to map history.EpisodeId →
// Episode.SeasonNumber that the v3 layer doesn't yet thread).
func HistorySeriesHandler(svc HistoryRangeReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seriesIDStr := r.URL.Query().Get("seriesId")
		if seriesIDStr == "" {
			httperr.BadRequest(w, "history/series: missing seriesId")
			return
		}
		seriesID, err := strconv.Atoi(seriesIDStr)
		if err != nil || seriesID <= 0 {
			httperr.BadRequest(w, fmt.Sprintf("history/series: invalid seriesId %q", seriesIDStr))
			return
		}
		eventType, ok := parseEventTypeQuery(w, r)
		if !ok {
			return
		}
		rows, err := svc.GetBySeries(r.Context(), seriesID, eventType)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("history/series: %w", err), nil)
			return
		}
		// J338 populator + J377 embeds — see HistorySinceHandler.
		includeSeries := r.URL.Query().Get("includeSeries") == "true"
		includeEpisode := r.URL.Query().Get("includeEpisode") == "true"
		writeHistoryEntries(r.Context(), w, rows, svc, includeSeries, includeEpisode)
	})
}

// HistoryEpisodeReader is the read surface for /history/episode.
// Satisfied by *history.Service (GetByEpisode). A separate narrow
// interface (not folded into HistoryRangeReader) so existing test fakes
// for since/series don't need to grow the method.
type HistoryEpisodeReader interface {
	GetByEpisode(ctx context.Context, episodeID int, eventType *history.EventType) ([]history.Entry, error)
}

// HistoryEpisodeHandler returns GET /api/v3/history/episode (also the v5
// route). Mirrors C# `HistoryController.GetEpisodeHistory`: it calls
// `historyService.GetByEpisode(episodeId, eventType)` → MapToResource.
// Required `episodeId` → 400 if missing/invalid; optional `eventType`.
// Series/episode embeds + the populator are discovered on svc by
// writeHistoryEntries (same as since/series).
func HistoryEpisodeHandler(svc HistoryEpisodeReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idStr := r.URL.Query().Get("episodeId")
		if idStr == "" {
			httperr.BadRequest(w, "history/episode: missing episodeId")
			return
		}
		episodeID, err := strconv.Atoi(idStr)
		if err != nil || episodeID <= 0 {
			httperr.BadRequest(w, fmt.Sprintf("history/episode: invalid episodeId %q", idStr))
			return
		}
		eventType, ok := parseEventTypeQuery(w, r)
		if !ok {
			return
		}
		rows, err := svc.GetByEpisode(r.Context(), episodeID, eventType)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("history/episode: %w", err), nil)
			return
		}
		includeSeries := r.URL.Query().Get("includeSeries") == "true"
		includeEpisode := r.URL.Query().Get("includeEpisode") == "true"
		writeHistoryEntries(r.Context(), w, rows, svc, includeSeries, includeEpisode)
	})
}

func parseEventTypeQuery(w http.ResponseWriter, r *http.Request) (*history.EventType, bool) {
	raw := r.URL.Query().Get("eventType")
	if raw == "" {
		return nil, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		httperr.BadRequest(w, fmt.Sprintf("history: invalid eventType %q", raw))
		return nil, false
	}
	v := history.EventType(n)
	return &v, true
}

// HistoryEntryPopulator is the optional-extension interface for the
// HistorySince + HistorySeries handlers. When the HistoryRangeReader
// passed in ALSO satisfies this interface (idiomatic Go type-
// assertion), writeHistoryEntries invokes PopulateHistoryEntry
// per row to compute the matched customFormats[] + customFormatScore
// pair. When the passed-in svc does NOT implement this interface,
// the handlers fall back to the empty stub (CustomFormats: []; score
// 0) — preserves the pre-J338 wire shape + the existing test
// fixtures' nil-populator path.
//
// Production wiring at cmd/sonarr composes *history.Service with a
// wrapper that ALSO implements PopulateHistoryEntry — the wrapper
// re-uses the J336 computeHistoryCustomFormats helper + per-request
// cache primitives. Empty-DB shadow traffic is unaffected; populated
// rows surface the matched customFormats list.
//
// J338.
type HistoryEntryPopulator interface {
	PopulateHistoryEntry(ctx context.Context, e *history.Entry) (customFormats []CustomFormatResource, customFormatScore int)
}

// HistorySeriesEmbedder + HistoryEpisodeEmbedder are the optional-
// extension interfaces backing the includeSeries / includeEpisode
// branches on the history since/series endpoints. C#
// `HistoryController.MapToResource` sets `resource.Series =
// model.Series.ToResource()` (BASE series ToResource) and
// `resource.Episode = model.Episode.ToResource()` (BASE episode
// ToResource) when the respective flags are set. The Go history.Entry
// carries only SeriesID/EpisodeID ints, so the embed needs a by-id
// lookup; the production cmd/sonarr wrapper implements these by loading
// the row and projecting via ToSeriesResource / ToEpisodeResource. Test
// fixtures don't implement them, so the embeds stay nil (key absent).
//
// J377.
type HistorySeriesEmbedder interface {
	HistorySeriesResource(ctx context.Context, seriesID int) *SeriesResource
}

type HistoryEpisodeEmbedder interface {
	HistoryEpisodeResource(ctx context.Context, episodeID int) *EpisodeResource
}

// HistoryQualityCutoffPopulator computes the qualityCutoffNotMet flag for
// a history row. C# `HistoryController.MapToResource` sets it whenever
// `model.Series != null` via
// `_upgradableSpecification.QualityCutoffNotMet(model.Series.QualityProfile
// .Value, model.Quality)` — unconditional, NOT gated by includeSeries.
// The production cmd/sonarr wrapper implements it (decode the row's
// Quality, load the series' profile, run the ported upgradable spec); a
// missing dep / lookup failure degrades to false. Test fixtures don't
// implement it, so the flag stays the false default.
//
// J378 — the history counterpart to the J375 EpisodeFile cutoff.
type HistoryQualityCutoffPopulator interface {
	PopulateHistoryQualityCutoffNotMet(ctx context.Context, e *history.Entry) bool
}

// writeHistoryEntries projects history rows to the wire. svc is the
// handler's reader value (typed `any`): it is type-asserted for the
// HistoryEntryPopulator (customFormats) + — when includeSeries /
// includeEpisode are set — the HistorySeriesEmbedder /
// HistoryEpisodeEmbedder optional extensions. The series embed is
// deduped per-request (the since/series lists repeat series ids).
//
// J377: includeSeries / includeEpisode embeds added (the paged
// HistoryHandler uses a different lister-projected path and is unaffected).
func writeHistoryEntries(ctx context.Context, w http.ResponseWriter, rows []history.Entry, svc any, includeSeries, includeEpisode bool) {
	var populator HistoryEntryPopulator
	if p, ok := svc.(HistoryEntryPopulator); ok {
		populator = p
	}
	var seriesEmbed HistorySeriesEmbedder
	if includeSeries {
		seriesEmbed, _ = svc.(HistorySeriesEmbedder)
	}
	var episodeEmbed HistoryEpisodeEmbedder
	if includeEpisode {
		episodeEmbed, _ = svc.(HistoryEpisodeEmbedder)
	}
	// J378: qualityCutoffNotMet is computed unconditionally (NOT flag-
	// gated) — C# MapToResource sets it whenever model.Series != null.
	var cutoffPop HistoryQualityCutoffPopulator
	if c, ok := svc.(HistoryQualityCutoffPopulator); ok {
		cutoffPop = c
	}
	seriesCache := map[int]*SeriesResource{}
	out := make([]HistoryResource, 0, len(rows))
	for _, e := range rows {
		entry := e // copy for pointer capture
		eventTypeName := ""
		if e.EventType != nil {
			eventTypeName = history.EventType(*e.EventType).String()
		}
		// J338 populated-mapper invocation: if populator non-nil,
		// invoke per-row; otherwise fall back to empty stub.
		cf := []CustomFormatResource{}
		score := 0
		if populator != nil {
			cf, score = populator.PopulateHistoryEntry(ctx, &entry)
			if cf == nil {
				cf = []CustomFormatResource{}
			}
		}
		res := HistoryResource{
			ID:                e.ID,
			SeriesID:          e.SeriesID,
			EpisodeID:         e.EpisodeID,
			SourceTitle:       e.SourceTitle,
			Date:              e.Date.UTC().Format("2006-01-02T15:04:05Z"),
			DownloadID:        e.DownloadID,
			EventType:         eventTypeName,
			Languages:         ToLanguageRefs(e.Languages),
			Data:              e.Data,
			CustomFormats:     cf,
			CustomFormatScore: score,
		}
		if seriesEmbed != nil && e.SeriesID != 0 {
			if cached, ok := seriesCache[e.SeriesID]; ok {
				res.Series = cached
			} else if sr := seriesEmbed.HistorySeriesResource(ctx, e.SeriesID); sr != nil {
				res.Series = sr
				seriesCache[e.SeriesID] = sr
			}
		}
		if episodeEmbed != nil && e.EpisodeID != 0 {
			if er := episodeEmbed.HistoryEpisodeResource(ctx, e.EpisodeID); er != nil {
				res.Episode = er
			}
		}
		if cutoffPop != nil {
			res.QualityCutoffNotMet = cutoffPop.PopulateHistoryQualityCutoffNotMet(ctx, &entry)
		}
		out = append(out, res)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	buf, _ := json.Marshal(out)
	_, _ = w.Write(buf)
}

// FailedDownloadMarker is the narrow MarkAsFailed surface the POST
// handler reaches for. Satisfied by *faileddownloads.Service.
type FailedDownloadMarker interface {
	MarkAsFailed(ctx context.Context, historyID int) error
}

// HistoryMarkAsFailedHandler returns POST /api/v3/history/failed/{id}.
// Mirrors `HistoryController.MarkAsFailed` at `:131-136`:
//
//	_failedDownloadService.MarkAsFailed(id);
//	return new { };
//
// The Go-port delegates to the same MarkAsFailed implementation the
// scheduler / IHandle path would invoke. history.ErrNotFound surfaces
// as 404.
func HistoryMarkAsFailedHandler(svc FailedDownloadMarker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "history: invalid id")
			return
		}
		if err := svc.MarkAsFailed(r.Context(), id); err != nil {
			if errors.Is(err, history.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("history %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("history: markAsFailed %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}
