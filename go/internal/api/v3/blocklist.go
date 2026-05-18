package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/httperr"
)

// BlocklistResource is the per-record shape in the
// PagingResource[BlocklistResource] envelope. Mirrors
// `src/Sonarr.Api.V3/Blocklist/BlocklistResource.cs`.
//
// This slice ships the wire-shape skeleton; populated-row mapping
// awaits a follow-up that lands the paged surface on the Go
// blocklist.Service. The empty-DB golden has zero records so the
// gap is unobservable today (DEFERRED — STATUS line 9 carries a
// note).
type BlocklistResource struct {
	ID          int    `json:"id"`
	SeriesID    int    `json:"seriesId"`
	Title       string `json:"title"`
	Date        string `json:"date"`
	SourceTitle string `json:"sourceTitle"`
}

// BlocklistPagedLister is the read side the handler depends on. The
// production Go *blocklist.Service does not yet expose a Paged
// surface, so the contract test substitutes an empty fixture. When
// the paged port lands, *blocklist.Service grows a method that
// satisfies this interface and the production wiring takes effect.
type BlocklistPagedLister interface {
	PagedBlocklist(ctx context.Context, req PagingRequest) ([]BlocklistResource, int, error)
}

// BlocklistHandler returns the http.Handler for GET /api/v3/blocklist.
// Mirrors `BlocklistController.GetBlocklist` at
// `src/Sonarr.Api.V3/Blocklist/BlocklistController.cs:14-35`.
// Default sortKey is "date" (controller hard-codes it); default
// direction "descending" comes from the shared paging-request
// defaults.
func BlocklistHandler(lister BlocklistPagedLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "date")
		records, total, err := lister.PagedBlocklist(r.Context(), req)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("blocklist: %w", err), nil)
			return
		}
		if records == nil {
			records = []BlocklistResource{}
		}
		resp := PagingResource[BlocklistResource]{
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

// BlocklistWriter is the slice of *blocklist.Service the DELETE
// handlers reach for. Defined locally for stubbable tests.
type BlocklistWriter interface {
	Delete(ctx context.Context, id int) error
	DeleteByIDs(ctx context.Context, ids []int) (int64, error)
}

// blocklistBulkResource mirrors C# `BlocklistBulkResource` — `{"ids": [...]}`.
type blocklistBulkResource struct {
	IDs []int `json:"ids"`
}

// BlocklistDeleteHandler returns DELETE /api/v3/blocklist/{id}.
// Mirrors `BlocklistController.DeleteBlocklist` at `:57-61`.
// C# uses `[RestDeleteById]` which returns 200 on success, 404 when
// the id doesn't exist (the underlying service throws ModelNotFoundException).
func BlocklistDeleteHandler(svc BlocklistWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "blocklist: invalid id")
			return
		}
		if err := svc.Delete(r.Context(), id); err != nil {
			if errors.Is(err, blocklist.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("blocklist %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("blocklist: delete %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// BlocklistBulkDeleteHandler returns DELETE /api/v3/blocklist/bulk.
// Mirrors `BlocklistController.Remove` at `:63-70`. C# returns
// `{ }` (empty object literal); the Go port emits the same.
// Empty `ids` is accepted (matches C# which silently no-ops on
// empty input — the underlying `Delete(List<int>)` short-circuits).
func BlocklistBulkDeleteHandler(svc BlocklistWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body blocklistBulkResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("blocklist/bulk: malformed body: %v", err))
			return
		}
		if _, err := svc.DeleteByIDs(r.Context(), body.IDs); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("blocklist/bulk: %w", err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}
