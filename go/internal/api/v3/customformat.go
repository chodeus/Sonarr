package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/httperr"
)

// CustomFormatResource mirrors
// `src/Sonarr.Api.V3/CustomFormats/CustomFormatResource.cs`. The C#
// class re-declares Id with `[JsonIgnore(Never)]` so the field is
// emitted even when zero — mirrored here with `json:"id"` (no
// omitempty).
//
// includeDetails: the C# controller calls `ToResource(true)` so
// IncludeCustomFormatWhenRenaming + Specifications always populate.
// The `?` on the C# `bool?` field plus STJson.WhenWritingNull means
// a null source value omits the field; the Go port uses *bool with
// omitempty for the same effect.
//
// Nullable-vs-zero-value note: customformats.Format stores
// IncludeCustomFormatWhenRenaming as a non-nullable bool, so "false"
// and "unset" are conflated upstream. For empty-DB the mapper never
// runs and the wire shape is `[]` either way, but a future slice
// with seeded custom formats may need to lift Format.IncludeCustom...
// to *bool to preserve the C# null-vs-false distinction (DEFERRED;
// recorded in STATUS line 9 as a follow-up).
type CustomFormatResource struct {
	ID                              int                               `json:"id"`
	Name                            string                            `json:"name"`
	IncludeCustomFormatWhenRenaming *bool                             `json:"includeCustomFormatWhenRenaming,omitempty"`
	Specifications                  []CustomFormatSpecificationSchema `json:"specifications"`
}

// CustomFormatSpecificationSchema mirrors the C# wire shape for one
// element of CustomFormatResource.Specifications. Same schema-builder
// dependency as AutoTaggingSpecificationSchema — the Fields slice
// holds the polymorphic ClientSchema.Field list, populated by a
// future slice once the reflection layer ports.
//
// TODO (DEFERRED): map customformats.Specification → Schema once the
// schema-builder package lands. Empty-DB golden has zero rows so the
// gap is unobservable today.
type CustomFormatSpecificationSchema struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Implementation     string `json:"implementation"`
	ImplementationName string `json:"implementationName"`
	Negate             bool   `json:"negate"`
	Required           bool   `json:"required"`
	Fields             []any  `json:"fields"`
}

// CustomFormatLister is the read side the handler depends on.
// *customformats.Service satisfies it via All.
type CustomFormatLister interface {
	All(ctx context.Context) ([]*customformats.Format, error)
}

// CustomFormatHandler returns the http.Handler for
// GET /api/v3/customformat. Mirrors
// `CustomFormatController.GetAll` at
// `src/Sonarr.Api.V3/CustomFormats/CustomFormatController.cs` (the
// list call always passes `includeDetails: true`).
func CustomFormatHandler(lister CustomFormatLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
			return
		}
		out := make([]CustomFormatResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toCustomFormatResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toCustomFormatResource(f *customformats.Format) CustomFormatResource {
	if f == nil {
		return CustomFormatResource{}
	}
	// Wrap the non-nullable bool into a *bool so the wire shape can
	// match C#'s "always emit when includeDetails=true" — once the
	// upstream Format gains true nullability, this becomes a direct
	// passthrough.
	include := f.IncludeCustomFormatWhenRenaming
	return CustomFormatResource{
		ID:                              f.ID,
		Name:                            f.Name,
		IncludeCustomFormatWhenRenaming: &include,
		// TODO (DEFERRED): populate Specifications from f.Specifications
		// once the schema-builder port lands. Empty slice keeps the wire
		// shape stable until then.
		Specifications: []CustomFormatSpecificationSchema{},
	}
}
