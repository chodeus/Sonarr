package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/tags"
)

// TagResource is the JSON shape produced by GET /api/v3/tag. Mirrors
// `src/Sonarr.Api.V3/Tags/TagResource.cs`.
//
// Field-source mapping (C# → Go):
//
//	id    → Tag.Id    (passthrough)
//	label → Tag.Label (passthrough, lowercase by the time it's stored)
type TagResource struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

// TagLister is the read side of tags the handler depends on.
// *tags.Service satisfies it via its existing All method.
type TagLister interface {
	All(ctx context.Context) ([]tags.Tag, error)
}

// TagHandler returns the http.Handler for GET /api/v3/tag. Mirrors
// `TagController.GetAll` at `src/Sonarr.Api.V3/Tags/TagController.cs:40-45`.
// tags.Service.All sorts by Label (case-insensitive) before returning;
// the handler relays that ordering as-is.
//
// The output slice is allocated with len==0 capacity==len(rows) so an
// empty result emits `[]`, never `null` — C# `List<TagResource>` always
// serialises as `[]` for empty collections and the byte-equality
// contract depends on that.
func TagHandler(lister TagLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("tags: %v", err), http.StatusInternalServerError)
			return
		}
		out := make([]TagResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, TagResource{ID: row.ID, Label: row.Label})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}
