package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/customfilters"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/validation"
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
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customfilters: %w", err), nil)
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

// CustomFilterWriter narrows the write surface to Get/Add/Update/Delete.
// *customfilters.Service satisfies it directly. CustomFilterController
// in C# extends RestController<CustomFilterResource> (NOT
// RestControllerWithSignalR), so there's no SignalR fan-out to
// thread — the Go port skips the emitter entirely.
type CustomFilterWriter interface {
	Get(ctx context.Context, id int) (*customfilters.Filter, error)
	Add(ctx context.Context, f *customfilters.Filter) (*customfilters.Filter, error)
	Update(ctx context.Context, f *customfilters.Filter) error
	Delete(ctx context.Context, id int) error
}

// CustomFilterByIDHandler returns GET /api/v3/customfilter/{id}.
// Mirrors `CustomFilterController.GetResourceById` at
// `src/Sonarr.Api.V3/CustomFilters/CustomFilterController.cs:20-23`.
func CustomFilterByIDHandler(svc CustomFilterWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseCustomFilterID(w, r)
		if !ok {
			return
		}
		f, err := svc.Get(r.Context(), id)
		if errors.Is(err, customfilters.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("customfilter %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customfilters: %w", err), nil)
			return
		}
		writeCustomFilterJSON(w, http.StatusOK, *f)
	})
}

// CustomFilterCreateHandler returns POST /api/v3/customfilter.
// Mirrors `AddCustomFilter` at `:32-39` — 201 + body with new ID.
//
// Validation: Type + Label are both required (the C# resource has no
// FluentValidation chain, but empty strings on either field render
// the row unusable in the UI). Filters is an opaque JSON payload —
// we accept whatever the client sends and re-emit it; the C# side
// does the same via STJson round-trip.
func CustomFilterCreateHandler(svc CustomFilterWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeCustomFilterBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("customfilter: malformed body: %v", err))
			return
		}
		if errs := validateCustomFilterResource(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := customFilterToModel(*body)
		inserted, err := svc.Add(r.Context(), &model)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customfilters: %w", err), nil)
			return
		}
		writeCustomFilterJSON(w, http.StatusCreated, *inserted)
	})
}

// CustomFilterUpdateHandler returns PUT /api/v3/customfilter/{id}.
// Mirrors `UpdateCustomFilter` at `:41-47` — 202 + body. Path ID
// wins over body ID per the C# RestPutById attribute.
func CustomFilterUpdateHandler(svc CustomFilterWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseCustomFilterID(w, r)
		if !ok {
			return
		}
		body, err := decodeCustomFilterBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("customfilter: malformed body: %v", err))
			return
		}
		if errs := validateCustomFilterResource(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		body.ID = id
		model := customFilterToModel(*body)
		err = svc.Update(r.Context(), &model)
		if errors.Is(err, customfilters.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("customfilter %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customfilters: %w", err), nil)
			return
		}
		writeCustomFilterJSON(w, http.StatusAccepted, model)
	})
}

// CustomFilterDeleteHandler returns DELETE /api/v3/customfilter/{id}.
// Mirrors `DeleteCustomResource` at `:49-53` — 200 empty body.
func CustomFilterDeleteHandler(svc CustomFilterWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseCustomFilterID(w, r)
		if !ok {
			return
		}
		err := svc.Delete(r.Context(), id)
		if errors.Is(err, customfilters.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("customfilter %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customfilters: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// parseCustomFilterID mirrors parseTagID / parseRootFolderID. Once
// 4 resources share this shape, factor into a shared v3util helper.
func parseCustomFilterID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "customfilter: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("customfilter: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

// decodeCustomFilterBody parses the request body. The Filters field
// is opaque JSON — we accept []json.RawMessage so the wire shape
// passes through without re-shaping.
func decodeCustomFilterBody(r *http.Request) (*CustomFilterResource, error) {
	var t CustomFilterResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateCustomFilterResource pins Type+Label as required. The C#
// resource doesn't declare a FluentValidation chain (
// `CustomFilterController` skips the SharedValidator block), but the
// UI requires both fields to be populated — making the empty-string
// case unsupported is a useful client-error signal.
func validateCustomFilterResource(res *CustomFilterResource) *validation.Errors {
	errs := validation.New()
	errs.Required("Type", res.Type)
	errs.Required("Label", res.Label)
	return errs
}

// customFilterToModel serialises the resource into the persisted
// model. The Filters []json.RawMessage round-trips through
// json.Marshal so the DB column carries the canonical JSON form.
// Mirrors C# `STJson.ToJson(resource.Filters)` at
// `CustomFilterResource.cs:46`.
func customFilterToModel(res CustomFilterResource) customfilters.Filter {
	out := customfilters.Filter{
		ID:    res.ID,
		Type:  res.Type,
		Label: res.Label,
	}
	if len(res.Filters) > 0 {
		buf, _ := json.Marshal(res.Filters)
		out.Filters = string(buf)
	} else {
		// Empty slice → "[]" rather than "" — matches the C# default
		// `STJson.ToJson(new List<ExpandoObject>())` which renders as
		// "[]". Avoids legacy-row ambiguity where "" might be parsed
		// as missing-field rather than empty-list.
		out.Filters = "[]"
	}
	return out
}

// writeCustomFilterJSON emits a single Filter projection. Same shape
// as writeTagJSON / writeRootFolderJSON (5.B1 / 5.B2).
func writeCustomFilterJSON(w http.ResponseWriter, status int, f customfilters.Filter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(toCustomFilterResource(f))
	_, _ = w.Write(buf)
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
