package v3

import (
	"net/http"
	"strconv"
	"strings"
)

// PagingResource is the generic envelope every paginated V3 list
// endpoint returns. Mirrors
// `src/Sonarr.Http/PagingResource.cs:18-27`.
//
// Records is non-omitempty: C# `List<TResource> Records = new()` always
// emits as `[]` when empty. Handlers MUST allocate
// `make([]T, 0, len(records))` rather than relying on nil — JSON-marshal
// of a nil slice produces `null` and breaks byte-equality.
type PagingResource[T any] struct {
	Page          int    `json:"page"`
	PageSize      int    `json:"pageSize"`
	Records       []T    `json:"records"`
	SortDirection string `json:"sortDirection"`
	SortKey       string `json:"sortKey"`
	TotalRecords  int    `json:"totalRecords"`
}

// PagingRequest is the parsed shape of the four `[FromQuery]` params
// every paged endpoint accepts. Mirrors
// `src/Sonarr.Http/PagingResource.cs:6-13` (PagingRequestResource)
// with one normalisation: the C# `SortDirection?` enum becomes the
// JsonStringEnumConverter+CamelCase lowercase form ("ascending" /
// "descending") for direct passthrough to PagingResource.SortDirection.
//
// Page / PageSize use the C# DefaultValue(1) / DefaultValue(10)
// guards. SortKey and SortDirection fall back to controller-supplied
// defaults — they're applied in parsePagingRequest only when the
// query is missing or empty.
type PagingRequest struct {
	Page          int
	PageSize      int
	SortKey       string
	SortDirection string
}

// parsePagingRequest reads the four standard paging query params and
// merges them with controller-supplied defaults. Returns a fully
// resolved PagingRequest ready to hand off to the service.
//
// Defaults:
//   - page: 1 (C# DefaultValue(1))
//   - pageSize: 10 (C# DefaultValue(10))
//   - sortKey: defaultSortKey arg (controller-specific)
//   - sortDirection: "descending" (C# `?? SortDirection.Descending` at
//     PagingResource.cs:35)
//
// Invalid integer params silently fall back to defaults — matches
// ASP.NET model binding's tolerant behaviour for `int?` query params.
func parsePagingRequest(r *http.Request, defaultSortKey string) PagingRequest {
	q := r.URL.Query()
	out := PagingRequest{
		Page:          1,
		PageSize:      10,
		SortKey:       defaultSortKey,
		SortDirection: "descending",
	}
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			out.Page = n
		}
	}
	if v := q.Get("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			out.PageSize = n
		}
	}
	if v := strings.TrimSpace(q.Get("sortKey")); v != "" {
		out.SortKey = v
	}
	if v := strings.TrimSpace(q.Get("sortDirection")); v != "" {
		out.SortDirection = strings.ToLower(v)
	}
	return out
}

// emptyPagingResponse builds a PagingResource[T] with the supplied
// request applied and zero records. Helper used both by handlers
// short-circuiting on empty results and by tests verifying the
// default-shape wire output.
func emptyPagingResponse[T any](req PagingRequest) PagingResource[T] {
	return PagingResource[T]{
		Page:          req.Page,
		PageSize:      req.PageSize,
		Records:       make([]T, 0),
		SortDirection: req.SortDirection,
		SortKey:       req.SortKey,
		TotalRecords:  0,
	}
}
