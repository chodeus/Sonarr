package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/customfilters"
)

// CustomFilterResource mirrors
// `src/Sonarr.Api.V3/CustomFilters/CustomFilterResource.cs`.
//
// The Filters field in C# is `List<ExpandoObject>` — opaque per-row
// JSON payload built by the frontend. The Go customfilters.Filter
// stores it as a raw JSON string; the mapper deserialises into
// []json.RawMessage so the wire shape is `[{...},{...}]` rather than
// a stringified blob.
//
// Empty-DB golden has zero rows; populated-row mapping (the JSON
// parse) is exercised by unit tests in this slice but not by the
// contract harness until a future fixture includes a saved filter.
type CustomFilterResource struct {
	ID      int               `json:"id"`
	Type    string            `json:"type"`
	Label   string            `json:"label"`
	Filters []json.RawMessage `json:"filters"`
}

// CustomFilterLister is the read side the handler depends on.
// *customfilters.Service satisfies it via All.
type CustomFilterLister interface {
	All(ctx context.Context) ([]customfilters.Filter, error)
}

// CustomFilterHandler returns the http.Handler for
// GET /api/v3/customfilter. Mirrors
// `CustomFilterController.GetCustomFilters` at
// `src/Sonarr.Api.V3/CustomFilters/CustomFilterController.cs`.
func CustomFilterHandler(lister CustomFilterLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("customfilters: %v", err), http.StatusInternalServerError)
			return
		}
		out := make([]CustomFilterResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toCustomFilterResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toCustomFilterResource(f customfilters.Filter) CustomFilterResource {
	r := CustomFilterResource{
		ID:    f.ID,
		Type:  f.Type,
		Label: f.Label,
	}
	// The persisted Filters column is opaque JSON. Decode into a
	// raw-message slice so the wire shape passes through the
	// browser's filter payload exactly as it was saved. A
	// malformed/empty string yields an empty slice — defensive
	// against legacy rows.
	if f.Filters != "" {
		var raw []json.RawMessage
		if err := json.Unmarshal([]byte(f.Filters), &raw); err == nil {
			r.Filters = raw
		}
	}
	if r.Filters == nil {
		r.Filters = []json.RawMessage{}
	}
	return r
}
