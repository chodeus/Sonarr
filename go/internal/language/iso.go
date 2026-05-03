package language

import "strings"

// Iso is one row in the ISO-language registry: a Language plus the
// 2-letter (ISO 639-1) and 3-letter (ISO 639-2) codes plus optional
// 2-letter country code for region-specific variants
// (PortugueseBrazil, SpanishLatino). Mirrors
// `NzbDrone.Core.Parser.IsoLanguage`.
type Iso struct {
	TwoLetter   string
	Country     string
	ThreeLetter string
	Language    Language
}

// CountryLower returns the lowercase country code (or "" if unset).
// Mirrors `IsoLanguage.CountyCodeLower` in the C# tree (note the
// typo "Couny" — preserved as a function name pun in the C# source;
// the Go port uses the correct spelling).
func (i Iso) CountryLower() string { return strings.ToLower(i.Country) }

// isoAll mirrors the `IsoLanguages.All` HashSet at
// `IsoLanguages.cs:11-66`. Same order so a diff against the C# tree
// stays line-aligned.
var isoAll = []Iso{
	{"en", "", "eng", English},
	{"fr", "fr", "fra", French},
	{"es", "", "spa", Spanish},
	{"de", "de", "deu", German},
	{"it", "", "ita", Italian},
	{"da", "", "dan", Danish},
	{"nl", "", "nld", Dutch},
	{"ja", "", "jpn", Japanese},
	{"is", "", "isl", Icelandic},
	{"zh", "cn", "zho", Chinese},
	{"ru", "", "rus", Russian},
	{"pl", "", "pol", Polish},
	{"vi", "", "vie", Vietnamese},
	{"sv", "", "swe", Swedish},
	{"no", "", "nor", Norwegian},
	{"nb", "", "nob", Norwegian},
	{"fi", "", "fin", Finnish},
	{"tr", "", "tur", Turkish},
	{"pt", "pt", "por", Portuguese},
	{"nl", "", "nld", Flemish},
	{"el", "", "ell", Greek},
	{"ko", "", "kor", Korean},
	{"hu", "", "hun", Hungarian},
	{"he", "", "heb", Hebrew},
	{"lt", "", "lit", Lithuanian},
	{"cs", "", "ces", Czech},
	{"ar", "", "ara", Arabic},
	{"hi", "", "hin", Hindi},
	{"bg", "", "bul", Bulgarian},
	{"ml", "", "mal", Malayalam},
	{"uk", "", "ukr", Ukrainian},
	{"sk", "", "slk", Slovak},
	{"th", "th", "tha", Thai},
	{"pt", "br", "por", PortugueseBrazil},
	{"es", "mx", "spa", SpanishLatino},
	{"ro", "", "ron", Romanian},
	{"lv", "", "lav", Latvian},
	{"fa", "", "fas", Persian},
	{"ca", "", "cat", Catalan},
	{"hr", "", "hrv", Croatian},
	{"sr", "", "srp", Serbian},
	{"bs", "", "bos", Bosnian},
	{"et", "", "est", Estonian},
	{"ta", "", "tam", Tamil},
	{"id", "", "ind", Indonesian},
	{"mk", "", "mkd", Macedonian},
	{"sl", "", "slv", Slovenian},
	{"az", "", "aze", Azerbaijani},
	{"uz", "", "uzb", Uzbek},
	{"ms", "", "msa", Malay},
	{"ur", "", "urd", Urdu},
	{"rm", "", "roh", Romansh},
	{"ka", "", "kat", Georgian},
}

// alternateIsoCodeMappings handles a few legacy / regional variant
// codes that don't appear in isoAll. Mirrors
// `IsoLanguages.AlternateIsoCodeMappings` at `IsoLanguages.cs:68-76`.
// Lookup is case-sensitive — the C# Dictionary uses default
// StringComparer; the test fixture only exercises lowercase keys, and
// preserving case-sensitivity matches behaviour.
var alternateIsoCodeMappings = map[string]Language{
	"cze":  Czech,
	"dut":  Dutch,
	"mac":  Macedonian,
	"rum":  Romanian,
	"yue":  Chinese,
	"zhtw": Chinese,
}

// iso639BTMap maps ISO 639-2/B (bibliographic) codes onto ISO 639-2/T
// (terminological) codes, e.g. `geo → kat` (Georgian). The C# tree
// stores this under `FileNameBuilder.Iso639BTMap`; for the Go port
// we colocate with the rest of the language registry. Generated from
// https://www.loc.gov/standards/iso639-2/ISO-639-2_utf-8.txt — same
// table the C# comment cites at `FileNameBuilder.cs:88`.
var iso639BTMap = map[string]string{
	"alb": "sqi",
	"arm": "hye",
	"baq": "eus",
	"bur": "mya",
	"chi": "zho",
	"cze": "ces",
	"dut": "nld",
	"fre": "fra",
	"geo": "kat",
	"ger": "deu",
	"gre": "ell",
	"gsw": "deu",
	"ice": "isl",
	"mac": "mkd",
	"mao": "mri",
	"may": "msa",
	"per": "fas",
	"rum": "ron",
	"slo": "slk",
	"tib": "bod",
	"wel": "cym",
}

// FindIso resolves an ISO language code (2-letter, 3-letter, or
// 2-letter with country, e.g. `pt-BR`) to an Iso row. Returns
// (Iso{}, false) when no match. Algorithm mirrors
// `IsoLanguages.Find` at `IsoLanguages.cs:78-113` exactly:
//
//  1. Split on `-`. Lowercase the first part.
//  2. If the first part is 2 chars: filter by TwoLetter. If a
//     country code is present, prefer rows whose Country matches it;
//     otherwise prefer rows with empty Country. Return the first
//     match, or (Iso{}, false) if none.
//  3. Else if the FULL isoCode (not the lowercased langCode) is in
//     alternateIsoCodeMappings: return GetIso of the mapped Language.
//     The C# code uses the original isoCode here — preserved for
//     parity, even though it means `CZE` does NOT match `cze`.
//  4. Else if the first part is 3 chars: apply iso639BTMap if
//     present, then filter by ThreeLetter.
//  5. Otherwise: not found.
func FindIso(isoCode string) (Iso, bool) {
	parts := strings.Split(isoCode, "-")
	langCode := strings.ToLower(parts[0])

	if len(langCode) == 2 {
		candidates := make([]Iso, 0, 4)
		for _, iso := range isoAll {
			if iso.TwoLetter == langCode {
				candidates = append(candidates, iso)
			}
		}
		if len(parts) > 1 {
			country := strings.ToLower(parts[1])
			withCountry := filterByCountry(candidates, country)
			if len(withCountry) > 0 {
				candidates = withCountry
			} else {
				candidates = filterByCountry(candidates, "")
			}
		}
		if len(candidates) == 0 {
			return Iso{}, false
		}
		return candidates[0], true
	}

	if alt, ok := alternateIsoCodeMappings[isoCode]; ok {
		return GetIso(alt)
	}

	if len(langCode) == 3 {
		if mapped, ok := iso639BTMap[langCode]; ok {
			langCode = mapped
		}
		for _, iso := range isoAll {
			if iso.ThreeLetter == langCode {
				return iso, true
			}
		}
	}
	return Iso{}, false
}

// GetIso returns the first Iso whose Language matches l. Mirrors
// `IsoLanguages.Get(Language)` at `IsoLanguages.cs:115-118`. Returns
// (Iso{}, false) if l is not in the registry (e.g. Unknown).
func GetIso(l Language) (Iso, bool) {
	for _, iso := range isoAll {
		if iso.Language == l {
			return iso, true
		}
	}
	return Iso{}, false
}

// FindIsoByName returns the first Iso whose Language.Name matches
// name (case-insensitive). Mirrors `IsoLanguages.FindByName` at
// `IsoLanguages.cs:120-123`.
func FindIsoByName(name string) (Iso, bool) {
	for _, iso := range isoAll {
		if strings.EqualFold(iso.Language.Name, name) {
			return iso, true
		}
	}
	return Iso{}, false
}

func filterByCountry(in []Iso, country string) []Iso {
	out := in[:0:0]
	for _, iso := range in {
		if iso.Country == country {
			out = append(out, iso)
		}
	}
	return out
}
