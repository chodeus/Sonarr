package v3

import (
	"encoding/json"
	"net/http"
	"strings"

	"sonarr.tv/sonarr/internal/language"
)

// LocalizationResource is the JSON shape produced by
// GET /api/v3/localization. Mirrors
// `src/Sonarr.Api.V3/Localization/LocalizationResource.cs`.
//
// The wire shape is `{ "strings": {key: value, ...} }` — one
// top-level wrapper around the i18n dictionary. The C# resource
// applies `[JsonConverter(typeof(LocalizationResourceSerializer))]`
// to preserve dict insertion order on emit; Go map iteration is
// non-deterministic but the contract-test normalizer alphabetizes
// keys before diff, so insertion order is unobservable on byte-eq.
type LocalizationResource struct {
	Strings map[string]string `json:"strings"`
}

// LocalizationStringsLister narrows the localization service to
// the dict-read surface the handler needs. *localization.Service
// satisfies it via GetLocalizationDictionary.
type LocalizationStringsLister interface {
	GetLocalizationDictionary() map[string]string
}

// LocalizationHandler returns the http.Handler for
// GET /api/v3/localization. Mirrors
// `LocalizationController.GetLocalization` at
// `src/Sonarr.Api.V3/Localization/LocalizationController.cs:30-34`.
// English-only today — per-user-language overlay is DEFERRED with
// the wider Localization sub-arc.
func LocalizationHandler(lister LocalizationStringsLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := LocalizationResource{Strings: lister.GetLocalizationDictionary()}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(body)
		_, _ = w.Write(buf)
	})
}

// LocalizationLanguageResource is the JSON shape produced by
// GET /api/v3/localization/language. Mirrors
// `src/Sonarr.Api.V3/Localization/LocalizationLanguageResource.cs`.
//
// Field-source mapping (C# → Go):
//
//	identifier → LocalizationService.GetLanguageIdentifier
//	             (BCP-47-style: TwoLetter[-COUNTRY])
type LocalizationLanguageResource struct {
	Identifier string `json:"identifier"`
}

// LanguageIdentifier resolves a UI Language Id to its BCP-47-style
// identifier, mirroring
// `src/NzbDrone.Core/Localization/LocalizationService.cs:87-98`:
//
//  1. Look up the Iso row for the configured Language.
//  2. If unknown (Id not in registry, or Language has no Iso entry),
//     fall back to English.
//  3. Identifier = TwoLetterCode; append "-COUNTRY" if CountryCode is
//     non-empty (e.g. PortugueseBrazil → "pt-BR").
//
// The country segment is uppercased to match C#
// `CountryCode.ToUpperInvariant()`.
func LanguageIdentifier(uiLanguageID int) string {
	var iso language.Iso
	if lang, ok := language.FindByID(uiLanguageID); ok {
		if found, isoOK := language.GetIso(lang); isoOK {
			iso = found
		}
	}
	if iso.TwoLetter == "" {
		// FindByID failed, or the Language has no Iso entry
		// (e.g. Unknown). Fall back to English.
		iso, _ = language.GetIso(language.English)
	}
	id := iso.TwoLetter
	if iso.Country != "" {
		id += "-" + strings.ToUpper(iso.Country)
	}
	return id
}

// LocalizationLanguageHandler returns the http.Handler for
// GET /api/v3/localization/language. uiLanguage is read on every
// request so a runtime SetUILanguage takes effect without a restart,
// matching the C# behaviour where IConfigService re-reads its cached
// row on every getter call.
func LocalizationLanguageHandler(uiLanguage func() int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := LocalizationLanguageResource{
			Identifier: LanguageIdentifier(uiLanguage()),
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(body)
		_, _ = w.Write(buf)
	})
}
