// Package releaseprofile ports the v5 ReleaseProfileController family
// at /api/v5/releaseprofile.
//
// Source: src/Sonarr.Api.V5/Profiles/Release/ReleaseProfileController.cs
// (95 LOC) + ReleaseProfileResource.cs (59 LOC).
//
// 5 endpoints (1 GET list + 1 GET by-id + 1 POST + 1 PUT + 1 DELETE).
// TWO divergences from v3:
//
//  1. Resource field rename + type change: v3 emits `indexerId` (int,
//     collapsed from IndexerIDs[0]); v5 emits `indexerIds` (List<int>,
//     full slice). The wire field differs in BOTH name AND type —
//     v5-specific Resource + custom mapper required (J254/J262/J274
//     pattern).
//
//  2. DELETE /{id} returns 204 NoContent on v5 vs v3's 200+empty —
//     v5util.RewriteSuccessTo204 wrapper (J277 pattern).
//
// Other 4 endpoints have v5-specific handlers because the Resource
// shape they project/consume differs. v3 Go-port emits POST → 201
// and PUT → 202 matching v5's TypedCreated/TypedAccepted directly
// — same status codes, just different body projection.
//
// Validation chain: same SharedValidator Custom() block ported in
// the v3 handler at `validateReleaseProfile` — duplicated locally
// here to keep v3's private helper unexported. Adds one v5-specific
// rule: when Enabled and IndexerIds non-empty, each id must
// reference an existing indexer (per `ReleaseProfileController.cs:41-47`).
// The v3 surface marks this as DEFERRED pending an
// IndexerExistence adapter (per `releaseprofile.go:160-165`) — v5
// inherits the same deferral; the check is left as a TODO.
package releaseprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/validation"
)

// Resource mirrors src/Sonarr.Api.V5/Profiles/Release/ReleaseProfileResource.cs.
//
// Diff vs v3 Go-port apiv3.ReleaseProfileResource:
//   - removes `indexerId` (int)
//   - adds `indexerIds` ([]int)
//
// Required/Ignored are strictly []string on v5 (v3 C# is `object`
// polymorphic for backward compatibility — the v3 Go-port already
// pins []string per `internal/api/v3/releaseprofile.go:43-44`, so
// the v5 shape just locks in what's already on the wire).
type Resource struct {
	ID                 int      `json:"id"`
	Name               string   `json:"name"`
	Enabled            bool     `json:"enabled"`
	Required           []string `json:"required"`
	Ignored            []string `json:"ignored"`
	AirDateRestriction bool     `json:"airDateRestriction"`
	AirDateGracePeriod int      `json:"airDateGracePeriod"`
	IndexerIDs         []int    `json:"indexerIds"`
	Tags               []int    `json:"tags"`
	ExcludedTags       []int    `json:"excludedTags"`
}

// Lister is the read-list surface for GET /releaseprofile.
type Lister = apiv3.ReleaseProfileLister

// Writer is the CRUD surface for GET/{id}, POST, PUT, DELETE.
type Writer = apiv3.ReleaseProfileWriter

// ListHandler returns GET /api/v5/releaseprofile. Mirrors v3's list
// handler but projects the v5 Resource shape (indexerIds plural).
func ListHandler(lister Lister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("releaseprofiles: %w", err), nil)
			return
		}
		out := make([]Resource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// ByIDHandler returns GET /api/v5/releaseprofile/{id}.
func ByIDHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
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
		writeJSON(w, http.StatusOK, *p)
	})
}

// CreateHandler returns POST /api/v5/releaseprofile. 201 + persisted
// resource. Same validation chain as v3.
func CreateHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("releaseprofile: malformed body: %v", err))
			return
		}
		if errs := validate(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := fromResource(body)
		inserted, err := svc.Add(r.Context(), &model)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("releaseprofiles: %w", err), nil)
			return
		}
		writeJSON(w, http.StatusCreated, *inserted)
	})
}

// UpdateHandler returns PUT /api/v5/releaseprofile/{id}. 202 + body.
func UpdateHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		body, err := decodeBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("releaseprofile: malformed body: %v", err))
			return
		}
		if errs := validate(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		body.ID = id
		model := fromResource(body)
		if err := svc.Update(r.Context(), &model); err != nil {
			if errors.Is(err, releaseprofiles.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("releaseprofile %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("releaseprofiles: %w", err), nil)
			return
		}
		writeJSON(w, http.StatusAccepted, model)
	})
}

// DeleteHandler wraps v3's delete handler — v3 emits 200+empty,
// v5 wants 204. Wrapper predicate only matches 200; J232-idempotent
// missing-id path (200) gets rewritten to 204 too.
func DeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.ReleaseProfileDeleteHandler(svc))
}

// ---------- helpers ----------

func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
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

func decodeBody(r *http.Request) (*Resource, error) {
	var t Resource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validate ports the v5 SharedValidator Custom() block. Same rules as
// v3 PLUS the v5-only IndexerIds-each-exists check (DEFERRED — same
// status as v3, pending an IndexerExistence adapter).
func validate(res *Resource) *validation.Errors {
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
		ids := make([]string, 0, len(overlap))
		for _, id := range overlap {
			ids = append(ids, strconv.Itoa(id))
		}
		errs.Add("ExcludedTags", fmt.Sprintf("tag id(s) %s cannot be in both 'Tags' and 'Excluded Tags'", strings.Join(ids, ", ")))
	}
	return errs
}

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

func toResource(p releaseprofiles.Profile) Resource {
	r := Resource{
		ID:                 p.ID,
		Name:               p.Name,
		Enabled:            p.Enabled,
		Required:           p.Required,
		Ignored:            p.Ignored,
		AirDateRestriction: p.AirDateRestriction,
		AirDateGracePeriod: p.AirDateGracePeriod,
		IndexerIDs:         p.IndexerIDs,
		Tags:               p.Tags,
		ExcludedTags:       p.ExcludedTags,
	}
	if r.Required == nil {
		r.Required = []string{}
	}
	if r.Ignored == nil {
		r.Ignored = []string{}
	}
	if r.IndexerIDs == nil {
		r.IndexerIDs = []int{}
	}
	if r.Tags == nil {
		r.Tags = []int{}
	}
	if r.ExcludedTags == nil {
		r.ExcludedTags = []int{}
	}
	return r
}

func fromResource(res *Resource) releaseprofiles.Profile {
	out := releaseprofiles.Profile{
		ID:                 res.ID,
		Name:               res.Name,
		Enabled:            res.Enabled,
		Required:           res.Required,
		Ignored:            res.Ignored,
		IndexerIDs:         res.IndexerIDs,
		Tags:               res.Tags,
		ExcludedTags:       res.ExcludedTags,
		AirDateRestriction: res.AirDateRestriction,
		AirDateGracePeriod: res.AirDateGracePeriod,
	}
	if out.Required == nil {
		out.Required = []string{}
	}
	if out.Ignored == nil {
		out.Ignored = []string{}
	}
	if out.IndexerIDs == nil {
		out.IndexerIDs = []int{}
	}
	if out.Tags == nil {
		out.Tags = []int{}
	}
	if out.ExcludedTags == nil {
		out.ExcludedTags = []int{}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, p releaseprofiles.Profile) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(toResource(p))
	_, _ = w.Write(buf)
}
