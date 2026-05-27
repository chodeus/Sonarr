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
	"sonarr.tv/sonarr/internal/indexers"
)

// BlocklistResource is the per-record shape in the
// PagingResource[BlocklistResource] envelope. Mirrors
// `src/Sonarr.Api.V3/Blocklist/BlocklistResource.cs`.
//
// Reduced wire-shape skeleton — populated-row mapping (languages /
// quality / series subresource) lands with the paged surface on the
// Go blocklist.Service (DEFERRED). The customFormats[] field is wired
// at the struct level here (J297, Wave 1 calculator wire-through
// slice 2) so future populated-row mappers can emit the C# wire
// shape without re-widening; the underlying calculator engine
// ([sonarr.tv/sonarr/internal/customformats.Calculate] +
// [sonarr.tv/sonarr/internal/customformats/calculate_blocklist.go:ParseCustomFormatForBlocklist])
// is already ported. Empty-DB golden has zero records so the gap
// is unobservable today; constructors should default CustomFormats
// to `[]CustomFormatResource{}` (not nil) so a future populated
// row without matched formats still emits `customFormats: []`
// rather than `null` — matches the C# Newtonsoft default for
// non-null `List<CustomFormatResource>`. Note: Blocklist carries
// `customFormats` ONLY (no `customFormatScore` field, unlike
// HistoryResource).
//
// Scalar-field widening (J311) covers four C# fields that are
// already available on the persisted `blocklist.Entry` row but
// weren't projected to the wire: EpisodeIDs ([]int — always emit
// per C# `required List<int>` on v5 / non-nullable on v3); Protocol
// (DownloadProtocol enum wired as camelCase string per STJson's
// global `JsonStringEnumConverter(CamelCase)` — "usenet" / "torrent"
// / "unknown"; always emit, default "unknown" when persisted
// Protocol is nil/0); Indexer + Message (nullable strings — use
// omitempty to match `JsonIgnoreCondition.WhenWritingNull` for
// v5's `string?` semantic; v3 STJson same posture). Languages,
// Quality, EpisodeIds-as-subresource, and Series subresource remain
// DEFERRED — each requires either a typed wire DTO (Quality is
// complex) or a service join (Series).
//
// Note: the Go-port carries a `title` field that does NOT exist on
// either v3 or v5 C# BlocklistResource. Preserved unmodified to
// avoid a wire-break for any third-party client that already reads
// it; future contract-replay re-baseline (per C.1.i-full) can
// decide whether to drop it.
type BlocklistResource struct {
	ID            int                    `json:"id"`
	SeriesID      int                    `json:"seriesId"`
	EpisodeIDs    []int                  `json:"episodeIds"`
	Title         string                 `json:"title"`
	Date          string                 `json:"date"`
	SourceTitle   string                 `json:"sourceTitle"`
	Protocol      string                 `json:"protocol"`
	Indexer       string                 `json:"indexer,omitempty"`
	Message       string                 `json:"message,omitempty"`
	CustomFormats []CustomFormatResource `json:"customFormats"`
}

// BlocklistProtocolName returns the camelCase wire form of a
// persisted DownloadProtocol value matching C#'s
// `JsonStringEnumConverter(CamelCase)` projection. Nil or unknown
// values surface as `"unknown"` (matches C# enum default-zero name
// under `Enum.GetName(DownloadProtocol.Unknown)`).
//
// Exported so cmd/sonarr's blocklistPagedAdapter (the production
// paged consumer) can share the same mapping rather than duplicate
// it. The history+queue adapters can reuse it when their paged
// surfaces start emitting protocol too.
func BlocklistProtocolName(persistedProtocol *int) string {
	if persistedProtocol == nil {
		return "unknown"
	}
	return indexers.DownloadProtocol(*persistedProtocol).String()
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
		// Idempotent DELETE — C.1.i-follow-4 alignment (J232). Live C# v5-develop 7c41dcbe2
		// `DELETE /api/v3/blocklist/99999` → 200 + `{}` whether the id existed or not.
		if err := svc.Delete(r.Context(), id); err != nil && !errors.Is(err, blocklist.ErrNotFound) {
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
