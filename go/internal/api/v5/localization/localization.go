// Package localization is the v5 wire surface for /api/v5/language
// and /api/v5/localization.
//
// The v5 family (src/Sonarr.Api.V5/Localization/) is 143 LOC of C#
// across 5 files — LanguageController (38), LanguageResource (12),
// LocalizationController (43), LocalizationResource (44, including
// the JsonConverter), LocalizationLanguageResource (6).
//
// Five endpoints:
//
//	v5 endpoint                              v5 status   vs v3 Go-port
//	GET /api/v5/language                        200       same as /api/v3/language (path-alias)
//	GET /api/v5/language/{id}                   200/404   v3 Go-port has NO handler — new here
//	GET /api/v5/localization                    200       same as /api/v3/localization (path-alias)
//	GET /api/v5/localization/{id}               200       v3 Go-port has NO handler — new here
//	                                                      (id ignored; RestController GetResourceById
//	                                                       delegates to GetLocalization)
//	GET /api/v5/localization/language           200       same as /api/v3/localization/language
//	                                                      (path-alias)
//
// Resource shapes IDENTICAL field-for-field to v3:
//
//   - LanguageResource — `{id, name, nameLower}`. C# v5 declares
//     `NameLower => Name?.ToLowerInvariant()` as a computed property;
//     v3 Go-port precomputes the same value into a struct field via
//     `ListAll`. JSON output is identical.
//   - LocalizationResource — `{strings: {key: value, ...}}`. The C#
//     v5 declares a JsonConverter to preserve dict insertion order on
//     emit; Go map iteration is non-deterministic but the contract-
//     test normalizer alphabetizes keys before diff, so insertion
//     order is unobservable on byte-eq.
//   - LocalizationLanguageResource — `{identifier}` (BCP-47-style
//     "TwoLetter[-COUNTRY]").
//
// Two endpoint kinds:
//
//  1. Three list/dict endpoints (language list, localization dict,
//     localization-language identifier) are PATH-ALIASES of the
//     existing v3 Go-port handlers — same status, same envelope.
//  2. Two by-id endpoints are NEW in this slice. The C# v5 inherits
//     them from RestController via `GetResourceById`:
//     - `LanguageController.GetResourceById(id)` casts `(Language)id`
//       which calls `Language.FindById(id)`; if the cast throws
//       ArgumentException (unknown id), the framework projects to
//       404 per openapi. Go port: use `language.FindByID(id)` and
//       emit 404 on `!ok` (with id==0 returning Unknown per parity
//       with C# `:196-199`).
//     - `LocalizationController.GetResourceById(id)` ignores the id
//       and returns the same dict as the list endpoint (`:23-26`).
//       Go port: ignore the id, write the dict.
//
// Wave 3 v5 endpoint family port — 143 LOC C# source. J266.
package localization

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/language"
)

// LanguageResource is an alias of the v3 wire shape (unchanged in v5).
type LanguageResource = apiv3.LanguageResource

// LocalizationResource is an alias of the v3 wire shape (unchanged
// in v5).
type LocalizationResource = apiv3.LocalizationResource

// LocalizationLanguageResource is an alias of the v3 wire shape
// (unchanged in v5).
type LocalizationLanguageResource = apiv3.LocalizationLanguageResource

// StringsLister is an alias of the v3 read interface (re-exports
// `GetLocalizationDictionary`).
type StringsLister = apiv3.LocalizationStringsLister

// ----- list / dict path-aliases -----

// LanguageHandler returns GET /api/v5/language. Same wire shape +
// sort + status as v3.
var LanguageHandler = apiv3.LanguageHandler

// LocalizationHandler returns GET /api/v5/localization. Same dict
// envelope + status as v3.
var LocalizationHandler = apiv3.LocalizationHandler

// LocalizationLanguageHandler returns GET /api/v5/localization/language.
// Same BCP-47 identifier shape as v3.
var LocalizationLanguageHandler = apiv3.LocalizationLanguageHandler

// ----- new v5-specific ByID handlers -----

// LanguageByIDHandler returns GET /api/v5/language/{id}. Mirrors
// `LanguageController.GetResourceById(int id)` at
// `src/Sonarr.Api.V5/Localization/LanguageController.cs:12-22`:
// constructs a LanguageResource from the registry entry; the C#
// `(Language)id` cast throws ArgumentException for unknown ids,
// which the framework projects to 404 per the v5 openapi
// `:3422-3454` declaration.
//
// J266.
func LanguageByIDHandler(langs []language.Language) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.PathValue("id")
		id, err := strconv.Atoi(raw)
		if err != nil {
			httperr.BadRequest(w, "language: invalid id")
			return
		}
		// Filter the injected registry — tests can pass an empty or
		// subset slice. Production passes language.All().
		var matched *language.Language
		for i := range langs {
			if langs[i].ID == id {
				cp := langs[i]
				matched = &cp
				break
			}
		}
		if matched == nil {
			// Fall through to global lookup so that production callers
			// passing a partial registry still get the canonical entry.
			// (Defensive — most callers pass language.All().)
			if l, ok := language.FindByID(id); ok {
				matched = &l
			}
		}
		if matched == nil {
			httperr.NotFound(w, "language not found")
			return
		}
		out := LanguageResource{
			ID:        matched.ID,
			Name:      matched.Name,
			NameLower: strings.ToLower(matched.Name),
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// LocalizationByIDHandler returns GET /api/v5/localization/{id}.
// Mirrors `LocalizationController.GetResourceById(int id)` at
// `src/Sonarr.Api.V5/Localization/LocalizationController.cs:23-26`:
// IGNORES the id and returns the same dict as the list endpoint.
//
// J266.
func LocalizationByIDHandler(lister StringsLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// id is parsed but unused — match the C# signature which takes
		// `int id` but never reads it.
		if _, err := strconv.Atoi(r.PathValue("id")); err != nil {
			httperr.BadRequest(w, "localization: invalid id")
			return
		}
		body := LocalizationResource{Strings: lister.GetLocalizationDictionary()}
		writeJSON(w, http.StatusOK, body)
	})
}

// ----- private helpers -----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(v)
	_, _ = w.Write(buf)
}
