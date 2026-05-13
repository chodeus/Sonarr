package v3

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"sonarr.tv/sonarr/internal/language"
)

// LanguageResource is the JSON shape produced by GET /api/v3/language.
// Mirrors `src/Sonarr.Api.V3/Profiles/Languages/LanguageResource.cs`.
//
// C# inherits from RestResource which carries an Id property; the
// subclass re-declares Id with `[JsonIgnore(Condition = Never)]` so
// the field is emitted even when zero (Unknown = 0 must round-trip).
// Go's `json:"id"` tag without omitempty matches that semantic.
//
// Field-source mapping (C# → Go):
//
//	id         → Language.Id        (passthrough — IDs are persisted)
//	name       → Language.Name      (passthrough)
//	nameLower  → Name.ToLowerInvariant() (precomputed in ListAll)
type LanguageResource struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	NameLower string `json:"nameLower"`
}

// ListAll returns every Language as a LanguageResource, sorted to
// match `LanguageController.GetAll`:
//
//	OrderBy(l => l.Id > 0).ThenBy(l => l.Name)
//
// i.e. languages with Id <= 0 ("Original" id=-2, "Unknown" id=0) come
// first in name order, then everything with Id > 0 in name order. C#
// booleans sort false-before-true in LINQ's OrderBy, which collapses
// to "non-positive Ids first" here.
func ListAll(langs []language.Language) []LanguageResource {
	out := make([]LanguageResource, len(langs))
	for i, l := range langs {
		out[i] = LanguageResource{
			ID:        l.ID,
			Name:      l.Name,
			NameLower: strings.ToLower(l.Name),
		}
	}
	slices.SortStableFunc(out, func(a, b LanguageResource) int {
		ag, bg := bucket(a.ID), bucket(b.ID)
		if ag != bg {
			return ag - bg
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// bucket sorts non-positive Ids before positive ones — matches C#
// `OrderBy(l => l.Id > 0)` where false (0) precedes true (1).
func bucket(id int) int {
	if id > 0 {
		return 1
	}
	return 0
}

// LanguageHandler returns the http.Handler for GET /api/v3/language.
// langs is captured by closure so callers can inject a fixed registry
// for tests; production passes language.All().
func LanguageHandler(langs []language.Language) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(ListAll(langs))
		_, _ = w.Write(buf)
	})
}
