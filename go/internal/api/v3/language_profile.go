package v3

import (
	"encoding/json"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/language"
)

// LanguageProfileResource mirrors
// `src/Sonarr.Api.V3/Profiles/Languages/LanguageProfileResource.cs`.
// The C# controller is `[Obsolete("Deprecated")]` — Sonarr V3 removed
// the per-series language-profile feature, but the controller still
// returns a hardcoded "Deprecated" profile so callers that haven't
// migrated still get a 200 response. The Go port mirrors that
// stubbed shape verbatim.
type LanguageProfileResource struct {
	ID             int                           `json:"id"`
	Name           string                        `json:"name"`
	UpgradeAllowed bool                          `json:"upgradeAllowed"`
	Cutoff         language.Language             `json:"cutoff"`
	Languages      []LanguageProfileItemResource `json:"languages"`
}

// LanguageProfileItemResource mirrors the per-language Allowed flag.
// `RestResource.Id` is inherited in C#; the Go port mirrors via an
// explicit field. C# default value is 0 (no override needed).
type LanguageProfileItemResource struct {
	ID       int               `json:"id"`
	Language language.Language `json:"language"`
	Allowed  bool              `json:"allowed"`
}

// deprecatedLanguageProfile is the single hardcoded entry the C#
// controller returns. The English Language struct + Allowed=true
// matches the C# `Language.English` static field at
// `src/NzbDrone.Core/Languages/Language.cs`.
func deprecatedLanguageProfile() LanguageProfileResource {
	return LanguageProfileResource{
		ID:             1,
		Name:           "Deprecated",
		UpgradeAllowed: true,
		Cutoff:         language.English,
		Languages: []LanguageProfileItemResource{
			{Language: language.English, Allowed: true},
		},
	}
}

// LanguageProfileHandler returns GET /api/v3/languageprofile. Mirrors
// `LanguageProfileController.GetAll` — emits a single-element array
// containing the hardcoded deprecated profile.
func LanguageProfileHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal([]LanguageProfileResource{deprecatedLanguageProfile()})
		_, _ = w.Write(buf)
	})
}

// LanguageProfileByIDHandler returns GET /api/v3/languageprofile/{id}.
// Mirrors `GetResourceById` — emits the hardcoded deprecated profile
// for any id (the C# implementation ignores the id entirely and
// always returns the same struct).
func LanguageProfileByIDHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate the id is parseable but otherwise ignore it - C# accepts
		// any int id and returns the static deprecated profile.
		if _, err := strconv.Atoi(r.PathValue("id")); err != nil {
			httperr.BadRequest(w, "languageprofile: invalid id")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(deprecatedLanguageProfile())
		_, _ = w.Write(buf)
	})
}

// LanguageProfileSchemaHandler returns GET /api/v3/languageprofile/schema.
// Mirrors `LanguageProfileSchemaController.GetSchema` — emits the same
// deprecated single-profile shape, also obsolete.
func LanguageProfileSchemaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(deprecatedLanguageProfile())
		_, _ = w.Write(buf)
	})
}

// LanguageProfileWriteAcceptedHandler returns POST/PUT
// /api/v3/languageprofile + DELETE/{id}. Mirrors the obsolete write
// endpoints which the C# controller exposes as no-ops returning 202.
// Empty {} body for DELETE; echo body for POST/PUT (C# returns
// Accepted(resource)).
func LanguageProfileWriteAcceptedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		// POST/PUT: echo body if parseable, otherwise empty resource.
		var body LanguageProfileResource
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(body)
		_, _ = w.Write(buf)
	})
}
