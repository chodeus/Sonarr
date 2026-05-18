package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/tags"
	"sonarr.tv/sonarr/internal/validation"
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
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("tags: %w", err), nil)
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

// TagWriter is the write side of tags the CRUD handlers depend on.
// *tags.Service satisfies it via its existing Add/Update/Delete +
// GetByID methods. The handler depends on the narrowed interface so
// test doubles can isolate write paths.
type TagWriter interface {
	GetByID(ctx context.Context, id int) (*tags.Tag, error)
	Add(ctx context.Context, t *tags.Tag) (*tags.Tag, error)
	Update(ctx context.Context, t *tags.Tag) error
	Delete(ctx context.Context, id int) error
}

// tagLabelRule is the regex the C# TagController applies to every
// Label via FluentValidation at
// `src/Sonarr.Api.V3/Tags/TagController.cs:29-32`. Compiled once at
// init for the validation hot path. Matches `^[a-z0-9-]+$` with
// case-insensitive mode (so uppercase passes through, then the
// service lowercases on persist).
var tagLabelRule = regexp.MustCompile(`(?i)^[a-z0-9-]+$`)

// validateTagResource runs the per-field rules the C# `SharedValidator`
// applies to every TagResource — NotEmpty + Matches the label regex
// with the "Allowed characters a-z, 0-9 and -" overrides message
// (mirrors `.WithMessage(...)` in the C# rule chain). Returns a
// populated validation.Errors that the caller can pass straight to
// httperr.WriteValidation when !OK.
func validateTagResource(res *TagResource) *validation.Errors {
	errs := validation.New()
	if !errs.Required("Label", res.Label) {
		return errs
	}
	if !tagLabelRule.MatchString(res.Label) {
		errs.Add("Label", "Allowed characters a-z, 0-9 and -")
	}
	return errs
}

// decodeTagBody reads the JSON body into a TagResource. Wire-shape
// errors (malformed JSON) emit as a 400 envelope rather than a
// FluentValidation 400 — they're protocol-level, not field-level.
func decodeTagBody(r *http.Request) (*TagResource, error) {
	var t TagResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// TagByIDHandler returns the http.Handler for GET /api/v3/tag/{id}.
// Mirrors `TagController.GetResourceById` at
// `src/Sonarr.Api.V3/Tags/TagController.cs:35-38` via the
// `RestController<T>` base.
//
// Returns 200 + TagResource on hit, 404 + envelope on miss. The
// ID is parsed from the {id} path wildcard via Go 1.22's
// http.Request.PathValue.
func TagByIDHandler(svc TagWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseTagID(w, r)
		if !ok {
			return
		}
		row, err := svc.GetByID(r.Context(), id)
		if errors.Is(err, tags.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("tag %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("tags: %w", err), nil)
			return
		}
		writeTagJSON(w, http.StatusOK, *row)
	})
}

// TagCreateHandler returns the http.Handler for POST /api/v3/tag.
// Mirrors `TagController.Create` at
// `src/Sonarr.Api.V3/Tags/TagController.cs:47-52`. On success returns
// 201 + the inserted resource (Id populated by the service).
//
// The service's Add path is dedupe-on-label by design — submitting
// an existing label returns the existing row at 201, matching the
// C# `_tagService.Add(...)` behaviour. SignalR Sync fires via the
// bus subscription wired in cmd/sonarr/signalr_wiring.go (no direct
// emitter call from the handler — Add publishes TagsUpdatedEvent).
func TagCreateHandler(svc TagWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeTagBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("tag: malformed body: %v", err))
			return
		}
		if errs := validateTagResource(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		inserted, err := svc.Add(r.Context(), &tags.Tag{Label: body.Label})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("tags: %w", err), nil)
			return
		}
		writeTagJSON(w, http.StatusCreated, *inserted)
	})
}

// TagUpdateHandler returns the http.Handler for PUT /api/v3/tag/{id}.
// Mirrors `TagController.Update` at
// `src/Sonarr.Api.V3/Tags/TagController.cs:54-60`. Returns 202 +
// the resource on success (matches C# `Accepted(resource.Id)` shape
// even though Sonarr's frontend treats 202 the same as 200 here).
//
// The {id} path wildcard is the authoritative ID. The body's Id is
// ignored if it conflicts — matches the C# `RestPutById` attribute
// which routes by URL ID.
func TagUpdateHandler(svc TagWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseTagID(w, r)
		if !ok {
			return
		}
		body, err := decodeTagBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("tag: malformed body: %v", err))
			return
		}
		if errs := validateTagResource(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		body.ID = id
		err = svc.Update(r.Context(), &tags.Tag{ID: id, Label: body.Label})
		if errors.Is(err, tags.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("tag %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("tags: %w", err), nil)
			return
		}
		writeTagJSON(w, http.StatusAccepted, tags.Tag{ID: id, Label: body.Label})
	})
}

// TagDeleteHandler returns the http.Handler for DELETE /api/v3/tag/{id}.
// Mirrors `TagController.DeleteTag` at
// `src/Sonarr.Api.V3/Tags/TagController.cs:62-66`. Returns 200 with an
// empty body on success (matches the C# `void` return + default
// success code).
//
// The C# Delete does NOT check Details() for in-use rejection — V3
// frontends are expected to pre-flight via /tag/detail. The Go port
// preserves that posture for wire compatibility.
func TagDeleteHandler(svc TagWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseTagID(w, r)
		if !ok {
			return
		}
		err := svc.Delete(r.Context(), id)
		if errors.Is(err, tags.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("tag %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("tags: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// parseTagID extracts the {id} path wildcard and emits a 400 on
// malformed input. Returns (id, true) on success or (0, false) when
// the caller should bail (response already written).
func parseTagID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "tag: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("tag: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

// writeTagJSON emits a single Tag at the given status code. Used by
// the per-id GET / create / update paths. Matches the C# single-row
// projection at `TagResourceMapper.ToResource`.
func writeTagJSON(w http.ResponseWriter, status int, t tags.Tag) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(TagResource{ID: t.ID, Label: t.Label})
	_, _ = w.Write(buf)
}
