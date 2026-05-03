package language_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/language"
)

// TestFindIso_English ports `should_return_iso_language_for_English`
// from src/NzbDrone.Core.Test/ParserTests/IsoLanguagesFixture.cs.
func TestFindIso_English(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"en", "eng", "en-US", "en-GB"} {
		iso, ok := language.FindIso(code)
		if !ok || iso.Language != language.English {
			t.Errorf("FindIso(%q) = (%+v, %v), want English", code, iso, ok)
		}
	}
}

// TestFindIso_UnknownOrInvalid ports
// `unknown_or_invalid_code_should_return_null`.
func TestFindIso_UnknownOrInvalid(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"enus", "enusa", "wo", "fr-CA"} {
		iso, ok := language.FindIso(code)
		if ok {
			t.Errorf("FindIso(%q) = (%+v, true), want false", code, iso)
		}
	}
}

// TestFindIso_Portuguese ports `should_return_portuguese`.
func TestFindIso_Portuguese(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"pt", "por", "pt-PT"} {
		iso, ok := language.FindIso(code)
		if !ok || iso.Language != language.Portuguese {
			t.Errorf("FindIso(%q) = (%+v, %v), want Portuguese", code, iso, ok)
		}
	}
}

// TestFindIso_DeAU ports `should_not_return_portuguese` (`de-AU`
// has no German variant for AU and German has a fixed `de` country
// code so the empty-country fallback also misses).
func TestFindIso_DeAU(t *testing.T) {
	t.Parallel()
	if iso, ok := language.FindIso("de-AU"); ok {
		t.Errorf("FindIso(de-AU) = (%+v, true), want false", iso)
	}
}

// TestFindIso_AlternateMapping ports
// `should_lookup_cze_via_alternate_iso_code_mapping`.
func TestFindIso_AlternateMapping(t *testing.T) {
	t.Parallel()
	iso, ok := language.FindIso("cze")
	if !ok || iso.Language != language.Czech {
		t.Errorf("FindIso(cze) = (%+v, %v), want Czech", iso, ok)
	}
}

// TestFindIso_Urdu ports `should_return_urdu`.
func TestFindIso_Urdu(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"ur", "urd", "ur-PK"} {
		iso, ok := language.FindIso(code)
		if !ok || iso.Language != language.Urdu {
			t.Errorf("FindIso(%q) = (%+v, %v), want Urdu", code, iso, ok)
		}
	}
}

// TestFindIso_Romansh ports `should_return_romansh`.
func TestFindIso_Romansh(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"rm", "roh", "rm-CH"} {
		iso, ok := language.FindIso(code)
		if !ok || iso.Language != language.Romansh {
			t.Errorf("FindIso(%q) = (%+v, %v), want Romansh", code, iso, ok)
		}
	}
}

// TestFindIso_Georgian ports `should_return_georgian`. Exercises both
// the alternate-mapping path AND the iso639BTMap (`geo → kat`).
func TestFindIso_Georgian(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"ka", "geo", "kat", "ka-GE"} {
		iso, ok := language.FindIso(code)
		if !ok || iso.Language != language.Georgian {
			t.Errorf("FindIso(%q) = (%+v, %v), want Georgian", code, iso, ok)
		}
	}
}

func TestFindIso_GetByLanguage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in        language.Language
		wantThree string
	}{
		{language.English, "eng"},
		{language.German, "deu"},
		{language.PortugueseBrazil, "por"},
		{language.Georgian, "kat"},
	}
	for _, c := range cases {
		iso, ok := language.GetIso(c.in)
		if !ok {
			t.Errorf("GetIso(%v) missing", c.in)
			continue
		}
		if iso.ThreeLetter != c.wantThree {
			t.Errorf("GetIso(%v).ThreeLetter = %q, want %q", c.in, iso.ThreeLetter, c.wantThree)
		}
	}

	if iso, ok := language.GetIso(language.Unknown); ok {
		t.Errorf("GetIso(Unknown) = (%+v, true), want false (no Iso for Unknown)", iso)
	}
}

func TestFindIso_FindIsoByName(t *testing.T) {
	t.Parallel()
	iso, ok := language.FindIsoByName("English")
	if !ok || iso.Language != language.English {
		t.Errorf("FindIsoByName(English) = (%+v, %v), want English", iso, ok)
	}
	iso, ok = language.FindIsoByName("english")
	if !ok || iso.Language != language.English {
		t.Errorf("FindIsoByName(english) = (%+v, %v), want English (case-insensitive)", iso, ok)
	}
	if _, ok := language.FindIsoByName("Klingon"); ok {
		t.Errorf("FindIsoByName(Klingon) ok=true, want false")
	}
}

func TestFindIso_CountryLower(t *testing.T) {
	t.Parallel()
	iso, _ := language.FindIso("pt-BR")
	if iso.CountryLower() != "br" {
		t.Errorf("CountryLower = %q, want br", iso.CountryLower())
	}
}
