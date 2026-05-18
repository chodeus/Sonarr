package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/httperr"
)

// HistoryResource is the per-record shape in the
// PagingResource[HistoryResource] envelope. Mirrors
// `src/Sonarr.Api.V3/History/HistoryResource.cs`.
//
// Skeleton shape only — populated-row mapping (eventType enum,
// languages/quality slices, data dictionary, optional series/episode
// when includeSeries/includeEpisode=true) lands with the paged
// surface on history.Service (DEFERRED). Empty-DB golden has zero
// records so the gap is unobservable today.
type HistoryResource struct {
	ID          int    `json:"id"`
	SeriesID    int    `json:"seriesId"`
	EpisodeID   int    `json:"episodeId"`
	SourceTitle string `json:"sourceTitle"`
	Date        string `json:"date"`
	DownloadID  string `json:"downloadId"`
	EventType   string `json:"eventType"`
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
