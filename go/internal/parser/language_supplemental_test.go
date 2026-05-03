package parser

import (
	"testing"

	"sonarr.tv/sonarr/internal/language"
)

// White-box tests for the LanguageParser supplemental regexes.
//
// Targets:
//   - regexLanguagesCS — case-sensitive arm of `LanguageParser.RegexLanguage`
//     at `LanguageParser.cs:354-384`, plus the `[\W_]SUB` / `SUB[\W_]`
//     adjacency filters from `:28`.
//   - IsGermanDualLanguage — `(?<!WEB[-_. ]?)\bDL\b` from `:31`.
//   - IsGermanMultiLanguage — `\bML\b` from `:32`.
//
// Cases ported verbatim from
// `src/NzbDrone.Core.Test/ParserTests/LanguageParserFixture.cs` where
// the discriminator is one of these primitives.

func TestRegexLanguagesCS_FixtureRows(t *testing.T) {
	cases := []struct {
		title string
		want  language.Language
	}{
		// from `should_parse_language_lithuanian`
		{"Title.the.Series.S05E01.WEBRip.x264.AC3.LT.EN-CNN", language.Lithuanian},
		// LV/RU don't need the case-sensitive arm here — included to
		// confirm LT is detected alongside the other tokens. RU is
		// caught by slice (a)'s case-insensitive arm; LV is caught by
		// slice (a) too (`\b(?:lat|lav|lv)\b`). Slice (b) cares about LT.
		{"Title.the.Series.S01.COMPLETE.2009.1080p.WEB-DL.x264.AVC.AAC.LT.LV.RU", language.Lithuanian},

		// from `should_parse_language_czech`. The fixture title contains
		// zero-width spaces (U+200B) between several tokens — written
		// here with explicit `\u200b` escapes so the source file stays
		// staticcheck-clean (ST1018 flags raw Unicode-format chars in
		// string literals). Zero-width spaces are non-word, so
		// `\bCZ\b` fires at `-\u200bCZ\u200b.`.
		{"Title.the.Series.\u200bS07E11.\u200bWEB Rip.\u200bXviD.\u200bLouige-\u200bCZ.\u200bEN.\u200b5.\u200b1", language.Czech},

		// from `should_parse_language_polish` — bare `PL` form (the
		// PLDUB / PL-DUB / etc. forms are slice (a)'s case-insensitive arm)
		{"Title.the.Series.2009.S01E14.PL.HDTV.XviD-LOL", language.Polish},

		// from `should_parse_language_bulgarian` — bare `BG` form (the
		// BGAUDIO form is slice (a)'s case-insensitive arm)
		{"Title.the.Series.2009.S01E14.BG.AUDIO.HDTV.XviD-LOL", language.Bulgarian},

		// from `should_parse_language_slovak` — bare `SK` form (the
		// `Slovak` literal is .Contains-driven in slice c)
		{"Title.the.Series.2021.S01E11.HDTV.XviD.ENG.SK-LOL", language.Slovak},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := regexLanguagesCS(tc.title)
			if !contains(got, tc.want) {
				t.Errorf("regexLanguagesCS(%q) = %v, want to contain %v", tc.title, got, tc.want)
			}
		})
	}
}

// TestRegexLanguagesCS_CaseSensitivity pins that the alternation is
// case-SENSITIVE — only uppercase 2-letter tokens count. The C# regex
// has no `(?i)` at the outer scope; the inline `(?i)` only scopes the
// SUB-adjacency lookarounds.
func TestRegexLanguagesCS_CaseSensitivity(t *testing.T) {
	cases := []struct {
		title    string
		wantNone bool
	}{
		// Lowercase variants do NOT match.
		{"Foo.lt.Bar", true},
		{"Foo.cz.Bar", true},
		{"Foo.pl.Bar", true},
		{"Foo.bg.Bar", true},
		{"Foo.sk.Bar", true},
		{"Foo.de.Bar", true},
		// Mixed-case does NOT match (only fully uppercase).
		{"Foo.Lt.Bar", true},
		{"Foo.Cz.Bar", true},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := regexLanguagesCS(tc.title)
			if tc.wantNone && got != nil {
				t.Errorf("regexLanguagesCS(%q) = %v, want nil", tc.title, got)
			}
		})
	}
}

// TestRegexLanguagesCS_SubAdjacencyFilter pins the
// `(?<!SUB[\W_])` and `(?![\W_]SUB)` filters from `LanguageParser.cs:28`.
// SUB followed/preceded by a `[\W_]` boundary must drop the match —
// `LT-SUB` and `SUB-LT` are subtitle markers, not Lithuanian releases.
func TestRegexLanguagesCS_SubAdjacencyFilter(t *testing.T) {
	cases := []struct {
		title    string
		wantNone bool
	}{
		// SUB-prefix forms — pre-filter rejects.
		{"Foo.SUB-LT.Bar", true},
		{"Foo.SUB.LT.Bar", true},
		{"Foo.SUB_LT.Bar", true},
		{"Foo.sub-LT.Bar", true}, // case-insensitive SUB
		{"Foo.Sub.CZ.Bar", true},

		// SUB-suffix forms — post-filter rejects.
		{"Foo.LT-SUB.Bar", true},
		{"Foo.LT.SUB.Bar", true},
		{"Foo.LT_SUB.Bar", true},
		{"Foo.LT-sub.Bar", true},

		// Plain forms — no SUB adjacency, match should fire.
		{"Foo.LT.Bar", false},
		{"Foo.CZ.Bar", false},
		{"Foo.SK-LOL.Bar", false},
		// SUBSCRIBE doesn't trigger because the chars after SUB are
		// alphanumeric (not in `[\W_]`).
		{"Foo.SUBSCRIBE.LT.Bar", false},
		// LTD is not LT (the `\b` between T and D doesn't fire — both
		// word chars), so neither LT nor any other arm catches.
		{"Foo.LTD.Bar", true},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := regexLanguagesCS(tc.title)
			if tc.wantNone && got != nil {
				t.Errorf("regexLanguagesCS(%q) = %v, want nil", tc.title, got)
			}
			if !tc.wantNone && got == nil {
				t.Errorf("regexLanguagesCS(%q) = nil, want non-nil", tc.title)
			}
		})
	}
}

// TestIsGermanDualLanguage pins the `(?<!WEB[-_. ]?)\bDL\b` semantic.
// `WEB-DL` / `WEB DL` / `WEB.DL` are source tags and must NOT register;
// bare `DL` (or `DL` after a non-WEB token) IS the German Dual Language tag.
func TestIsGermanDualLanguage(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		// from `should_add_original_language_to_german_release_with_dl_tag`
		{"Series.Title.S01E01.German.DL.1080p.BluRay.x264-RlsGrp", true},
		{"Series.Title.S01E01.GERMAN.DL.1080P.WEB.H264-RlsGrp", true},
		{"Series.Title.2023.S01E01.German.DL.EAC3.1080p.DSNP.WEB.H264-RlsGrp", true},
		{"Series.Title.S02E10.Episode.Title.German.DL.BD.x264-TVS", true},
		{"Series.Title.S02E09.EpisodeName.German.DL.BD.x264-TVS", true},

		// from `should_not_add_original_language_to_german_release_when_title_contains_web_dl`
		{"Series.Title.2023.S01E01.GERMAN.1080P.WEB-DL.H264-RlsGrp", false},
		{"Series.Title.2023.S01E01.GERMAN.1080P.WEB.DL.H264-RlsGrp", false},
		{"Series Title 2023 S01E01 GERMAN 1080P WEB DL H264-RlsGrp", false},
		// `WEBDL` (no separator): \bDL\b doesn't fire inside (B and D
		// both word chars, no \b boundary between).
		{"Series.Title.2023.S01E01.GERMAN.1080P.WEBDL.H264-RlsGrp", false},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := IsGermanDualLanguage(tc.title)
			if got != tc.want {
				t.Errorf("IsGermanDualLanguage(%q) = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}

// TestIsGermanMultiLanguage pins `\bML\b`.
func TestIsGermanMultiLanguage(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		// from `should_add_original_language_and_english_to_german_release_with_ml_tag`
		{"Series.Title.2023.S01.German.ML.EAC3.1080p.NF.WEB.H264-RlsGrp", true},
		// case-insensitive
		{"Series.Title.S01.German.ml.WEB.H264", true},
		// negatives
		{"Series.Title.S01.German.MLB.WEB.H264", false}, // MLB, not ML
		{"Series.Title.S01.German.WEB.H264", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := IsGermanMultiLanguage(tc.title)
			if got != tc.want {
				t.Errorf("IsGermanMultiLanguage(%q) = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}

// TestRegexLanguagesCS_Pins pins regex-construction invariants.
func TestRegexLanguagesCS_Pins(t *testing.T) {
	for _, g := range caseSensitiveGroupOrder {
		if idx := caseSensitiveLanguageRe.SubexpIndex(g.name); idx <= 0 {
			t.Errorf("named group %q not present in caseSensitiveLanguageRe", g.name)
		}
	}
	// caseSensitiveLanguageRe must NOT have `(?i)` at the head — that
	// would propagate case-insensitivity into the alternation, which
	// would cause `lt` (lowercase) to match Lithuanian. The C# regex
	// scopes `(?i)` only into the lookarounds.
	if got := caseSensitiveLanguageRe.String(); len(got) >= 4 && got[:4] == "(?i)" {
		t.Errorf("caseSensitiveLanguageRe must be case-sensitive at the top level; got prefix %q", got[:4])
	}
}
