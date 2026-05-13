package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
			http.Error(w, fmt.Sprintf("blocklist: %v", err), http.StatusInternalServerError)
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
