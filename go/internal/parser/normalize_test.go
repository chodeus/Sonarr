package parser_test

import (
	"fmt"
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

// TestCleanSeriesTitle ports
// src/NzbDrone.Core.Test/ParserTests/NormalizeSeriesTitleFixture.cs
// as a table-driven Go test. Per the strategic plan, the C# test
// corpus IS the spec for the parser — every (input, expected) pair
// must round-trip identically.
func TestCleanSeriesTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		// should_normalize_series_title
		{"Series", "series"},
		{"Series (2009)", "series2009"},
		{"Series.2010", "series2010"},
		{"Series_and_Title_Sonarr", "seriestitlesonarr"},

		// should_remove_special_characters_and_casing
		{"CaPitAl", "capital"},
		{"peri.od", "period"},
		{"this.^&%^**$%@#$!That", "thisthat"},
		{"test/test", "testtest"},
		{"90210", "90210"},
		{"24", "24"},
		{"Test: Something à Deux", "testsomethingdeux"},
		{"Parler à", "parlera"},
		{"Ríkið", "rikid"},

		// should_not_remove_from_the_beginning_of_the_title
		{"The Series", "theseries"},
		{"The Series Show With Sonarr Dev", "theseriesshowwithsonarrdev"},
		{"The.Series.Show", "theseriesshow"},

		// should_not_clean_trailing_a
		{"Series Title A", "seriestitlea"},

		// should_replace_percent_sign_with_percent_following_numbers
		{"3%", "3percent"},
		{"Series Top & 100% Coding Developers", "seriestop100percentcodingdevelopers"},
		{"Series Title What's Your F@%king Deal?!", "seriestitlewhatsyourfkingdeal"},
	}
	for _, c := range cases {
		if got := parser.CleanSeriesTitle(c.in); got != c.want {
			t.Errorf("CleanSeriesTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCleanSeriesTitle_StripsCommonWordsFromMiddle ports
// `should_remove_common_words_from_middle_of_title` and
// `should_remove_a_from_middle_of_title` from
// NormalizeSeriesTitleFixture.cs.
func TestCleanSeriesTitle_StripsCommonWordsFromMiddle(t *testing.T) {
	t.Parallel()
	formats := []string{"word.%s.word", "word %s word", "word-%s-word"}
	for _, word := range []string{"the", "and", "or", "an", "of", "a"} {
		for _, f := range formats {
			in := fmt.Sprintf(f, word)
			if got := parser.CleanSeriesTitle(in); got != "wordword" {
				t.Errorf("CleanSeriesTitle(%q) = %q, want %q", in, got, "wordword")
			}
		}
	}
}

// TestCleanSeriesTitle_KeepsCommonWordsAtEdges ports
// `should_not_remove_common_words_from_end_of_title`,
// `should_not_clean_word_from_beginning_of_string`, and
// `should_not_remove_common_words_in_the_middle_of_word`.
func TestCleanSeriesTitle_KeepsCommonWordsAtEdges(t *testing.T) {
	t.Parallel()
	for _, word := range []string{"the", "and", "or", "an", "of"} {
		// At end of title — preserved.
		ends := []string{"word.word.%s", "word-word-%s", "word-word %s"}
		for _, f := range ends {
			in := fmt.Sprintf(f, word)
			want := "wordword" + word
			if got := parser.CleanSeriesTitle(in); got != want {
				t.Errorf("CleanSeriesTitle(%q) [end] = %q, want %q", in, got, want)
			}
		}
		// At start of title — preserved.
		starts := []string{"%s.word.word", "%s-word-word", "%s word word"}
		for _, f := range starts {
			in := fmt.Sprintf(f, word)
			want := word + "wordword"
			if got := parser.CleanSeriesTitle(in); got != want {
				t.Errorf("CleanSeriesTitle(%q) [start] = %q, want %q", in, got, want)
			}
		}
	}
	// And the same for "a" at start/end.
	for _, c := range []struct{ in, want string }{
		{"a.word.word", "awordword"},
		{"word.word.a", "wordworda"},
	} {
		if got := parser.CleanSeriesTitle(c.in); got != c.want {
			t.Errorf("CleanSeriesTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Common word as a substring inside a longer word — preserved
	// (the boundary check requires \W|_ on both sides).
	mids := []string{
		"word.%sword", "word %sword", "word-%sword",
		"word%s.word", "word%s-word",
	}
	for _, word := range []string{"the", "and", "or", "a", "an", "of"} {
		for _, f := range mids {
			in := fmt.Sprintf(f, word)
			want := "word" + word + "word"
			if got := parser.CleanSeriesTitle(in); got != want {
				t.Errorf("CleanSeriesTitle(%q) [substr] = %q, want %q", in, got, want)
			}
		}
	}
}

// TestNormalizeEpisodeTitle ports
// src/NzbDrone.Core.Test/ParserTests/NormalizeEpisodeTitleFixture.cs.
func TestNormalizeEpisodeTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"Episode Title", "episode title"},
		{"A.B,C;", "a b c"},
		{"Episode  Title", "episode title"},
		{"French Title (1)", "french title"},
		{"Series.Title.S01.Special.Episode.Title.720p.HDTV.x264-Sonarr", "episode title"},
		{"Series.Title.S01E00.Episode.Title.720p.HDTV.x264-Sonarr", "episode title"},
	}
	for _, c := range cases {
		if got := parser.NormalizeEpisodeTitle(c.in); got != c.want {
			t.Errorf("NormalizeEpisodeTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNormalizeTitle covers the loose-title path, which has no
// dedicated C# fixture — sanity assertions match what the C#
// implementation produces for representative inputs.
func TestNormalizeTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"The Big Show", "big show"},
		{"A Series", "series"},
		{"An Other Title", "other title"},
		{"Hello, World!", "hello world"},
		{"Multiple   Spaces", "multiple spaces"},
		{"  trim me  ", "trim me"},
		{"", ""},
	}
	for _, c := range cases {
		if got := parser.NormalizeTitle(c.in); got != c.want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRemoveDiacritics covers the helper directly. Most of its
// behaviour is exercised through CleanSeriesTitle, but the lookup
// table for non-NFKD-decomposing characters deserves explicit
// coverage so a future contributor doesn't trim the table.
func TestRemoveDiacritics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"plain ASCII", "plain ASCII"},
		{"café", "cafe"},
		{"Ríkið", "Rikid"},
		{"naïve", "naive"},
		{"Mötley Crüe", "Motley Crue"},
		{"Æon", "AEon"},
		{"Þor", "Thor"},
		{"strasse ß ok", "strasse ss ok"},
		{"łódź", "lodz"},
		{"Ø vs O", "O vs O"},
		{"œuvre", "oeuvre"},
	}
	for _, c := range cases {
		if got := parser.RemoveDiacritics(c.in); got != c.want {
			t.Errorf("RemoveDiacritics(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
