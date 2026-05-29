package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/validation"
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
// for each spec. On GET it's populated by [autoTagSpecToSchema]
// (J433): the typed spec's base flags + value fields projected onto
// the golden-verified [autoTaggingSchemas] FieldDef templates. On
// POST/PUT the request decodes into the same type ([]any fields hold
// the client's {name,value} objects, read by
// [autoTaggingSpecsToEnvelopeJSON] on write).
//
// `id` uses omitempty because C# ToSchema never sets the spec's
// RestResource.Id (stays 0 → omitted). No InfoLink — the C# autotag
// spec POCO has none.
type AutoTaggingSpecificationSchema struct {
	ID                 int    `json:"id,omitempty"`
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
	specs := make([]AutoTaggingSpecificationSchema, 0, len(t.Specifications))
	for _, spec := range t.Specifications {
		schema, err := autoTagSpecToSchema(spec)
		if err != nil {
			// Unreachable in practice (loaded specs round-trip + every
			// discriminator has a template); skip rather than panic.
			continue
		}
		specs = append(specs, schema)
	}
	return AutoTaggingResource{
		ID:                      t.ID,
		Name:                    t.Name,
		RemoveTagsAutomatically: t.RemoveTagsAutomatically,
		Tags:                    tags,
		Specifications:          specs,
	}
}

// atSchemaTemplateByImpl indexes the canonical autotag schema
// templates by implementation discriminator for the GET projection's
// field-metadata clone.
var atSchemaTemplateByImpl = func() map[string]AutoTaggingSpecificationSchemaResource {
	m := make(map[string]AutoTaggingSpecificationSchemaResource, len(autoTaggingSchemas))
	for _, s := range autoTaggingSchemas {
		m[s.Implementation] = s
	}
	return m
}()

// autoTagSpecToSchema projects one persisted typed autotag spec onto
// the wire [AutoTaggingSpecificationSchema]. Ports C#
// `IAutoTaggingSpecification.ToSchema()`. Recovers base flags + value
// fields by round-tripping through the tested
// [autotagging.MarshalSpecs] envelope, then clones the golden-verified
// [autoTaggingSchemas] FieldDef template for the impl and injects the
// persisted value per field name. (AutoTag specs carry no InfoLink —
// matches the C# AutoTaggingSpecificationSchema shape.)
func autoTagSpecToSchema(spec autotagging.Specification) (AutoTaggingSpecificationSchema, error) {
	envJSON, err := autotagging.MarshalSpecs([]autotagging.Specification{spec})
	if err != nil {
		return AutoTaggingSpecificationSchema{}, err
	}
	var envs []struct {
		Type string         `json:"type"`
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal(envJSON, &envs); err != nil {
		return AutoTaggingSpecificationSchema{}, err
	}
	if len(envs) != 1 {
		return AutoTaggingSpecificationSchema{}, fmt.Errorf("autotagging: specToSchema: expected 1 envelope, got %d", len(envs))
	}
	impl := envs[0].Type
	body := envs[0].Body

	template, ok := atSchemaTemplateByImpl[impl]
	if !ok {
		return AutoTaggingSpecificationSchema{}, fmt.Errorf("autotagging: specToSchema: no schema template for %q", impl)
	}

	name, _ := body["name"].(string)
	negate, _ := body["negate"].(bool)
	required, _ := body["required"].(bool)

	fields := make([]any, 0, len(template.Fields))
	for _, fd := range template.Fields {
		if v, present := body[fd.Name]; present {
			fields = append(fields, fd.WithValue(v))
		} else {
			fields = append(fields, fd)
		}
	}

	return AutoTaggingSpecificationSchema{
		Name:               name,
		Implementation:     impl,
		ImplementationName: template.ImplementationName,
		Negate:             negate,
		Required:           required,
		Fields:             fields,
	}, nil
}

// autoTaggingSpecsToEnvelopeJSON maps the on-wire autotag spec list
// into the persisted `[{"type","body"}]` envelope JSON the load path
// understands (same pattern + rationale as customformat's
// [customFormatSpecsToEnvelopeJSON]). Empty/nil → `"[]"`.
func autoTaggingSpecsToEnvelopeJSON(specs []AutoTaggingSpecificationSchema) (string, error) {
	if len(specs) == 0 {
		return "[]", nil
	}
	type envelope struct {
		Type string         `json:"type"`
		Body map[string]any `json:"body"`
	}
	envs := make([]envelope, 0, len(specs))
	for _, s := range specs {
		bodyMap := map[string]any{
			"name":     s.Name,
			"negate":   s.Negate,
			"required": s.Required,
		}
		for _, raw := range s.Fields {
			fm, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			fname, _ := fm["name"].(string)
			if fname == "" {
				continue
			}
			if v, present := fm["value"]; present {
				bodyMap[fname] = v
			}
		}
		envs = append(envs, envelope{Type: s.Implementation, Body: bodyMap})
	}
	rawJSON, err := json.Marshal(envs)
	if err != nil {
		return "", err
	}
	typed, err := autotagging.UnmarshalSpecs(rawJSON)
	if err != nil {
		return "", err
	}
	canonical, err := autotagging.MarshalSpecs(typed)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

// ---------- 5.B10: CRUD ----------

// AutoTaggingWriter is the write surface. *autotagging.Service
// satisfies it via All/GetByID/Insert/Update/Delete.
// AutoTaggingController in C# extends RestController<T> (no SignalR).
type AutoTaggingWriter interface {
	All(ctx context.Context) ([]*autotagging.Tag, error)
	GetByID(ctx context.Context, id int) (*autotagging.Tag, error)
	Insert(ctx context.Context, row *autotagging.AutoTag) (*autotagging.Tag, error)
	Update(ctx context.Context, row *autotagging.AutoTag) error
	Delete(ctx context.Context, id int) error
}

// AutoTaggingByIDHandler returns GET /api/v3/autotagging/{id}.
// Mirrors `AutoTaggingController.GetResourceById` at
// `src/Sonarr.Api.V3/AutoTagging/AutoTaggingController.cs:47-50`.
func AutoTaggingByIDHandler(svc AutoTaggingWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseAutoTaggingID(w, r)
		if !ok {
			return
		}
		t, err := svc.GetByID(r.Context(), id)
		if errors.Is(err, autotagging.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("autotagging %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("autotagging: %w", err), nil)
			return
		}
		writeAutoTaggingJSON(w, http.StatusOK, t)
	})
}

// AutoTaggingCreateHandler returns POST /api/v3/autotagging.
// Mirrors `Create` at `:52-61`. Validation chain ports the C#
// SharedValidator block plus the Custom() spec-condition checks.
//
// DEFERRED: spec-internal Validate() (regex syntax etc) — needs the
// spec-builder reflection layer that customformat 5.B4 also defers.
// The handler accepts Specifications opaquely and persists the JSON
// verbatim until that lands.
func AutoTaggingCreateHandler(svc AutoTaggingWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeAutoTaggingBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("autotagging: malformed body: %v", err))
			return
		}
		if errs := validateAutoTagging(r.Context(), svc, body, 0); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model, err := autoTaggingToModel(*body)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("autotagging: serialise: %w", err), nil)
			return
		}
		inserted, err := svc.Insert(r.Context(), &model)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("autotagging: %w", err), nil)
			return
		}
		writeAutoTaggingJSON(w, http.StatusCreated, inserted)
	})
}

// AutoTaggingUpdateHandler returns PUT /api/v3/autotagging/{id}.
// Mirrors `Update` at `:63-74`. Path id wins; reloads the row after
// Update so the response projects the canonical typed Tag shape.
func AutoTaggingUpdateHandler(svc AutoTaggingWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseAutoTaggingID(w, r)
		if !ok {
			return
		}
		body, err := decodeAutoTaggingBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("autotagging: malformed body: %v", err))
			return
		}
		body.ID = id
		if errs := validateAutoTagging(r.Context(), svc, body, id); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model, err := autoTaggingToModel(*body)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("autotagging: serialise: %w", err), nil)
			return
		}
		err = svc.Update(r.Context(), &model)
		if errors.Is(err, autotagging.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("autotagging %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("autotagging: %w", err), nil)
			return
		}
		t, err := svc.GetByID(r.Context(), id)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("autotagging: reload: %w", err), nil)
			return
		}
		writeAutoTaggingJSON(w, http.StatusAccepted, t)
	})
}

// AutoTaggingDeleteHandler returns DELETE /api/v3/autotagging/{id}.
// Mirrors `DeleteFormat` at C# `AutoTaggingController.cs:83-87`.
// **Idempotent** — returns 200 empty body whether the id exists or
// not, matching live C# v5-develop `7c41dcbe2` probe behaviour
// (`DELETE /api/v3/autotagging/99999` → 200, empty body). C.1.i-follow-4
// alignment closed at J232.
//
// The v5 sibling at `internal/api/v5/autotagging.DeleteHandler` uses
// strict `TypedResults.NoContent()` (404 on missing) — that's the v5
// surface's deliberate divergence from the v3 idempotent posture.
func AutoTaggingDeleteHandler(svc AutoTaggingWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseAutoTaggingID(w, r)
		if !ok {
			return
		}
		err := svc.Delete(r.Context(), id)
		if err != nil && !errors.Is(err, autotagging.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("autotagging: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// ---------- helpers ----------

func parseAutoTaggingID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "autotagging: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("autotagging: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

func decodeAutoTaggingBody(r *http.Request) (*AutoTaggingResource, error) {
	var t AutoTaggingResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateAutoTagging ports the C# SharedValidator chain at
// `AutoTaggingController.cs:28-44`:
//
//   - Name NotEmpty
//   - Name unique across All() (excluding self by id)
//   - Tags NotEmpty
//   - Specifications NotEmpty + each Specification.Name non-empty
//
// excludeID is the URL id on Update (so the row being updated doesn't
// false-positive the uniqueness check); 0 on Create.
func validateAutoTagging(ctx context.Context, svc AutoTaggingWriter, res *AutoTaggingResource, excludeID int) *validation.Errors {
	errs := validation.New()
	if !errs.Required("Name", res.Name) {
		return errs
	}
	if len(res.Tags) == 0 {
		errs.Add("Tags", "'Tags' must not be empty")
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
	// Uniqueness check across all rows.
	all, err := svc.All(ctx)
	if err != nil {
		return errs
	}
	for _, t := range all {
		if t == nil || t.ID == excludeID {
			continue
		}
		if t.Name == res.Name {
			errs.Add("Name", "Must be unique.")
			return errs
		}
	}
	return errs
}

// autoTaggingToModel serialises the resource into the persisted
// model. Mirrors C# `resource.ToModel(_specifications)` →
// MapSpecification → converter-serialise.
//
// CRITICAL (same class of bug as customformat J431): the column MUST
// carry the loader's `{type, body}` envelope — `autotagging.LoadAll`
// → `UnmarshalSpecs` rejects the on-wire schema shape. The earlier
// stub persisted the schema shape verbatim, so any AutoTag created
// with conditions failed write→reload through the real service.
func autoTaggingToModel(res AutoTaggingResource) (autotagging.AutoTag, error) {
	specJSON, err := autoTaggingSpecsToEnvelopeJSON(res.Specifications)
	if err != nil {
		return autotagging.AutoTag{}, err
	}
	tags := res.Tags
	if tags == nil {
		tags = []int{}
	}
	return autotagging.AutoTag{
		ID:                      res.ID,
		Name:                    res.Name,
		Specifications:          specJSON,
		RemoveTagsAutomatically: res.RemoveTagsAutomatically,
		Tags:                    tags,
	}, nil
}

func writeAutoTaggingJSON(w http.ResponseWriter, status int, t *autotagging.Tag) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(toAutoTaggingResource(t))
	_, _ = w.Write(buf)
}
