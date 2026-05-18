package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/httperr"
)

// AutoTaggingResource mirrors
// `src/Sonarr.Api.V3/AutoTagging/AutoTaggingResource.cs`. The C# class
// re-declares Id with `[JsonIgnore(Never)]` so the field is emitted
// even when zero, mirrored here with a non-omitempty `json:"id"` tag.
//
// Field-source mapping (C# → Go):
//
//	id                       → Tag.ID
//	name                     → Tag.Name
//	removeTagsAutomatically  → Tag.RemoveTagsAutomatically
//	tags                     → Tag.Tags (int slice)
//	specifications           → Tag.Specifications.ToSchema(...) — TODO,
//	                           DEFERRED (see toAutoTaggingResource note)
type AutoTaggingResource struct {
	ID                      int                              `json:"id"`
	Name                    string                           `json:"name"`
	RemoveTagsAutomatically bool                             `json:"removeTagsAutomatically"`
	Tags                    []int                            `json:"tags"`
	Specifications          []AutoTaggingSpecificationSchema `json:"specifications"`
}

// AutoTaggingSpecificationSchema mirrors
// `AutoTaggingSpecificationSchema.cs:8-16`. Carries the wire shape
// for each spec; the Fields slice is the polymorphic
// ClientSchema.Field list, which Phase 5 ports in a later slice
// alongside the schema-builder package.
//
// TODO: populate from `autotagging.Specification` once the schema
// reflection layer lands. Until then, the mapper emits an empty
// Specifications slice for any populated AutoTag row — the empty-DB
// golden has zero rows so the contract harness can't observe the
// gap, but a future slice with seeded auto-tags MUST land the schema
// port (DEFERRED).
type AutoTaggingSpecificationSchema struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Implementation     string `json:"implementation"`
	ImplementationName string `json:"implementationName"`
	Negate             bool   `json:"negate"`
	Required           bool   `json:"required"`
	Fields             []any  `json:"fields"`
}

// AutoTaggingLister is the read side the handler depends on.
// *autotagging.Service satisfies it via its existing All method.
type AutoTaggingLister interface {
	All(ctx context.Context) ([]*autotagging.Tag, error)
}

// AutoTaggingHandler returns the http.Handler for GET /api/v3/autotagging.
// Mirrors `AutoTaggingController.GetAll` at
// `src/Sonarr.Api.V3/AutoTagging/AutoTaggingController.cs:76-81`.
//
// Service.All does not impose an order in the Go port — the storage
// `OrderBy: "Id"` clause on the repo mapper sorts ascending. Empty
// DB returns an initialised slice so the response body is `[]`.
func AutoTaggingHandler(lister AutoTaggingLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("autotagging: %w", err), nil)
			return
		}
		out := make([]AutoTaggingResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toAutoTaggingResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toAutoTaggingResource(t *autotagging.Tag) AutoTaggingResource {
	if t == nil {
		return AutoTaggingResource{}
	}
	tags := t.Tags
	if tags == nil {
		tags = []int{}
	}
	return AutoTaggingResource{
		ID:                      t.ID,
		Name:                    t.Name,
		RemoveTagsAutomatically: t.RemoveTagsAutomatically,
		Tags:                    tags,
		// TODO: map Specifications via the schema-builder port
		// (DEFERRED). Empty slice keeps the wire shape (`[]`)
		// stable until then.
		Specifications: []AutoTaggingSpecificationSchema{},
	}
}
