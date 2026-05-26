package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/importlistexclusions"
	"sonarr.tv/sonarr/internal/validation"
)

// ImportListExclusionResource mirrors
// `src/Sonarr.Api.V3/ImportLists/ImportListExclusionResource.cs`.
// Three plain fields plus the inherited Id; the wire shape is a
// direct passthrough of the persisted Exclusion model.
//
// Field-source mapping (C# → Go):
//
//	id      → Exclusion.ID
//	tvdbId  → Exclusion.TvdbID
//	title   → Exclusion.Title
type ImportListExclusionResource struct {
	ID     int    `json:"id"`
	TvdbID int    `json:"tvdbId"`
	Title  string `json:"title"`
}

// ImportListExclusionBulkResource mirrors C#
// `src/Sonarr.Api.V3/ImportLists/ImportListExclusionBulkResource.cs`:
// `{ ids: HashSet<int> }`. DELETE bulk reads this shape from the body.
type ImportListExclusionBulkResource struct {
	Ids []int `json:"ids"`
}

// ImportListExclusionStore is the persistence surface — the four
// CRUD operations + the TvdbId-uniqueness lookup that
// ImportListExclusionExistsValidator runs in C#.
//
// *importlistexclusions.Repo satisfies this directly via the embedded
// *repo.Repository[Exclusion] (All/Get/Insert/Update/Delete) plus
// the package-local GetByTvdbID helper.
type ImportListExclusionStore interface {
	All(ctx context.Context) ([]importlistexclusions.Exclusion, error)
	Get(ctx context.Context, id int) (*importlistexclusions.Exclusion, error)
	Insert(ctx context.Context, e *importlistexclusions.Exclusion) (*importlistexclusions.Exclusion, error)
	Update(ctx context.Context, e *importlistexclusions.Exclusion) error
	Delete(ctx context.Context, id int) error
	GetByTvdbID(ctx context.Context, tvdbID int) (*importlistexclusions.Exclusion, error)
}

// ImportListExclusionHandler returns GET /api/v3/importlistexclusion.
// Mirrors `GetImportListExclusions` at
// `src/Sonarr.Api.V3/ImportLists/ImportListExclusionController.cs:37-43`.
// The C# method is marked [Obsolete("Deprecated")] in favor of the
// paged endpoint, but the route still serves the flat list shape —
// the Go port mirrors that.
func ImportListExclusionHandler(store ImportListExclusionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlistexclusions: %w", err), nil)
			return
		}
		out := make([]ImportListExclusionResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, ImportListExclusionResource{ID: row.ID, TvdbID: row.TvdbID, Title: row.Title})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// ImportListExclusionPagedHandler returns GET /api/v3/importlistexclusion/paged.
// Mirrors `GetImportListExclusionsPaged` at
// `src/Sonarr.Api.V3/ImportLists/ImportListExclusionController.cs:45-61`.
// C# allows sort keys "id", "title", "tvdbId" (case-insensitive) and
// defaults to "id" desc.
//
// The Go port does in-memory paging (Repo.All + slice) — matching the
// blocklist/history adapter posture. Sort honours the three documented
// keys; unknown keys fall back to id-desc. The C# query supports
// page/pageSize/sortKey/sortDirection via the standard PagingRequest
// query params.
func ImportListExclusionPagedHandler(store ImportListExclusionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "id")
		if req.SortDirection == "" {
			req.SortDirection = "descending"
		}
		rows, err := store.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlistexclusions/paged: %w", err), nil)
			return
		}
		// Sort by the requested key (case-insensitive). Unknown keys
		// fall back to id.
		key := strings.ToLower(req.SortKey)
		desc := strings.EqualFold(req.SortDirection, "descending") || strings.EqualFold(req.SortDirection, "desc")
		sort.SliceStable(rows, func(i, j int) bool {
			var less bool
			switch key {
			case "title":
				less = strings.ToLower(rows[i].Title) < strings.ToLower(rows[j].Title)
			case "tvdbid":
				less = rows[i].TvdbID < rows[j].TvdbID
			default:
				less = rows[i].ID < rows[j].ID
			}
			if desc {
				return !less
			}
			return less
		})
		total := len(rows)
		start := (req.Page - 1) * req.PageSize
		if start < 0 {
			start = 0
		}
		if start > total {
			start = total
		}
		end := start + req.PageSize
		if end > total {
			end = total
		}
		page := rows[start:end]
		records := make([]ImportListExclusionResource, 0, len(page))
		for _, row := range page {
			records = append(records, ImportListExclusionResource{ID: row.ID, TvdbID: row.TvdbID, Title: row.Title})
		}
		resp := PagingResource[ImportListExclusionResource]{
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

// ImportListExclusionByIDHandler returns GET /api/v3/importlistexclusion/{id}.
// Mirrors `GetResourceById` at `:32-35` via the RestController<T> base.
func ImportListExclusionByIDHandler(store ImportListExclusionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseImportListExclusionID(w, r)
		if !ok {
			return
		}
		e, err := store.Get(r.Context(), id)
		if errors.Is(err, importlistexclusions.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("importlistexclusion %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlistexclusions: %w", err), nil)
			return
		}
		writeImportListExclusionJSON(w, http.StatusOK, *e)
	})
}

// ImportListExclusionCreateHandler returns POST /api/v3/importlistexclusion.
// Mirrors `AddImportListExclusion` at `:63-70`. 201 + body.
//
// Validation ports the C# SharedValidator chain at :25-29:
//   - TvdbId NotEmpty (NotEmpty on int means != 0)
//   - TvdbId not already exclusion (via importListExclusionExistsValidator)
//   - Title NotEmpty
func ImportListExclusionCreateHandler(store ImportListExclusionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeImportListExclusionBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlistexclusion: malformed body: %v", err))
			return
		}
		if errs := validateImportListExclusion(r.Context(), store, body, 0); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		inserted, err := store.Insert(r.Context(), &importlistexclusions.Exclusion{
			TvdbID: body.TvdbID,
			Title:  body.Title,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlistexclusions: %w", err), nil)
			return
		}
		writeImportListExclusionJSON(w, http.StatusCreated, *inserted)
	})
}

// ImportListExclusionUpdateHandler returns PUT /api/v3/importlistexclusion/{id}.
// Mirrors `UpdateImportListExclusion` at `:72-78`. Path id wins.
func ImportListExclusionUpdateHandler(store ImportListExclusionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseImportListExclusionID(w, r)
		if !ok {
			return
		}
		body, err := decodeImportListExclusionBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlistexclusion: malformed body: %v", err))
			return
		}
		body.ID = id
		if errs := validateImportListExclusion(r.Context(), store, body, id); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := importlistexclusions.Exclusion{ID: id, TvdbID: body.TvdbID, Title: body.Title}
		err = store.Update(r.Context(), &model)
		if errors.Is(err, importlistexclusions.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("importlistexclusion %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlistexclusions: %w", err), nil)
			return
		}
		writeImportListExclusionJSON(w, http.StatusAccepted, model)
	})
}

// ImportListExclusionDeleteHandler returns DELETE /api/v3/importlistexclusion/{id}.
// Mirrors `DeleteImportListExclusion` at `:80-84`. 200 empty / 404.
func ImportListExclusionDeleteHandler(store ImportListExclusionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseImportListExclusionID(w, r)
		if !ok {
			return
		}
		// Idempotent DELETE — C.1.i-follow-4 alignment (J232). C# returns 200 on missing id.
		if err := store.Delete(r.Context(), id); err != nil && !errors.Is(err, importlistexclusions.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlistexclusions: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// ImportListExclusionBulkDeleteHandler returns DELETE /api/v3/importlistexclusion/bulk.
// Mirrors `DeleteImportListExclusions` at `:86-93`. 200 with `{}` body
// matching the C# `return new { }` literal.
//
// Per-id deletion in a loop; first ErrNotFound short-circuits to 404
// matching the C# `_importListExclusionService.Delete(ids.ToList())`
// throwing ModelNotFoundException on any missing id.
func ImportListExclusionBulkDeleteHandler(store ImportListExclusionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body ImportListExclusionBulkResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlistexclusion: malformed bulk body: %v", err))
			return
		}
		for _, id := range body.Ids {
			err := store.Delete(r.Context(), id)
			if errors.Is(err, importlistexclusions.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("importlistexclusion %d not found", id))
				return
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlistexclusions: delete %d: %w", id, err), nil)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
}

// ---------- helpers ----------

func parseImportListExclusionID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "importlistexclusion: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("importlistexclusion: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

func decodeImportListExclusionBody(r *http.Request) (*ImportListExclusionResource, error) {
	var t ImportListExclusionResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateImportListExclusion ports the C# SharedValidator chain:
//
//   - TvdbId NotEmpty (.NotEmpty() on int means != 0)
//   - TvdbId not already excluded by another row (via
//     importListExclusionExistsValidator — the Go port reads
//     GetByTvdbID directly and skips when the match IS the row being
//     updated, matching the C# validator's id-aware exclusion)
//   - Title NotEmpty
//
// excludeID is the URL id on Update (skip the row being updated when
// the uniqueness lookup hits it); 0 on Create.
func validateImportListExclusion(ctx context.Context, store ImportListExclusionStore, res *ImportListExclusionResource, excludeID int) *validation.Errors {
	errs := validation.New()
	if res.TvdbID == 0 {
		errs.Add("TvdbId", "'TvdbId' must not be empty")
	}
	errs.Required("Title", res.Title)
	if errs.Len() > 0 {
		return errs
	}
	// TvdbId-uniqueness lookup. ErrNotFound is the happy path (no
	// existing row → uniqueness holds). A hit on a different id is
	// the rejection case.
	existing, err := store.GetByTvdbID(ctx, res.TvdbID)
	if err != nil && !errors.Is(err, importlistexclusions.ErrNotFound) {
		// Lookup failure surfaces as a server-side error; treat the
		// rule as passing so the Insert/Update can surface its own
		// failure (the wire shape stays consistent — 500 not 400).
		return errs
	}
	if existing != nil && existing.ID != excludeID {
		errs.Add("TvdbId", "This TVDB id is already excluded")
	}
	return errs
}

func writeImportListExclusionJSON(w http.ResponseWriter, status int, e importlistexclusions.Exclusion) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(ImportListExclusionResource{ID: e.ID, TvdbID: e.TvdbID, Title: e.Title})
	_, _ = w.Write(buf)
}
