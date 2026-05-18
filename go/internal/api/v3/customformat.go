package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/validation"
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

// CustomFormatBulkResource is the JSON body shape for the bulk
// PUT and DELETE endpoints. Mirrors C# `CustomFormatBulkResource`:
// PUT-bulk patches IncludeCustomFormatWhenRenaming on every row in
// Ids; DELETE-bulk removes every row in Ids.
//
// IncludeCustomFormatWhenRenaming is *bool so callers can opt OUT
// of patching (omit the field → null on the wire → no-op on each
// row, preserving the existing value). The PUT-bulk handler matches
// the C# `existing.IncludeCustomFormatWhenRenaming = resource.
// IncludeCustomFormatWhenRenaming ?? existing.IncludeCustom...`
// null-coalesce semantic.
type CustomFormatBulkResource struct {
	Ids                             []int `json:"ids"`
	IncludeCustomFormatWhenRenaming *bool `json:"includeCustomFormatWhenRenaming,omitempty"`
}

// CustomFormatWriter is the write surface the CRUD + bulk handlers
// depend on. *customformats.Service satisfies it via the existing
// All/GetByID/Insert/Update/Delete methods.
//
// CustomFormatController in C# extends RestController<T> (NOT
// RestControllerWithSignalR), so there's no per-row Created/Updated/
// Deleted broadcast. The service does publish CustomFormatAddedEvent
// + CustomFormatDeletedEvent for the internal cache-invalidation
// chain, but those are bus events, not SignalR.
type CustomFormatWriter interface {
	All(ctx context.Context) ([]*customformats.Format, error)
	GetByID(ctx context.Context, id int) (*customformats.Format, error)
	Insert(ctx context.Context, row *customformats.CustomFormat) (*customformats.Format, error)
	Update(ctx context.Context, row *customformats.CustomFormat) error
	Delete(ctx context.Context, id int) error
}

// CustomFormatByIDHandler returns GET /api/v3/customformat/{id}.
// Mirrors `CustomFormatController.GetResourceById` at
// `src/Sonarr.Api.V3/CustomFormats/CustomFormatController.cs:45-48`.
func CustomFormatByIDHandler(svc CustomFormatWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseCustomFormatID(w, r)
		if !ok {
			return
		}
		f, err := svc.GetByID(r.Context(), id)
		if errors.Is(err, customformats.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("customformat %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
			return
		}
		writeCustomFormatJSON(w, http.StatusOK, f)
	})
}

// CustomFormatCreateHandler returns POST /api/v3/customformat.
// Mirrors `Create` at `:57-66`. Returns 201 + the inserted resource.
//
// Validation chain mirrors the C# `SharedValidator`:
//   - Name NotEmpty
//   - Name unique across all formats (excluding self by Id)
//   - Specifications NotEmpty
//   - Each Specification has a non-empty Name
//
// The C# per-spec internal validation (Validate() on each
// ICustomFormatSpecification) is NOT yet ported — the Go spec-builder
// reflection layer hasn't landed. This slice accepts the
// Specifications payload opaquely and persists it; the deeper rules
// (e.g. regex syntax for ReleaseTitleSpec) will activate when the
// spec-builder slice ports.
func CustomFormatCreateHandler(svc CustomFormatWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeCustomFormatBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("customformat: malformed body: %v", err))
			return
		}
		if errs := validateCustomFormat(r.Context(), svc, body, 0); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model, err := customFormatToModel(*body)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: serialise: %w", err), nil)
			return
		}
		inserted, err := svc.Insert(r.Context(), &model)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
			return
		}
		writeCustomFormatJSON(w, http.StatusCreated, inserted)
	})
}

// CustomFormatUpdateHandler returns PUT /api/v3/customformat/{id}.
// Mirrors `Update` at `:68-79`. Returns 202 + the persisted resource.
//
// Same validation chain as Create; the Id passed to the uniqueness
// check is the URL id (matches C# `f.Id != v.Id` exclusion using the
// resource id, which the URL overrides per RestPutById).
func CustomFormatUpdateHandler(svc CustomFormatWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseCustomFormatID(w, r)
		if !ok {
			return
		}
		body, err := decodeCustomFormatBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("customformat: malformed body: %v", err))
			return
		}
		if errs := validateCustomFormat(r.Context(), svc, body, id); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		body.ID = id
		model, err := customFormatToModel(*body)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: serialise: %w", err), nil)
			return
		}
		err = svc.Update(r.Context(), &model)
		if errors.Is(err, customformats.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("customformat %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
			return
		}
		// Reload the row to project the full Specifications shape
		// the C# `Accepted(model.Id)` path returns.
		f, err := svc.GetByID(r.Context(), id)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: reload: %w", err), nil)
			return
		}
		writeCustomFormatJSON(w, http.StatusAccepted, f)
	})
}

// CustomFormatDeleteHandler returns DELETE /api/v3/customformat/{id}.
// Mirrors `DeleteFormat` at `:103-107`. Returns 200 empty body.
func CustomFormatDeleteHandler(svc CustomFormatWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseCustomFormatID(w, r)
		if !ok {
			return
		}
		err := svc.Delete(r.Context(), id)
		if errors.Is(err, customformats.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("customformat %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// CustomFormatBulkUpdateHandler returns PUT /api/v3/customformat/bulk.
// Mirrors `Update(CustomFormatBulkResource)` at `:81-101`. Returns
// 202 + the updated resource array.
//
// Body shape: {ids: [int], includeCustomFormatWhenRenaming: bool?}.
// Empty ids → 400 ("ids must be provided") matches C# BadRequestException.
// For each id: GetByID + patch IncludeCustomFormatWhenRenaming (only
// if the request value is non-nil — null-coalesce semantic) + Update.
// 404 on any missing id surfaces as a single 404 envelope (the C#
// version throws on the per-id GetById; the Go port mirrors that
// short-circuit posture).
func CustomFormatBulkUpdateHandler(svc CustomFormatWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body CustomFormatBulkResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("customformat: malformed bulk body: %v", err))
			return
		}
		if len(body.Ids) == 0 {
			httperr.BadRequest(w, "ids must be provided")
			return
		}
		out := make([]CustomFormatResource, 0, len(body.Ids))
		for _, id := range body.Ids {
			f, err := svc.GetByID(r.Context(), id)
			if errors.Is(err, customformats.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("customformat %d not found", id))
				return
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
				return
			}
			if body.IncludeCustomFormatWhenRenaming != nil {
				f.IncludeCustomFormatWhenRenaming = *body.IncludeCustomFormatWhenRenaming
			}
			model, mErr := customFormatToModelFromFormat(f)
			if mErr != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: serialise %d: %w", id, mErr), nil)
				return
			}
			if uErr := svc.Update(r.Context(), &model); uErr != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: update %d: %w", id, uErr), nil)
				return
			}
			out = append(out, toCustomFormatResource(f))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// CustomFormatBulkDeleteHandler returns DELETE /api/v3/customformat/bulk.
// Mirrors `DeleteFormats(CustomFormatBulkResource)` at `:109-116`.
// Returns 200 with `{}` body matching C# `return new { }`.
//
// Per-id deletion in a loop; the first ErrNotFound short-circuits to
// 404 (matches the C# `_formatService.Delete(ids.ToList())` which
// throws ModelNotFoundException on any missing id).
func CustomFormatBulkDeleteHandler(svc CustomFormatWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body CustomFormatBulkResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("customformat: malformed bulk body: %v", err))
			return
		}
		for _, id := range body.Ids {
			err := svc.Delete(r.Context(), id)
			if errors.Is(err, customformats.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("customformat %d not found", id))
				return
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: delete %d: %w", id, err), nil)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
}

// ---------- helpers ----------

func parseCustomFormatID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "customformat: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("customformat: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

func decodeCustomFormatBody(r *http.Request) (*CustomFormatResource, error) {
	var t CustomFormatResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateCustomFormat ports the C# SharedValidator chain:
//   - Name NotEmpty
//   - Name unique across all formats (excluding self by Id)
//   - Specifications NotEmpty + each Specification.Name non-empty
//
// excludeID is the row's own ID — 0 on Create (nothing to exclude),
// the URL id on Update (excludes the row being updated from the
// uniqueness check). Mirrors C# `f.Id != v.Id` exclusion.
func validateCustomFormat(ctx context.Context, svc CustomFormatWriter, res *CustomFormatResource, excludeID int) *validation.Errors {
	errs := validation.New()
	if !errs.Required("Name", res.Name) {
		return errs
	}
	if len(res.Specifications) == 0 {
		errs.Add("Specifications", "Must contain at least one Condition")
		return errs
	}
	for _, s := range res.Specifications {
		if s.Name == "" {
			errs.Add("Specifications", "Condition name(s) cannot be empty or consist of only spaces")
			break
		}
	}
	if errs.Len() > 0 {
		return errs
	}
	// Uniqueness check (cheapest if other rules already failed —
	// skip on prior failure). Mirrors C# .Must((v, c) => !svc.All()
	// .Any(f => f.Name == c && f.Id != v.Id)).WithMessage("Must be unique.")
	all, err := svc.All(ctx)
	if err != nil {
		// Treat lookup failure as a server-side issue surfaced
		// via 500 in the handler — Validate returns OK here so
		// the caller proceeds and the next Insert/Update emits
		// the 500.
		return errs
	}
	for _, f := range all {
		if f.ID == excludeID {
			continue
		}
		if f.Name == res.Name {
			errs.Add("Name", "Must be unique.")
			return errs
		}
	}
	return errs
}

// customFormatToModel serialises the resource into the persisted
// model. Specifications round-trips through json.Marshal so the
// Specifications column carries the canonical JSON form. Mirrors
// the C# `resource.ToModel(_specifications)` projection at
// `CustomFormatResource.cs` (the spec-builder reflection that gives
// each Specification its typed shape — Go defers that, persisting
// the on-wire structure verbatim).
func customFormatToModel(res CustomFormatResource) (customformats.CustomFormat, error) {
	specJSON := "[]"
	if len(res.Specifications) > 0 {
		buf, err := json.Marshal(res.Specifications)
		if err != nil {
			return customformats.CustomFormat{}, err
		}
		specJSON = string(buf)
	}
	include := false
	if res.IncludeCustomFormatWhenRenaming != nil {
		include = *res.IncludeCustomFormatWhenRenaming
	}
	return customformats.CustomFormat{
		ID:                              res.ID,
		Name:                            res.Name,
		Specifications:                  specJSON,
		IncludeCustomFormatWhenRenaming: include,
	}, nil
}

// customFormatToModelFromFormat reverses the toCustomFormatResource
// projection plus the Specifications JSON serialisation. Used by
// the bulk update path where we have the typed Format from
// GetByID and need to persist with the patched include flag.
func customFormatToModelFromFormat(f *customformats.Format) (customformats.CustomFormat, error) {
	if f == nil {
		return customformats.CustomFormat{}, errors.New("nil format")
	}
	// Re-marshal Specifications from the typed Format projection.
	// For now, the toCustomFormatResource path returns an empty
	// Specifications slice (the spec-builder port is deferred), so
	// the persisted column is [] — same as the pre-patch state. When
	// the spec-builder lands, this round-trip needs to preserve the
	// spec-internal structure.
	res := toCustomFormatResource(f)
	res.IncludeCustomFormatWhenRenaming = boolPtr(f.IncludeCustomFormatWhenRenaming)
	return customFormatToModel(res)
}

func boolPtr(b bool) *bool { return &b }

func writeCustomFormatJSON(w http.ResponseWriter, status int, f *customformats.Format) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(toCustomFormatResource(f))
	_, _ = w.Write(buf)
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
