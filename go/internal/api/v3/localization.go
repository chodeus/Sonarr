package v3

import (
	"encoding/json"
	"net/http"
	"strings"

	"sonarr.tv/sonarr/internal/language"
)

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
