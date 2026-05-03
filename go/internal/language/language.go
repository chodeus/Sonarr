// Package language ports `NzbDrone.Core.Languages.Language` and the
// `IsoLanguages` registry from `NzbDrone.Core.Parser` plus the
// `Iso639BTMap` from `NzbDrone.Core.Organizer.FileNameBuilder`. Three
// concerns colocated because the C# tree splits them across three
// files but the data they carry is one logical registry of "every
// language Sonarr knows about, plus the ISO codes that map onto it".
//
// The Language type itself is a small (Id, Name) value mirroring the
// C# `Language` class. Each named constant (`English`, `Spanish`,
// etc.) is a package-level var of type `Language`. Equality is value-
// based: Language values compare equal iff their Ids match (Name is
// derived from Id, but both are checked structurally so a manually-
// constructed mismatch fails fast).
//
// Comparing Language values:
//
//	if l == language.English { ... }
//
// works because Language is a small POD with comparable fields.
//
// IDs are frozen — they are persisted in user databases (qualityprofiles,
// releaseprofiles, customformats) so renumbering would corrupt
// existing rows. Add new languages at the end with a fresh sequential
// Id; never reorder or repurpose Ids.
package language

import "strings"

// Language is one entry in the language registry. Stored persistently
// by Id (every row that references a language stores the int Id;
// Name is for display only).
type Language struct {
	ID   int
	Name string
}

// Predefined Language values. Every Id is fixed and load-bearing —
// the int values appear in user databases and cannot be reordered.
// Order in source mirrors `Language.cs:73-126` so a future drift
// against the C# tree is easier to spot.
var (
	Unknown          = Language{0, "Unknown"}
	English          = Language{1, "English"}
	French           = Language{2, "French"}
	Spanish          = Language{3, "Spanish"}
	German           = Language{4, "German"}
	Italian          = Language{5, "Italian"}
	Danish           = Language{6, "Danish"}
	Dutch            = Language{7, "Dutch"}
	Japanese         = Language{8, "Japanese"}
	Icelandic        = Language{9, "Icelandic"}
	Chinese          = Language{10, "Chinese"}
	Russian          = Language{11, "Russian"}
	Polish           = Language{12, "Polish"}
	Vietnamese       = Language{13, "Vietnamese"}
	Swedish          = Language{14, "Swedish"}
	Norwegian        = Language{15, "Norwegian"}
	Finnish          = Language{16, "Finnish"}
	Turkish          = Language{17, "Turkish"}
	Portuguese       = Language{18, "Portuguese"}
	Flemish          = Language{19, "Flemish"}
	Greek            = Language{20, "Greek"}
	Korean           = Language{21, "Korean"}
	Hungarian        = Language{22, "Hungarian"}
	Hebrew           = Language{23, "Hebrew"}
	Lithuanian       = Language{24, "Lithuanian"}
	Czech            = Language{25, "Czech"}
	Arabic           = Language{26, "Arabic"}
	Hindi            = Language{27, "Hindi"}
	Bulgarian        = Language{28, "Bulgarian"}
	Malayalam        = Language{29, "Malayalam"}
	Ukrainian        = Language{30, "Ukrainian"}
	Slovak           = Language{31, "Slovak"}
	Thai             = Language{32, "Thai"}
	PortugueseBrazil = Language{33, "Portuguese (Brazil)"}
	SpanishLatino    = Language{34, "Spanish (Latino)"}
	Romanian         = Language{35, "Romanian"}
	Latvian          = Language{36, "Latvian"}
	Persian          = Language{37, "Persian"}
	Catalan          = Language{38, "Catalan"}
	Croatian         = Language{39, "Croatian"}
	Serbian          = Language{40, "Serbian"}
	Bosnian          = Language{41, "Bosnian"}
	Estonian         = Language{42, "Estonian"}
	Tamil            = Language{43, "Tamil"}
	Indonesian       = Language{44, "Indonesian"}
	Macedonian       = Language{45, "Macedonian"}
	Slovenian        = Language{46, "Slovenian"}
	Azerbaijani      = Language{47, "Azerbaijani"}
	Uzbek            = Language{48, "Uzbek"}
	Malay            = Language{49, "Malay"}
	Urdu             = Language{50, "Urdu"}
	Romansh          = Language{51, "Romansh"}
	Georgian         = Language{52, "Georgian"}
	// Original is the special "use the series original audio language"
	// value, persisted with Id -2 (matches `Language.cs:126`).
	Original = Language{-2, "Original"}
)

// All returns every Language in registration order. Mirrors
// `Language.All` in the C# tree. Returned slice is a fresh copy so
// callers cannot mutate the registry.
func All() []Language {
	out := make([]Language, len(allLanguages))
	copy(out, allLanguages)
	return out
}

var allLanguages = []Language{
	Unknown, English, French, Spanish, German, Italian, Danish, Dutch,
	Japanese, Icelandic, Chinese, Russian, Polish, Vietnamese, Swedish,
	Norwegian, Finnish, Turkish, Portuguese, Flemish, Greek, Korean,
	Hungarian, Hebrew, Lithuanian, Czech, Arabic, Hindi, Bulgarian,
	Malayalam, Ukrainian, Slovak, Thai, PortugueseBrazil, SpanishLatino,
	Romanian, Latvian, Persian, Catalan, Croatian, Serbian, Bosnian,
	Estonian, Tamil, Indonesian, Macedonian, Slovenian, Azerbaijani,
	Uzbek, Malay, Urdu, Romansh, Georgian, Original,
}

var byID = func() map[int]Language {
	m := make(map[int]Language, len(allLanguages))
	for _, l := range allLanguages {
		m[l.ID] = l
	}
	return m
}()

// FindByID returns the Language with the given Id and whether it
// matched. Id 0 always resolves to Unknown (matches the C# guard at
// `Language.cs:196`); unknown ids return (Unknown, false).
func FindByID(id int) (Language, bool) {
	if id == 0 {
		return Unknown, true
	}
	l, ok := byID[id]
	if !ok {
		return Unknown, false
	}
	return l, true
}

// FindByName returns the Language with the given Name (case-
// insensitive comparison, matching `(string)Language` cast in
// `Language.cs:219-228`). Returns (Unknown, false) when no match.
func FindByName(name string) (Language, bool) {
	for _, l := range allLanguages {
		if strings.EqualFold(l.Name, name) {
			return l, true
		}
	}
	return Unknown, false
}
