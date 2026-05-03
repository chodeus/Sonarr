package parser_test

import (
	"reflect"
	"sort"
	"testing"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
)

// Black-box tests for the public ParseLanguages entry point.
//
// Cases ported from `src/NzbDrone.Core.Test/ParserTests/LanguageParserFixture.cs`.
// Each test mirrors a `should_parse_language_*` fixture method; the
// `result.Should().Contain(Language.X)` assertion becomes
// `containsLang(got, language.X)`. Cases in the C# fixture that go
// through `Parser.Parser.ParseTitle` (rather than `LanguageParser.ParseLanguages`
// directly — fixture lines 447-525) are exercised here against
// ParseLanguages because Parser.ParseTitle delegates to it for the
// language detection step.

func containsLang(haystack []language.Language, needle language.Language) bool {
	for _, l := range haystack {
		if l == needle {
			return true
		}
	}
	return false
}

func TestParseLanguages_FixtureContains(t *testing.T) {
	cases := []struct {
		title string
		wants []language.Language
	}{
		// from `should_parse_language_unknown`. These titles have a
		// language word in the SERIES NAME portion which gets stripped
		// by CleanSeriesTitleRegex, leaving no language token in the
		// post-strip lowercase title. The .Contains arm fires nothing
		// → Unknown.
		{"Spanish Killroy was Here S02E02 Flodden 720p AMZN WEB-DL DDP5 1 H 264-NTb", []language.Language{language.Unknown}},
		{"Title.the.Spanish.Series.S02E02.1080p.WEB.H264-CAKES", []language.Language{language.Unknown}},
		{"Title.the.Italian.Series.S01E01.The.Family.720p.HDTV.x264-FTP", []language.Language{language.Unknown}},
		{"Title.the.Italy.Series.S02E01.720p.HDTV.x264-TLA", []language.Language{language.Unknown}},
		{"Title.the.Series.2009.S01E14.HDTV.XviD-LOL", []language.Language{language.Unknown}},

		// from `should_parse_language_english`
		{"Title.the.Series.2009.S01E14.English.HDTV.XviD-LOL", []language.Language{language.English}},

		// from `should_parse_language_french`
		{"Title.the.Series.2009.S01E14.French.HDTV.XviD-LOL", []language.Language{language.French}},
		{"Title.the.Series.The.1x13.Tueurs.De.Flics.FR.DVDRip.XviD", []language.Language{language.French}},
		{"Title.S01.720p.VF.WEB-DL.AAC2.0.H.264-BTN", []language.Language{language.French}},
		{"Title.S01.720p.TRUEFRENCH.WEB-DL.AAC2.0.H.264-BTN", []language.Language{language.French}},

		// from `should_parse_language_french_english`
		{"Series Title S01 1080p Eng Fra [mkvonly]", []language.Language{language.French, language.English}},
		{"Series Title S01 Eng Fre Multi Subs 720p [H264 mp4]", []language.Language{language.French, language.English}},

		// from `should_parse_language_spanish`
		{"Title.the.Series.2009.S01E14.Spanish.HDTV.XviD-LOL", []language.Language{language.Spanish}},
		{"Series Title - Temporada 2 [HDTV 720p][Cap.206][AC3 5.1 Español Castellano]", []language.Language{language.Spanish}},

		// from `should_parse_language_german`
		{"Title.the.Series.2009.S01E14.German.HDTV.XviD-LOL", []language.Language{language.German}},
		{"Title.the.Series.S04E15.Brotherly.Love.GERMAN.DUBBED.WS.WEBRiP.XviD.REPACK-TVP", []language.Language{language.German}},
		{"The Series Title - S02E16 - Kampfhaehne - mkv - by Videomann", []language.Language{language.German}},
		{"Series.Title.S01E03.Ger.Dub.AAC.1080p.WebDL.x264-TKP21", []language.Language{language.German}},
		{"Series.Title.S01E10.EpisodeName.SwissGerman.WEB-DL.h264-RlsGrp", []language.Language{language.German}},

		// from `should_parse_language_italian`
		{"Title.the.Series.2009.S01E14.Italian.HDTV.XviD-LOL", []language.Language{language.Italian}},
		{"Title.the.Series.1x19.ita.720p.bdmux.x264-novarip", []language.Language{language.Italian}},

		// from `should_parse_language_danish`
		{"Title.the.Series.2009.S01E14.Danish.HDTV.XviD-LOL", []language.Language{language.Danish}},

		// from `should_parse_language_dutch`
		{"Title.the.Series.2009.S01E14.Dutch.HDTV.XviD-LOL", []language.Language{language.Dutch}},

		// from `should_parse_language_japanese`
		{"Title.the.Series.2009.S01E14.Japanese.HDTV.XviD-LOL", []language.Language{language.Japanese}},
		{"[Erai-raws] To Be Series - 14 (JA) [1080p CR WEB-DL AVC AAC][MultiSub]", []language.Language{language.Japanese}},

		// from `should_parse_language_icelandic`
		{"Title.the.Series.2009.S01E14.Icelandic.HDTV.XviD-LOL", []language.Language{language.Icelandic}},

		// from `should_parse_language_chinese`
		{"Title.the.Series.2009.S01E14.Chinese.HDTV.XviD-LOL", []language.Language{language.Chinese}},
		{"Title.the.Series.2009.S01E14.Cantonese.HDTV.XviD-LOL", []language.Language{language.Chinese}},
		{"Title.the.Series.2009.S01E14.Mandarin.HDTV.XviD-LOL", []language.Language{language.Chinese}},
		{"[abc] My Series - 01 [CHS]", []language.Language{language.Chinese}},
		{"[abc] My Series - 01 [BIG5]", []language.Language{language.Chinese}},

		// from `should_parse_language_korean`
		{"Title.the.Series.2009.S01E14.Korean.HDTV.XviD-LOL", []language.Language{language.Korean}},

		// from `should_parse_language_russian`
		{"Title.the.Series.2009.S01E14.Russian.HDTV.XviD-LOL", []language.Language{language.Russian}},

		// from `should_parse_language_polish`
		{"Title.the.Series.2009.S01E14.Polish.HDTV.XviD-LOL", []language.Language{language.Polish}},
		{"Title.the.Series.2009.S01E14.PL.HDTV.XviD-LOL", []language.Language{language.Polish}},
		{"Title.the.Series.2009.S01E14.PLDUB.HDTV.XviD-LOL", []language.Language{language.Polish}},

		// from `should_parse_language_vietnamese`
		{"Title.the.Series.2009.S01E14.Vietnamese.HDTV.XviD-LOL", []language.Language{language.Vietnamese}},

		// from `should_parse_language_swedish`
		{"Title.the.Series.2009.S01E14.Swedish.HDTV.XviD-LOL", []language.Language{language.Swedish}},

		// from `should_parse_language_norwegian` / `_finnish` / `_turkish` / `_portuguese`
		{"Title.the.Series.2009.S01E14.Norwegian.HDTV.XviD-LOL", []language.Language{language.Norwegian}},
		{"Title.the.Series.2009.S01E14.Finnish.HDTV.XviD-LOL", []language.Language{language.Finnish}},
		{"Title.the.Series.2009.S01E14.Turkish.HDTV.XviD-LOL", []language.Language{language.Turkish}},
		{"Title.the.Series.2009.S01E14.Portuguese.HDTV.XviD-LOL", []language.Language{language.Portuguese}},

		// from `should_parse_language_flemish` / `_greek`
		{"Title.the.Series.S01E01.FLEMISH.HDTV.x264-BRiGAND", []language.Language{language.Flemish}},
		{"Title.the.Series.S03E13.Greek.PDTV.XviD-Ouzo", []language.Language{language.Greek}},

		// from `should_parse_language_hungarian` / `_hebrew`
		{"Title.the.Series.2009.S01E14.HDTV.XviD.HUNDUB-LOL", []language.Language{language.Hungarian}},
		{"Title.the.Series.2009.S01E14.HDTV.XviD.HUN-LOL", []language.Language{language.Hungarian}},
		{"Title.the.Series.S01-03.DVDRip.HebDub", []language.Language{language.Hebrew}},

		// from `should_parse_language_lithuanian` (case-sensitive arm)
		{"Title.the.Series.S05E01.WEBRip.x264.AC3.LT.EN-CNN", []language.Language{language.Lithuanian}},

		// from `should_parse_language_arabic` / `_hindi` / `_bulgarian`
		{"Series Title.S01.ARABIC.COMPLETE.720p.NF.WEBRip.x264-PTV", []language.Language{language.Arabic}},
		{"The Shadow Series S01 E01-08 WebRip Dual Audio [Hindi 5.1] 720p x264 AAC ESub", []language.Language{language.Hindi}},
		{"Title.the.Series.2009.S01E14.Bulgarian.HDTV.XviD-LOL", []language.Language{language.Bulgarian}},
		{"Title.the.Series.2009.S01E14.BGAUDIO.HDTV.XviD-LOL", []language.Language{language.Bulgarian}},

		// from `should_parse_language_malayalam`
		{"Series Title S01E01 Malayalam.1080p.WebRip.AVC.5.1-Rjaa", []language.Language{language.Malayalam}},

		// from `should_parse_language_ukrainian`
		{"Гало(Сезон 1, серії 1-5) / SeriesTitle(Season 1, episodes 1-5) (2022) WEBRip-AVC Ukr/Eng",
			[]language.Language{language.Ukrainian, language.English}},

		// from `should_parse_language_slovak`
		{"Title.the.Series.2022.S02E22.Slovak.HDTV.XviD-LOL", []language.Language{language.Slovak}},
		{"Title.the.Series.2021.S01E11.HDTV.XviD.ENG.SK-LOL", []language.Language{language.Slovak, language.English}},

		// from `should_parse_language_thai`
		{"Thai.Series.Title.S01.THAI.1080p.WEBRip.x265-RARBG", []language.Language{language.Thai}},

		// from `should_parse_language_portuguese_brazil`
		{"Title.the.Series.2009.S01E14.Brazilian.HDTV.XviD-LOL", []language.Language{language.PortugueseBrazil}},
		{"Title.the.Series.2009.S01E14.Dublado.HDTV.XviD-LOL", []language.Language{language.PortugueseBrazil}},

		// from `should_parse_language_spanish_latino`
		{"Series.Title.S01.2019.720p_Eng-Spa(Latino)_MovieClubMx", []language.Language{language.SpanishLatino}},
		{"Series Title latino", []language.Language{language.SpanishLatino}},
		{"series title 7x4 audio latino", []language.Language{language.SpanishLatino}},

		// from `should_parse_language_romanian`
		{"TV.Series.S01.720p.WEB-DL.RoDubbed-RLSGRP", []language.Language{language.Romanian}},
		{"TV Series S22E36 ROMANIAN 1080p WEB-DL AAC 2.0 H.264-RLSGRP", []language.Language{language.Romanian}},

		// from `should_parse_language_catalan`
		{"Series S02 (2009–2013)[BDRemux 1080p AVC Cat.DD2.0,Subs][HD-Olimpo][PACK]", []language.Language{language.Catalan}},

		// from `should_parse_hindi_and_english`
		{"The Shadow Series S01 E01-08 WebRip Dual Audio [Hindi 5.1 + English 5.1] 720p x264 AAC ESub",
			[]language.Language{language.Hindi, language.English}},
		{"The Final Sonarr (2020) S04 Complete 720p NF WEBRip [Hindi+English] Dual audio",
			[]language.Language{language.Hindi, language.English}},

		// from `should_parse_spanish_and_catalan`
		{"Series S01 [WEB-DL NF 1080p EAC3 2.0 esp cat Subs][HDOlimpo]",
			[]language.Language{language.Spanish, language.Catalan}},

		// from `should_parse_english_spanish_and_catalan`
		{"Series S02 (2009–2013)[BDRemux 1080p AVC Esp.DD2.0,Ing.DD2.0,Cat.DD2.0,Subs][HD-Olimpo][PACK]",
			[]language.Language{language.English, language.Spanish, language.Catalan}},

		// from `should_parse_language_urdu` / `_romansh` / `_georgian`
		{"Title.the.Series.2009.S01E14.Urdu.HDTV.XviD-LOL", []language.Language{language.Urdu}},
		{"Title.the.Series.2025.S01.Romansh.1080p.WEB-DL.h264-RlsGrp", []language.Language{language.Romansh}},
		{"Title.the.Series.2025.S01.Georgian.1080p.WEB-DL.h264-RlsGrp", []language.Language{language.Georgian}},
		{"Title.the.Series.2025.S01.RU-KA.1080p.WEB-DL.h264-RlsGrp", []language.Language{language.Russian, language.Georgian}},

		// from `should_parse_azerbaijani` / `should_parse_uzbek` (Parser.ParseTitle paths)
		{"series.title.s01e01.2023.[Azerbaijan.Dubbed].1080p.WEB-DLRip.TeeWee", []language.Language{language.Azerbaijani}},
		{"Series s02e04 (2023) [Azerbaijani Dubbed] 1080p WEB-DLRip TeeWee", []language.Language{language.Azerbaijani}},
		{"series.title.s01e01.2023.[Uzbekistan.Dubbed].1080p.WEB-DLRip.TeeWee", []language.Language{language.Uzbek}},
		{"Sweet.Series.S02E08.2023.[Uzbek.Dubbed].1080p.WEB-DLRip.TeeWee", []language.Language{language.Uzbek}},

		// from `should_parse_original_title_from_release_name`
		{"Series.Title.S01E01.Original.1080P.WEB.H264-RlsGrp", []language.Language{language.Original}},
		{"Series.Title.S01E01.Orig.1080P.WEB.H264-RlsGrp", []language.Language{language.Original}},
	}

	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseLanguages(tc.title)
			for _, w := range tc.wants {
				if !containsLang(got, w) {
					t.Errorf("ParseLanguages(%q) = %v, want to contain %v", tc.title, got, w)
				}
			}
		})
	}
}

// TestParseLanguages_GermanDL pins the `should_add_original_language_to_german_release_with_dl_tag`
// fixture: a title with "GERMAN" + "DL" (and no WEB-DL false-positive)
// returns exactly [German, Original].
func TestParseLanguages_GermanDL(t *testing.T) {
	cases := []string{
		"Series.Title.S01E01.German.DL.1080p.BluRay.x264-RlsGrp",
		"Series.Title.S01E01.GERMAN.DL.1080P.WEB.H264-RlsGrp",
		"Series.Title.2023.S01E01.German.DL.EAC3.1080p.DSNP.WEB.H264-RlsGrp",
	}
	for _, title := range cases {
		t.Run(title, func(t *testing.T) {
			got := parser.ParseLanguages(title)
			want := []language.Language{language.German, language.Original}
			if !equalSets(got, want) {
				t.Errorf("ParseLanguages(%q) = %v, want %v", title, got, want)
			}
		})
	}
}

// TestParseLanguages_GermanML pins the `should_add_original_language_and_english_to_german_release_with_ml_tag`
// fixture: a title with "GERMAN" + "ML" returns exactly [German, Original, English].
func TestParseLanguages_GermanML(t *testing.T) {
	title := "Series.Title.2023.S01.German.ML.EAC3.1080p.NF.WEB.H264-RlsGrp"
	got := parser.ParseLanguages(title)
	want := []language.Language{language.German, language.Original, language.English}
	if !equalSets(got, want) {
		t.Errorf("ParseLanguages(%q) = %v, want %v", title, got, want)
	}
}

// TestParseLanguages_GermanWEBDL pins the
// `should_not_add_original_language_to_german_release_when_title_contains_web_dl`
// fixture: GERMAN + WEB-DL must NOT trigger the DL-tag German+Original
// branch. Result: exactly [German].
func TestParseLanguages_GermanWEBDL(t *testing.T) {
	cases := []string{
		"Series.Title.2023.S01E01.GERMAN.1080P.WEB-DL.H264-RlsGrp",
		"Series.Title.2023.S01E01.GERMAN.1080P.WEB.DL.H264-RlsGrp",
		"Series Title 2023 S01E01 GERMAN 1080P WEB DL H264-RlsGrp",
		"Series.Title.2023.S01E01.GERMAN.1080P.WEBDL.H264-RlsGrp",
	}
	for _, title := range cases {
		t.Run(title, func(t *testing.T) {
			got := parser.ParseLanguages(title)
			want := []language.Language{language.German}
			if !equalSets(got, want) {
				t.Errorf("ParseLanguages(%q) = %v, want %v", title, got, want)
			}
		})
	}
}

// TestParseLanguages_DistinctById pins the C# `DistinctBy(l => (int)l)`
// behaviour at `:226`. A title that exercises both the regex arm AND
// the .Contains arm for the same language should still appear once.
func TestParseLanguages_DistinctById(t *testing.T) {
	// "Russian" triggers .Contains("russian"); "Rus" triggers the
	// case-insensitive regex's russian arm. Both add language.Russian
	// — DistinctBy must collapse them.
	got := parser.ParseLanguages("Title.the.Series.S01E01.1080p.WEB-DL.Russian.Rus.Eng.TVKlondike")
	count := 0
	for _, l := range got {
		if l == language.Russian {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ParseLanguages should de-dup Russian, got %d occurrences in %v", count, got)
	}
}

// TestParseLanguages_FixtureMultiPack pins the
// `should_parse_language_english_french_german_italian_portuguese_spanish`
// row.
func TestParseLanguages_FixtureMultiPack(t *testing.T) {
	got := parser.ParseLanguages("The Boys S02 Eng Fre Ger Ita Por Spa 2160p WEBMux HDR10Plus HDR HEVC DDP SGF")
	want := []language.Language{
		language.English, language.French, language.German, language.Italian,
		language.Portuguese, language.Spanish,
	}
	if !equalSets(got, want) {
		t.Errorf("ParseLanguages multi-pack = %v, want %v", got, want)
	}
}

// equalSets compares two slices regardless of order. Used for fixture
// rows that assert `Should().BeEquivalentTo(...)` rather than
// `Should().Contain(...)`.
func equalSets(a, b []language.Language) bool {
	if len(a) != len(b) {
		return false
	}
	ai := make([]int, len(a))
	bi := make([]int, len(b))
	for i, l := range a {
		ai[i] = l.ID
	}
	for i, l := range b {
		bi[i] = l.ID
	}
	sort.Ints(ai)
	sort.Ints(bi)
	return reflect.DeepEqual(ai, bi)
}
