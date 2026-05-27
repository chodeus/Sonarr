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
type HistoryPagedLister interface {
	PagedHistory(ctx context.Context, req PagingRequest) ([]HistoryResource, int, error)
}

// HistoryHandler returns the http.Handler for GET /api/v3/history.
// Mirrors `HistoryController.GetHistory` at
// `src/Sonarr.Api.V3/History/HistoryController.cs:14-35`. Default
// sortKey is "date"; direction defaults to "descending".
//
// The C# version also accepts eventType / episodeId / downloadId /
// seriesIds / languages / quality filters. Filter handling lands
// with the paged-service port (DEFERRED).
func HistoryHandler(lister HistoryPagedLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "date")
		records, total, err := lister.PagedHistory(r.Context(), req)
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
// `includeEpisode` flags are accepted but DEFERRED (resource shape
// doesn't yet model the joined Series/Episode payloads).
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
		writeHistoryEntries(w, rows)
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
		writeHistoryEntries(w, rows)
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

func writeHistoryEntries(w http.ResponseWriter, rows []history.Entry) {
	out := make([]HistoryResource, 0, len(rows))
	for _, e := range rows {
		eventTypeName := ""
		if e.EventType != nil {
			eventTypeName = history.EventType(*e.EventType).String()
		}
		out = append(out, HistoryResource{
			ID:            e.ID,
			SeriesID:      e.SeriesID,
			EpisodeID:     e.EpisodeID,
			SourceTitle:   e.SourceTitle,
			Date:          e.Date.UTC().Format("2006-01-02T15:04:05Z"),
			DownloadID:    e.DownloadID,
			EventType:     eventTypeName,
			Languages:     ToLanguageRefs(e.Languages),
			Data:          e.Data,
			CustomFormats: []CustomFormatResource{},
			// CustomFormatScore: 0 — populated-row mapper landing
			// alongside the calculator wire-through will compute via
			// ParseCustomFormatForHistory + CalculateCustomFormatScore.
		})
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
