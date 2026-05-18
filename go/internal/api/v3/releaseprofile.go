package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/validation"
)

// ReleaseProfileResource mirrors
// `src/Sonarr.Api.V3/Profiles/Release/ReleaseProfileResource.cs`.
//
// C# input accepts Required/Ignored as string, list, or JArray for
// backward compatibility (see ReleaseProfileResource.cs:15-17 +
// RestrictionResourceMapper.ParseArray). Output is always a
// List<string> (line 45-46: `Required ?? new List<string>()`), so the
// Go wire type for output is `[]string`. The POST/PUT polymorphic
// input handling lands in a follow-up slice when those methods port.
//
// Field-source mapping (C# → Go):
//
//	id                  → Profile.ID
//	name                → Profile.Name
//	enabled             → Profile.Enabled
//	required            → Profile.Required (?? [])
//	ignored             → Profile.Ignored  (?? [])
//	airDateRestriction  → Profile.AirDateRestriction
//	airDateGracePeriod  → Profile.AirDateGracePeriod
//	indexerId           → Profile.IndexerIDs.FirstOrDefault(0)
//	tags                → Profile.Tags  (HashSet → []int)
//	excludedTags        → Profile.ExcludedTags  (HashSet → []int)
type ReleaseProfileResource struct {
	ID                 int      `json:"id"`
	Name               string   `json:"name"`
	Enabled            bool     `json:"enabled"`
	Required           []string `json:"required"`
	Ignored            []string `json:"ignored"`
	AirDateRestriction bool     `json:"airDateRestriction"`
	AirDateGracePeriod int      `json:"airDateGracePeriod"`
	IndexerID          int      `json:"indexerId"`
	Tags               []int    `json:"tags"`
	ExcludedTags       []int    `json:"excludedTags"`
}

// ReleaseProfileLister is the read side the handler depends on.
// *releaseprofiles.Service satisfies it via All.
type ReleaseProfileLister interface {
	All(ctx context.Context) ([]releaseprofiles.Profile, error)
}

// ReleaseProfileHandler returns the http.Handler for
// GET /api/v3/releaseprofile. Mirrors
// `ReleaseProfileController.GetAll` at
// `src/Sonarr.Api.V3/Profiles/Release/ReleaseProfileController.cs:82-86`.
//
// Empty DB returns an initialised slice so the response body is `[]`.
func ReleaseProfileHandler(lister ReleaseProfileLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("releaseprofiles: %w", err), nil)
			return
		}
		out := make([]ReleaseProfileResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toReleaseProfileResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toReleaseProfileResource(p releaseprofiles.Profile) ReleaseProfileResource {
	r := ReleaseProfileResource{
		ID:                 p.ID,
		Name:               p.Name,
		Enabled:            p.Enabled,
		Required:           p.Required,
		Ignored:            p.Ignored,
		AirDateRestriction: p.AirDateRestriction,
		AirDateGracePeriod: p.AirDateGracePeriod,
		Tags:               p.Tags,
		ExcludedTags:       p.ExcludedTags,
	}
	// Match the C# `?? new List<string>()` guard — both fields are
	// non-omitempty on the wire and must serialise as `[]`, not
	// `null`, when the row stored a nil slice.
	if r.Required == nil {
		r.Required = []string{}
	}
	if r.Ignored == nil {
		r.Ignored = []string{}
	}
	if r.Tags == nil {
		r.Tags = []int{}
	}
	if r.ExcludedTags == nil {
		r.ExcludedTags = []int{}
	}
	// IndexerId = IndexerIds.FirstOrDefault(0) — collapse the slice
	// to a single int. The Go port stores IndexerIDs as a list for
	// schema future-proofing; today the API only exposes the first.
	if len(p.IndexerIDs) > 0 {
		r.IndexerID = p.IndexerIDs[0]
	}
	return r
}

// ---------- 5.B6: CRUD ----------

// ReleaseProfileWriter is the write surface for the CRUD handlers.
// *releaseprofiles.Service satisfies it via All/Get/Add/Update/Delete.
// ReleaseProfileController in C# extends RestController<T> — no SignalR.
type ReleaseProfileWriter interface {
	Get(ctx context.Context, id int) (*releaseprofiles.Profile, error)
	Add(ctx context.Context, p *releaseprofiles.Profile) (*releaseprofiles.Profile, error)
	Update(ctx context.Context, p *releaseprofiles.Profile) error
	Delete(ctx context.Context, id int) error
}

// ReleaseProfileByIDHandler returns GET /api/v3/releaseprofile/{id}.
// Mirrors `ReleaseProfileController.GetResourceById` at
// `src/Sonarr.Api.V3/Profiles/Release/ReleaseProfileController.cs:77-80`.
func ReleaseProfileByIDHandler(svc ReleaseProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseReleaseProfileID(w, r)
		if !ok {
			return
		}
		p, err := svc.Get(r.Context(), id)
		if errors.Is(err, releaseprofiles.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("releaseprofile %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("releaseprofiles: %w", err), nil)
			return
		}
		writeReleaseProfileJSON(w, http.StatusOK, *p)
	})
}

// ReleaseProfileCreateHandler returns POST /api/v3/releaseprofile.
// Mirrors `Create` at `:53-59`. Returns 201 + persisted resource.
//
// Validation ports the C# SharedValidator chain (cheap subset):
//   - At least one of Required, Ignored, or AirDateRestriction is set
//   - Required/Ignored entries cannot be empty/whitespace
//   - Tags ∩ ExcludedTags = ∅ (no tag in both sets)
//
// DEFERRED: indexer-exists check (`indexerFactory.Exists(IndexerId)`
// when Enabled+IndexerId != 0). Requires plumbing an IndexerExistence
// adapter; surfaces as Service.Add error in the meantime (the C#
// validator runs at the controller boundary, not at the service —
// so service-side won't catch it; this slice ships without that
// check and leaves it as a TODO for the indexer-arc slice).
func ReleaseProfileCreateHandler(svc ReleaseProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeReleaseProfileBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("releaseprofile: malformed body: %v", err))
			return
		}
		if errs := validateReleaseProfile(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := fromReleaseProfileResource(body)
		inserted, err := svc.Add(r.Context(), &model)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("releaseprofiles: %w", err), nil)
			return
		}
		writeReleaseProfileJSON(w, http.StatusCreated, *inserted)
	})
}

// ReleaseProfileUpdateHandler returns PUT /api/v3/releaseprofile/{id}.
// Mirrors `Update` at `:67-75`. Path id wins over body id.
func ReleaseProfileUpdateHandler(svc ReleaseProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseReleaseProfileID(w, r)
		if !ok {
			return
		}
		body, err := decodeReleaseProfileBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("releaseprofile: malformed body: %v", err))
			return
		}
		if errs := validateReleaseProfile(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		body.ID = id
		model := fromReleaseProfileResource(body)
		err = svc.Update(r.Context(), &model)
		if errors.Is(err, releaseprofiles.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("releaseprofile %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("releaseprofiles: %w", err), nil)
			return
		}
		writeReleaseProfileJSON(w, http.StatusAccepted, model)
	})
}

// ReleaseProfileDeleteHandler returns DELETE /api/v3/releaseprofile/{id}.
// Mirrors `DeleteProfile` at `:61-65`. 200 empty / 404 envelope.
func ReleaseProfileDeleteHandler(svc ReleaseProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseReleaseProfileID(w, r)
		if !ok {
			return
		}
		err := svc.Delete(r.Context(), id)
		if errors.Is(err, releaseprofiles.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("releaseprofile %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("releaseprofiles: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// ---------- helpers ----------

func parseReleaseProfileID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "releaseprofile: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("releaseprofile: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

func decodeReleaseProfileBody(r *http.Request) (*ReleaseProfileResource, error) {
	var t ReleaseProfileResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateReleaseProfile ports the C# SharedValidator Custom() block
// at `src/Sonarr.Api.V3/Profiles/Release/ReleaseProfileController.cs:24-45`:
//
//   - At least one of Required, Ignored, or AirDateRestriction must be set
//     (otherwise the profile has no restrictions to apply)
//   - Required / Ignored entries cannot be empty or whitespace-only
//   - Tags ∩ ExcludedTags must be empty
func validateReleaseProfile(res *ReleaseProfileResource) *validation.Errors {
	errs := validation.New()
	if len(res.Required) == 0 && len(res.Ignored) == 0 && !res.AirDateRestriction {
		errs.Add("Required", "'Must contain' or 'Must not contain' is required")
	}
	for _, s := range res.Required {
		if strings.TrimSpace(s) == "" {
			errs.Add("Required", "'Must contain' should not contain whitespaces or an empty string")
			break
		}
	}
	for _, s := range res.Ignored {
		if strings.TrimSpace(s) == "" {
			errs.Add("Ignored", "'Must not contain' should not contain whitespaces or an empty string")
			break
		}
	}
	if overlap := intersectInts(res.Tags, res.ExcludedTags); len(overlap) > 0 {
		// C# formats the message with the tag labels joined by ", "
		// (looked up via tagService.GetTags). Without a TagLabelLister
		// in scope, the Go port emits the IDs — same error attribution
		// (ExcludedTags property), slightly different message body.
		ids := make([]string, 0, len(overlap))
		for _, id := range overlap {
			ids = append(ids, strconv.Itoa(id))
		}
		errs.Add("ExcludedTags", fmt.Sprintf("tag id(s) %s cannot be in both 'Tags' and 'Excluded Tags'", strings.Join(ids, ", ")))
	}
	return errs
}

// intersectInts returns the values present in both a and b. Order
// follows a; duplicates within a single slice are emitted at most
// once. Empty/nil inputs yield nil.
func intersectInts(a, b []int) []int {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	inB := make(map[int]struct{}, len(b))
	for _, v := range b {
		inB[v] = struct{}{}
	}
	seen := make(map[int]struct{}, len(a))
	var out []int
	for _, v := range a {
		if _, dup := seen[v]; dup {
			continue
		}
		if _, ok := inB[v]; ok {
			out = append(out, v)
			seen[v] = struct{}{}
		}
	}
	return out
}

// fromReleaseProfileResource is the inverse of toReleaseProfileResource.
// Single IndexerId from the resource expands into the IndexerIDs slice
// (single-element when non-zero, empty otherwise — mirrors the C#
// `FirstOrDefault(0)` collapse in reverse). Nil-safe slice handling so
// the persisted model has non-nil empty slices not nil pointers.
func fromReleaseProfileResource(res *ReleaseProfileResource) releaseprofiles.Profile {
	out := releaseprofiles.Profile{
		ID:                 res.ID,
		Name:               res.Name,
		Enabled:            res.Enabled,
		Required:           res.Required,
		Ignored:            res.Ignored,
		Tags:               res.Tags,
		ExcludedTags:       res.ExcludedTags,
		AirDateRestriction: res.AirDateRestriction,
		AirDateGracePeriod: res.AirDateGracePeriod,
	}
	if res.IndexerID != 0 {
		out.IndexerIDs = []int{res.IndexerID}
	}
	if out.Required == nil {
		out.Required = []string{}
	}
	if out.Ignored == nil {
		out.Ignored = []string{}
	}
	if out.Tags == nil {
		out.Tags = []int{}
	}
	if out.ExcludedTags == nil {
		out.ExcludedTags = []int{}
	}
	return out
}

func writeReleaseProfileJSON(w http.ResponseWriter, status int, p releaseprofiles.Profile) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(toReleaseProfileResource(p))
	_, _ = w.Write(buf)
}
