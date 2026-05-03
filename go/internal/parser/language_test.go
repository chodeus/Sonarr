package parser

import (
	"testing"

	"sonarr.tv/sonarr/internal/language"
)

// White-box tests for the LanguageParser regex slice.
//
// Targets `regexLanguagesCI` — the case-insensitive arm of
// `LanguageParser.RegexLanguage` at `LanguageParser.cs:386-520` plus the
// LanguageRegex outer wrapper at `LanguageParser.cs:25`. The companion
// case-sensitive arm (LT/CZ/PL/BG/SK/DE) and the .Contains-based
// detection in ParseLanguages (Spanish/Danish/Dutch/etc.) are deferred
// to slices b and c — fixture cases whose only discriminator is one of
// those arms are NOT covered here.
//
// Cases ported verbatim from `src/NzbDrone.Core.Test/ParserTests/LanguageParserFixture.cs`.

// TestRegexLanguagesCI_Single ports single-language fixture rows whose
// expected detection comes from the case-insensitive LanguageRegex
// (rather than the .Contains arm or the case-sensitive arm).
func TestRegexLanguagesCI_Single(t *testing.T) {
	cases := []struct {
		title string
		want  language.Language
	}{
		// from `should_parse_language_french`
		{"Title.the.Series.2009.S01E14.French.HDTV.XviD-LOL", language.French},
		{"Title.the.Series.The.1x13.Tueurs.De.Flics.FR.DVDRip.XviD", language.French},
		{"Title.S01.720p.VF.WEB-DL.AAC2.0.H.264-BTN", language.French},
		{"Title.S01.720p.VF2.WEB-DL.AAC2.0.H.264-BTN", language.French},
		{"Title.S01.720p.VFF.WEB-DL.AAC2.0.H.264-BTN", language.French},
		{"Title.S01.720p.VFQ.WEB-DL.AAC2.0.H.264-BTN", language.French},
		{"Title.S01.720p.TRUEFRENCH.WEB-DL.AAC2.0.H.264-BTN", language.French},

		// from `should_parse_language_german`
		{"The Series Title - S02E16 - Kampfhaehne - mkv - by Videomann", language.German},
		{"Series.Title.S01E03.Ger.Dub.AAC.1080p.WebDL.x264-TKP21", language.German},
		{"Series.Title.S01E10.EpisodeName.SwissGerman.WEB-DL.h264-RlsGrp", language.German},

		// from `should_parse_language_italian`
		{"Title.the.Series.1x19.ita.720p.bdmux.x264-novarip", language.Italian},

		// from `should_parse_language_japanese`
		{"[Erai-raws] To Be Series - 14 (JA) [1080p CR WEB-DL AVC AAC][MultiSub]", language.Japanese},

		// from `should_parse_language_chinese` (regex-detected branches only —
		// the `Chinese` / `Cantonese` / `Mandarin` rows are .Contains-driven)
		{"[abc] My Series - 01 [CHS]", language.Chinese},
		{"[abc] My Series - 01 [CHT]", language.Chinese},
		{"[abc] My Series - 01 [BIG5]", language.Chinese},
		{"[abc] My Series - 01 [GB]", language.Chinese},
		{"[abc] My Series - 01 [繁中]", language.Chinese},
		{"[abc] My Series - 01 [繁体]", language.Chinese},
		{"[abc] My Series - 01 [简繁外挂]", language.Chinese},
		{"[abc] My Series - 01 [简繁内封字幕]", language.Chinese},
		{"[喵萌奶茶屋&LoliHouse] 拳愿阿修罗 / Kengan Ashura - 17 [WebRip 1080p HEVC-10bit AAC][中日双语字幕]", language.Chinese},
		{"Series.Towards.You.S01.国语音轨.2023.1080p.NF.WEB-DL.H264.DDP2.0-SeeWEB", language.Chinese},

		// from `should_parse_language_polish` (PL-DUB / PL-LEK / etc. branches —
		// bare `PL` is case-sensitive, deferred to slice b)
		{"Title.the.Series.2009.S01E14.PLLEK.HDTV.XviD-LOL", language.Polish},
		{"Title.the.Series.2009.S01E14.PL-LEK.HDTV.XviD-LOL", language.Polish},
		{"Title.the.Series.2009.S01E14.LEKPL.HDTV.XviD-LOL", language.Polish},
		{"Title.the.Series.2009.S01E14.LEK-PL.HDTV.XviD-LOL", language.Polish},
		{"Title.the.Series.2009.S01E14.PLDUB.HDTV.XviD-LOL", language.Polish},
		{"Title.the.Series.2009.S01E14.PL-DUB.HDTV.XviD-LOL", language.Polish},
		{"Title.the.Series.2009.S01E14.DUBPL.HDTV.XviD-LOL", language.Polish},
		{"Title.the.Series.2009.S01E14.DUB-PL.HDTV.XviD-LOL", language.Polish},

		// from `should_parse_language_flemish` / `_greek`
		{"Title.the.Series.S01E01.FLEMISH.HDTV.x264-BRiGAND", language.Flemish},
		{"Title.the.Series.S03E13.Greek.PDTV.XviD-Ouzo", language.Greek},

		// from `should_parse_language_hungarian`
		{"Title.the.Series.2009.S01E14.HDTV.XviD.HUNDUB-LOL", language.Hungarian},
		{"Title.the.Series.2009.S01E14.HDTV.XviD.HUN-LOL", language.Hungarian},

		// from `should_parse_language_hebrew`
		{"Title.the.Series.S01-03.DVDRip.HebDub", language.Hebrew},

		// from `should_parse_language_bulgarian` (BGAUDIO arm only;
		// the bare `BG` form needs slice b's case-sensitive regex)
		{"Title.the.Series.2009.S01E14.BGAUDIO.HDTV.XviD-LOL", language.Bulgarian},

		// from `should_parse_language_thai`
		{"Thai.Series.Title.S01.THAI.1080p.WEBRip.x265-RARBG", language.Thai},
		{"Series.Title.S02.THAI.1080p.NF.WEBRip.DDP2.0.x264-PAAI[rartv]", language.Thai},

		// from `should_parse_language_romanian`
		{"TV.Series.S01.720p.WEB-DL.RoDubbed-RLSGRP", language.Romanian},
		{"TV Series S22E36 ROMANIAN 1080p WEB-DL AAC 2.0 H.264-RLSGRP", language.Romanian},

		// from `should_parse_language_urdu`
		{"Title.the.Series.2009.S01E14.Urdu.HDTV.XviD-LOL", language.Urdu},

		// from `should_parse_language_romansh`
		{"Title.the.Series.2025.S01.Romansh.1080p.WEB-DL.h264-RlsGrp", language.Romansh},
		{"Title.the.Series.2025.S01.Rumantsch.1080p.WEB.DL.h264-RlsGrp", language.Romansh},
		{"Title the Series 2025 S01 Romansch 1080p WEB DL h264-RlsGrp", language.Romansh},

		// from `should_parse_language_georgian`
		{"Title.the.Series.2025.S01.Georgian.1080p.WEB-DL.h264-RlsGrp", language.Georgian},
		{"Title.the.Series.2025.S01.Geo.1080p.WEB-DL.h264-RlsGrp", language.Georgian},

		// from `should_parse_original_title_from_release_name`
		{"Series.Title.S01E01.Original.1080P.WEB.H264-RlsGrp", language.Original},
		{"Series.Title.S01E01.Orig.1080P.WEB.H264-RlsGrp", language.Original},
	}

	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := regexLanguagesCI(tc.title)
			if !contains(got, tc.want) {
				t.Errorf("regexLanguagesCI(%q) = %v, want to contain %v", tc.title, got, tc.want)
			}
		})
	}
}

// TestRegexLanguagesCI_Multi ports multi-language fixture rows whose
// detection comes from the case-insensitive arm (no .Contains needed).
func TestRegexLanguagesCI_Multi(t *testing.T) {
	cases := []struct {
		title string
		want  []language.Language
	}{
		// from `should_parse_language_french` — multi-language packs.
		// The underscore-separated form catches French only (the french
		// arm has explicit `(?:_|\b)` boundaries; english/italian rely on
		// `\b` alone, and `_` is a word char in ASCII `\w` so `\b(?:eng)\b`
		// does NOT fire at `_ENG_`).
		{
			"Series_Title_S01_ENG_ITA_FRA_AAC_1080p_WebDL_x264",
			[]language.Language{language.French},
		},
		// The period-dash-period form catches all three (period + dash
		// are non-word so `\b` fires on every token boundary).
		{
			"Series.Title.S01.ENG-ITA-FRA.AAC.1080p.WebDL.x264",
			[]language.Language{language.English, language.Italian, language.French},
		},
		{
			"Series.Title.S01.ENG-ITA-FRE.AAC.1080p.WebDL.x264",
			[]language.Language{language.English, language.Italian, language.French},
		},

		// from `should_parse_language_french_english`
		{
			"Series Title S01 1080p Eng Fra [mkvonly]",
			[]language.Language{language.English, language.French},
		},
		{
			"Series Title S01 1080p FR ENG [mkvonly]",
			[]language.Language{language.French, language.English},
		},

		// from `should_parse_language_italian` — verifies italian arm
		// in multi-language pack
		{
			"Title.the.Series.ENG-FRE-ITA.AAC.1080p.WebDL.x264",
			[]language.Language{language.English, language.French, language.Italian},
		},

		// from `should_parse_language_russian`
		{
			"Title.the.Series.S01E01.1080p.WEB-DL.Rus.Eng.TVKlondike",
			[]language.Language{language.Russian, language.English},
		},

		// from `should_parse_language_latvian` (LAT in pack)
		{
			"Title.the.Series.2009.S01E08.2160p.WEB-DL.LAV.ENG",
			[]language.Language{language.Latvian, language.English},
		},
		{
			"Title.the.Series.S03.1080p.WEB.x264.LAT.ENG",
			[]language.Language{language.Latvian, language.English},
		},

		// from `should_parse_language_ukrainian` — Ukr/Eng pack
		{
			"Гало(Сезон 1, серії 1-5) / SeriesTitle(Season 1, episodes 1-5) (2022) WEBRip-AVC Ukr/Eng",
			[]language.Language{language.Ukrainian, language.English},
		},

		// from the C# `should_parse_turkish_and_ukra` + `nian` fixture
		// method (the original C# method name has a typo for ukrainian)
		{
			"Серія (Сезон 1, серії 01-26 із 51) / Seri (Season 1, episodes 01-26) (2018) WEBRip-AVC 2Ukr/Tur",
			[]language.Language{language.Ukrainian, language.Turkish},
		},

		// from `should_parse_language_english_french_german_italian_portuguese_spanish`
		{
			"The Boys S02 Eng Fre Ger Ita Por Spa 2160p WEBMux HDR10Plus HDR HEVC DDP SGF",
			[]language.Language{language.English, language.French, language.German, language.Italian, language.Portuguese, language.Spanish},
		},

		// from `should_parse_spanish_and_catalan` — esp + cat-DD/subs
		{
			"Series S01 [WEB-DL NF 1080p EAC3 2.0 esp cat Subs][HDOlimpo]",
			[]language.Language{language.Spanish, language.Catalan},
		},
	}

	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := regexLanguagesCI(tc.title)
			for _, w := range tc.want {
				if !contains(got, w) {
					t.Errorf("regexLanguagesCI(%q) = %v, missing %v", tc.title, got, w)
				}
			}
		})
	}
}

// TestRegexLanguagesCI_SubsFilter pins the LanguageRegex outer wrapper
// at `LanguageParser.cs:25`: language tokens inside a `(?:[-_. ](?:lang))*[-_. ]subs?`
// suffix must be filtered out.
func TestRegexLanguagesCI_SubsFilter(t *testing.T) {
	cases := []struct {
		title           string
		notExpectedLang []language.Language
	}{
		// from `should_parse_language_unknown` — Ger.Eng.Sub variants.
		// All language tokens followed by `.Sub.` should be dropped.
		{
			"Detektiv.Conan.1996.S33E39.Ger.Eng.Sub.AAC.1080p.WEB.H264-WeebPinn",
			[]language.Language{language.German, language.English},
		},
		{
			"Detektiv.Conan.1996.S33E39.Ger.Eng.Fre.Sub.AAC.1080p.WEB.H264-WeebPinn",
			[]language.Language{language.German, language.English, language.French},
		},
		{
			"Detektiv.Conan.1996.S33E39.Ger.Fre.Eng.Sub.AAC.1080p.WEB.H264-WeebPinn",
			[]language.Language{language.German, language.English, language.French},
		},
		{
			"Detektiv.Conan.1996.S33E39.Ger.Eng.Spa.Sub.AAC.1080p.WEB.H264-WeebPinn",
			[]language.Language{language.German, language.English, language.Spanish},
		},
		{
			"Detektiv.Conan.1996.S33E39.Ger.Spa.Eng.Sub.AAC.1080p.WEB.H264-WeebPinn",
			[]language.Language{language.German, language.English, language.Spanish},
		},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := regexLanguagesCI(tc.title)
			for _, l := range tc.notExpectedLang {
				if contains(got, l) {
					t.Errorf("regexLanguagesCI(%q) = %v, should NOT contain %v (subs-filtered)", tc.title, got, l)
				}
			}
		})
	}
}

// TestRegexLanguagesCI_SpaLatinoFilter pins the spanish-arm
// `spa(?!\(Latino\))` lookahead from `LanguageParser.cs:23`. A bare
// `spa` followed by `(Latino)` must NOT register as Spanish — the
// SpanishLatino classification (driven by .Contains("latino") in slice c)
// is what should fire instead.
func TestRegexLanguagesCI_SpaLatinoFilter(t *testing.T) {
	// from `should_parse_language_spanish_latino`. `_Eng-Spa(Latino)_`
	// — only the `Spa(Latino)` portion is relevant to slice (a) here:
	// the `_Eng-` token does NOT match `\b(?:eng)\b` because `_` is a
	// word char in ASCII `\w`, so the leading `\b` fails. The Spa->Latino
	// filter is the discriminator under test.
	got := regexLanguagesCI("Series.Title.S01.2019.720p_Eng-Spa(Latino)_MovieClubMx")
	if contains(got, language.Spanish) {
		t.Errorf("regexLanguagesCI Spa(Latino) = %v, should NOT contain Spanish", got)
	}

	// Bare `Spa` not followed by `(Latino)` DOES register as Spanish.
	got = regexLanguagesCI("Foo.Spa.Bar")
	if !contains(got, language.Spanish) {
		t.Errorf("regexLanguagesCI bare Spa = %v, expected Spanish", got)
	}
}

// TestRegexLanguagesCI_Negative confirms titles with no regex-detected
// language tokens return nil — the .Contains arm in slice c handles
// these via lowercase substring matches like `lower.Contains("spanish")`.
func TestRegexLanguagesCI_Negative(t *testing.T) {
	cases := []string{
		"",
		"Title.the.Series.2009.S01E14.HDTV.XviD-LOL",
		"Series Title - S01E01 - Pilot.sub",
		// `Spanish` / `Italian` / `Korean` / etc. as the literal English
		// word — these are .Contains-driven (slice c), not regex-driven,
		// so the slice (a) helper returns nil.
		"Title.the.Series.2009.S01E14.Spanish.HDTV.XviD-LOL",
		"Title.the.Series.2009.S01E14.Korean.HDTV.XviD-LOL",
		"Title.the.Series.2009.S01E14.Vietnamese.HDTV.XviD-LOL",
		"Title.the.Series.2009.S01E14.Swedish.HDTV.XviD-LOL",
	}
	for _, title := range cases {
		t.Run(title, func(t *testing.T) {
			if got := regexLanguagesCI(title); got != nil {
				t.Errorf("regexLanguagesCI(%q) = %v, want nil", title, got)
			}
		})
	}
}

// TestLanguagesOnlyExpr_Pins pins regex-construction invariants so a
// future edit to languagesOnlyExpr is caught immediately if it changes
// the named-group set or the dynamic-interpolation mechanism.
func TestLanguagesOnlyExpr_Pins(t *testing.T) {
	// Every entry in languageGroupOrder must resolve to a real
	// SubexpIndex on languageRe. Drift here means the regex string and
	// the consumer table fell out of sync.
	for _, g := range languageGroupOrder {
		if idx := languageRe.SubexpIndex(g.name); idx <= 0 {
			t.Errorf("named group %q not present in languageRe", g.name)
		}
	}

	// languagesOnlyNonCapturingExpr must contain zero `(?P<` headers —
	// the suffix-filter regex would otherwise hit RE2's duplicate-named-
	// group rejection when interpolated into subsFilterRe.
	if namedGroupHeader.MatchString(languagesOnlyNonCapturingExpr) {
		t.Errorf("languagesOnlyNonCapturingExpr still contains named-group headers")
	}

	// subsFilterRe MUST be anchored at `^` so MatchString(name[matchEnd:])
	// resolves the C# negative-lookahead semantic. This is a literal
	// pin — the source string starts with `(?i)^`.
	if got := subsFilterRe.String(); got[:5] != "(?i)^" {
		t.Errorf("subsFilterRe missing leading anchor; starts with %q", got[:5])
	}
}

func contains(haystack []language.Language, needle language.Language) bool {
	for _, l := range haystack {
		if l == needle {
			return true
		}
	}
	return false
}
