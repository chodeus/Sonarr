// Package updatesettings ports the v5 UpdateSettingsController at
// /api/v5/settings/update.
//
// Source: src/Sonarr.Api.V5/Settings/UpdateSettingsController.cs
// (24 LOC) + UpdateSettingsResource.cs (27 LOC) + the
// SettingsController<T> base at SettingsController.cs (55 LOC —
// shared with the other Settings sub-controllers).
//
// 3 endpoints — GET, GET /{id}, PUT /{id}. v5-NEW family: v3 has
// no equivalent UpdateConfigController, so there's no v3 handler
// to alias. Handlers are v5-specific from C# semantics.
//
// Wire shape (UpdateSettingsResource):
//
//   - branch                string  (camelCase enum-like; e.g. "main")
//   - updateAutomatically   bool    (default false)
//   - updateMechanism       string  (camelCase enum: builtIn/script/external/apt/docker)
//   - updateScriptPath      string  (validated absolute when mechanism=script)
//
// Plus the inherited `id` field from RestResource (always 1 for
// singleton resources — both GET / and GET /{id} return the same
// payload with the requested id set).
//
// Statuses: GET → 200, PUT → 202 Accepted (matches base
// SettingsController.SaveSettings TypedAccepted<TResource>).
//
// Validation: SharedValidator chain (per UpdateSettingsController.cs:15-17)
// — UpdateScriptPath must be a valid absolute path when
// UpdateMechanism == Script. Other branches are unrestricted.
//
// Persistence: writes via configfile.Provider's typed setters
// (SetBranch/SetUpdateAutomatically/SetUpdateMechanism/SetUpdateScriptPath).
// The C# SettingsController.SaveSettings uses reflection-based
// `SaveConfigDictionary` against both IConfigFileProvider and
// IConfigService — for this resource all 4 fields live on
// IConfigFileProvider so a `IConfigService.SaveConfigDictionary`
// pass would be a no-op. The Go-port skips the reflection trip
// and writes through the typed setters directly.
package updatesettings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/validation"
)

// Provider is the read+write surface this package needs from the
// configfile provider. *configfile.Provider satisfies it via the
// typed getters/setters at internal/configfile/wellknown.go.
type Provider interface {
	Branch(context.Context) (string, error)
	SetBranch(context.Context, string) error
	UpdateAutomatically(context.Context) (bool, error)
	SetUpdateAutomatically(context.Context, bool) error
	UpdateMechanism(context.Context) (string, error)
	SetUpdateMechanism(context.Context, string) error
	UpdateScriptPath(context.Context) (string, error)
	SetUpdateScriptPath(context.Context, string) error
}

// Resource mirrors v5 UpdateSettingsResource — 4 wire fields + the
// inherited RestResource id (omitempty when zero per the v3 Go-port
// convention; both GET endpoints emit id=1 to match the C# singleton
// resource model).
type Resource struct {
	ID                  int    `json:"id,omitempty"`
	Branch              string `json:"branch"`
	UpdateAutomatically bool   `json:"updateAutomatically"`
	UpdateMechanism     string `json:"updateMechanism"`
	UpdateScriptPath    string `json:"updateScriptPath"`
}

// GetHandler returns GET /api/v5/settings/update and GET /api/v5/settings/update/{id}.
// The {id} variant just uses the path param as the id field of the
// returned singleton resource; missing/invalid id defaults to 1.
func GetHandler(p Provider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := 1
		if raw := r.PathValue("id"); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				id = v
			}
		}
		res, ok := readResource(w, r, p)
		if !ok {
			return
		}
		res.ID = id
		write(w, http.StatusOK, res)
	})
}

// UpdateHandler returns PUT /api/v5/settings/update/{id}. Returns
// 202 Accepted + the persisted resource. Validation: UpdateScriptPath
// must be an absolute path when UpdateMechanism == script.
func UpdateHandler(p Provider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := 1
		if raw := r.PathValue("id"); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				id = v
			}
		}
		var body Resource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("settings/update: malformed body: %v", err))
			return
		}
		if errs := validate(&body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		// Persist via typed setters. Normalise UpdateMechanism back to
		// the PascalCase identifier the v3 config store uses.
		mechPascal, err := apiv3.UpdateMechanismFromName(body.UpdateMechanism)
		if err != nil {
			httperr.WriteValidation(w, []httperr.Failure{{PropertyName: "UpdateMechanism", ErrorMessage: err.Error()}})
			return
		}
		ctx := r.Context()
		if err := p.SetBranch(ctx, body.Branch); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("settings/update: set Branch: %w", err), nil)
			return
		}
		if err := p.SetUpdateAutomatically(ctx, body.UpdateAutomatically); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("settings/update: set UpdateAutomatically: %w", err), nil)
			return
		}
		if err := p.SetUpdateMechanism(ctx, mechPascal); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("settings/update: set UpdateMechanism: %w", err), nil)
			return
		}
		if err := p.SetUpdateScriptPath(ctx, body.UpdateScriptPath); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("settings/update: set UpdateScriptPath: %w", err), nil)
			return
		}
		res, ok := readResource(w, r, p)
		if !ok {
			return
		}
		res.ID = id
		write(w, http.StatusAccepted, res)
	})
}

// readResource pulls the 4 fields from the provider into a Resource.
// On any error, writes a 500 and returns ok=false.
func readResource(w http.ResponseWriter, r *http.Request, p Provider) (Resource, bool) {
	ctx := r.Context()
	branch, err := p.Branch(ctx)
	if err != nil {
		httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("settings/update: branch: %w", err), nil)
		return Resource{}, false
	}
	auto, err := p.UpdateAutomatically(ctx)
	if err != nil {
		httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("settings/update: updateAutomatically: %w", err), nil)
		return Resource{}, false
	}
	mech, err := p.UpdateMechanism(ctx)
	if err != nil {
		httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("settings/update: updateMechanism: %w", err), nil)
		return Resource{}, false
	}
	scriptPath, err := p.UpdateScriptPath(ctx)
	if err != nil {
		httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("settings/update: updateScriptPath: %w", err), nil)
		return Resource{}, false
	}
	return Resource{
		Branch:              branch,
		UpdateAutomatically: auto,
		UpdateMechanism:     apiv3.CamelCaseEnumName(mech),
		UpdateScriptPath:    scriptPath,
	}, true
}

func validate(res *Resource) *validation.Errors {
	errs := validation.New()
	if res.UpdateMechanism == "script" {
		if res.UpdateScriptPath == "" {
			errs.Add("UpdateScriptPath", "UpdateScriptPath must be set when UpdateMechanism is Script")
		} else if !filepath.IsAbs(res.UpdateScriptPath) {
			errs.Add("UpdateScriptPath", "UpdateScriptPath must be an absolute path when UpdateMechanism is Script")
		}
	}
	return errs
}

func write(w http.ResponseWriter, status int, res Resource) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(res)
	_, _ = w.Write(buf)
}

