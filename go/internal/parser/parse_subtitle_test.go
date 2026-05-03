package parser_test

import (
	"reflect"
	"sort"
	"testing"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
)

// Black-box tests for the subtitle entry points.
//
// Cases ported from `src/NzbDrone.Core.Test/ParserTests/LanguageParserFixture.cs`
// — fixture methods `should_parse_subtitle_language_*`,
// `should_parse_title_and_tags`, and `should_not_parse_false_title`.

func TestParseSubtitleLanguage_Unknown(t *testing.T) {
	got := parser.ParseSubtitleLanguage("Series Title - S01E01 - Pilot.sub")
	if got != language.Unknown {
		t.Errorf("ParseSubtitleLanguage no-iso-code = %v, want Unknown", got)
	}
}

func TestParseSubtitleLanguage_English(t *testing.T) {
	cases := []string{
		"Series Title - S01E01 - Pilot.en.sub",
		"Series Title - S01E01 - Pilot.EN.sub",
		"Series Title - S01E01 - Pilot.eng.sub",
		"Series Title - S01E01 - Pilot.ENG.sub",
		"Series Title - S01E01 - Pilot.English.sub",
		"Series Title - S01E01 - Pilot.english.sub",
		"Series Title - S01E01 - Pilot.en.cc.sub",
		"Series Title - S01E01 - Pilot.en.sdh.sub",
		"Series Title - S01E01 - Pilot.en.forced.sub",
		"Series Title - S01E01 - Pilot.en.sdh.forced.sub",
		"Series Title - S01E01 - Pilot.eng.sub",
	}
	for _, fileName := range cases {
		t.Run(fileName, func(t *testing.T) {
			got := parser.ParseSubtitleLanguage(fileName)
			if got != language.English {
				t.Errorf("ParseSubtitleLanguage(%q) = %v, want English", fileName, got)
			}
		})
	}
}

func TestParseLanguageTags(t *testing.T) {
	cases := []struct {
		fileName string
		want     []string
	}{
		{"Series Title - S01E01 - Pilot.en.cc.sub", []string{"cc"}},
		{"Series Title - S01E01 - Pilot.en.sdh.sub", []string{"sdh"}},
		{"Series Title - S01E01 - Pilot.en.forced.sub", []string{"forced"}},
		{"Series Title - S01E01 - Pilot.en.sdh.forced.sub", []string{"sdh", "forced"}},
		{"Series Title - S01E01 - Pilot.en.sub", nil},
	}
	for _, tc := range cases {
		t.Run(tc.fileName, func(t *testing.T) {
			got := parser.ParseLanguageTags(tc.fileName)
			if !equalStringSlices(got, tc.want) {
				t.Errorf("ParseLanguageTags(%q) = %v, want %v", tc.fileName, got, tc.want)
			}
		})
	}
}

// TestParseSubtitleLanguageInformation_TitleAndTags ports
// `should_parse_title_and_tags` from the fixture. Each case asserts
// (LanguageTags, Title, Language).
func TestParseSubtitleLanguageInformation_TitleAndTags(t *testing.T) {
	cases := []struct {
		fileName string
		tags     []string
		title    string
		lang     language.Language
	}{
		{"Name (2020) - S01E20 - [AAC 2.0].testtitle.default.eng.forced.ass", []string{"default", "forced"}, "testtitle", language.English},
		{"Name (2020) - S01E20 - [AAC 2.0].eng.default.testtitle.forced.ass", []string{"default", "forced"}, "testtitle", language.English},
		{"Name (2020) - S01E20 - [AAC 2.0].default.eng.testtitle.forced.ass", []string{"default", "forced"}, "testtitle", language.English},
		{"Name (2020) - S01E20 - [AAC 2.0].testtitle.forced.eng.ass", []string{"forced"}, "testtitle", language.English},
		{"Name (2020) - S01E20 - [AAC 2.0].eng.forced.testtitle.ass", []string{"forced"}, "testtitle", language.English},
		{"Name (2020) - S01E20 - [AAC 2.0].forced.eng.testtitle.ass", []string{"forced"}, "testtitle", language.English},
		{"Name (2020) - S01E20 - [AAC 2.0].testtitle.default.fra.forced.ass", []string{"default", "forced"}, "testtitle", language.French},
		{"Name (2020) - S01E20 - [AAC 2.0].fra.default.testtitle.forced.ass", []string{"default", "forced"}, "testtitle", language.French},
		{"Name (2020) - S01E20 - [AAC 2.0].default.fra.testtitle.forced.ass", []string{"default", "forced"}, "testtitle", language.French},
		{"Name (2020) - S01E20 - [AAC 2.0].testtitle.forced.fra.ass", []string{"forced"}, "testtitle", language.French},
		{"Name (2020) - S01E20 - [AAC 2.0].fra.forced.testtitle.ass", []string{"forced"}, "testtitle", language.French},
		{"Name (2020) - S01E20 - [AAC 2.0].forced.fra.testtitle.ass", []string{"forced"}, "testtitle", language.French},
		{"Name (2020) - S01E20 - [AAC 2.0].ru-something-else.srt", nil, "something-else", language.Russian},
		{"Name (2020) - S01E20 - [AAC 2.0].Full Subtitles.eng.ass", nil, "Full Subtitles", language.English},
		{"Name (2020) - S01E20 - [AAC 2.0].mytitle - 1.en.ass", nil, "mytitle", language.English},
		{"Name (2020) - S01E20 - [AAC 2.0].mytitle 1.en.ass", nil, "mytitle 1", language.English},
		{"Name (2020) - S01E20 - [AAC 2.0].mytitle.en.ass", nil, "mytitle", language.English},
	}

	for _, tc := range cases {
		t.Run(tc.fileName, func(t *testing.T) {
			info := parser.ParseSubtitleLanguageInformation(tc.fileName)
			if info.Language != tc.lang {
				t.Errorf("Language = %v, want %v", info.Language, tc.lang)
			}
			if info.Title != tc.title {
				t.Errorf("Title = %q, want %q", info.Title, tc.title)
			}
			if !equalStringSlices(info.LanguageTags, tc.tags) {
				t.Errorf("LanguageTags = %v, want %v", info.LanguageTags, tc.tags)
			}
		})
	}
}

// TestParseSubtitleLanguageInformation_FalseTitle ports
// `should_not_parse_false_title` — filenames without a real title that
// should fall back to ParseBasicSubtitle (Language=Unknown,
// LanguageTags=nil/empty, RawTitle=empty).
func TestParseSubtitleLanguageInformation_FalseTitle(t *testing.T) {
	cases := []string{
		"Name (2020) - S01E20 - [AAC 2.0].default.forced.ass",
		"Name (2020) - S01E20 - [AAC 2.0].default.ass",
		"Name (2020) - S01E20 - [AAC 2.0].ass",
		"Name (2020) - S01E20 - [AAC 2.0].testtitle.ass",
	}
	for _, fileName := range cases {
		t.Run(fileName, func(t *testing.T) {
			info := parser.ParseSubtitleLanguageInformation(fileName)
			if info.Language != language.Unknown {
				t.Errorf("Language = %v, want Unknown", info.Language)
			}
			if len(info.LanguageTags) != 0 {
				t.Errorf("LanguageTags = %v, want empty", info.LanguageTags)
			}
			if info.RawTitle != "" {
				t.Errorf("RawTitle = %q, want empty", info.RawTitle)
			}
		})
	}
}

func TestUpdateTitleAndCopyFromTitle(t *testing.T) {
	// Direct unit tests for the post-processor — exercises each branch
	// of `LanguageParser.UpdateTitleAndCopyFromTitle` at `:311-328`.
	cases := []struct {
		raw       string
		wantTitle string
		wantCopy  int
	}{
		// `^((?<title>.+) - )?(?<copy>\d{1,3})$` matches with title.
		{"mytitle - 1", "mytitle", 1},
		{"mytitle - 99", "mytitle", 99},
		{"mytitle - 100", "mytitle", 100},
		// title-omitted form: bare 1-3 digits.
		{"1", "", 1},
		{"99", "", 99},
		{"100", "", 100},
		// 4 digits or more — the regex doesn't match; raw becomes title, copy stays 0.
		{"1234", "1234", 0},
		// no digits — same fallback.
		{"mytitle", "mytitle", 0},
		// empty raw.
		{"", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			info := parser.SubtitleTitleInfo{RawTitle: tc.raw}
			parser.UpdateTitleAndCopyFromTitle(&info)
			if info.Title != tc.wantTitle {
				t.Errorf("UpdateTitleAndCopyFromTitle(%q).Title = %q, want %q",
					tc.raw, info.Title, tc.wantTitle)
			}
			if info.Copy != tc.wantCopy {
				t.Errorf("UpdateTitleAndCopyFromTitle(%q).Copy = %d, want %d",
					tc.raw, info.Copy, tc.wantCopy)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	return reflect.DeepEqual(ac, bc)
}
