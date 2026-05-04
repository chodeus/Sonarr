package organizer_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/organizer"
)

func handlers(m map[string]string) map[string]organizer.TokenHandler {
	out := make(map[string]organizer.TokenHandler, len(m))
	for k, v := range m {
		v := v
		out[organizer.NormalizeTokenKey(k)] = func(tm organizer.TokenMatch) string { return v }
	}
	return out
}

func TestReplaceTokens_PassesValueThrough(t *testing.T) {
	t.Parallel()
	got := organizer.ReplaceTokens(
		"{Series Title}",
		handlers(map[string]string{"Series Title": "South Park"}),
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != "South Park" {
		t.Errorf("got %q want %q", got, "South Park")
	}
}

func TestReplaceTokens_AppliesPrefixAndSuffix(t *testing.T) {
	t.Parallel()
	got := organizer.ReplaceTokens(
		"prefix {Series Title} suffix",
		handlers(map[string]string{"Series Title": "Foo"}),
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != "prefix Foo suffix" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceTokens_PrefixSuffixOnTokenChars(t *testing.T) {
	t.Parallel()
	// `{ (Series Title) }` → prefix=` (`, suffix=`) `
	got := organizer.ReplaceTokens(
		"{ (Series Title) }",
		handlers(map[string]string{"Series Title": "Foo"}),
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != " (Foo) " {
		t.Errorf("got %q", got)
	}
}

func TestReplaceTokens_PrefixSuffixDroppedWhenReplacementEmpty(t *testing.T) {
	t.Parallel()
	// Empty replacement → prefix+suffix also dropped (mirrors C# guard
	// at `:902-905`).
	got := organizer.ReplaceTokens(
		"x { (Series Title) } y",
		handlers(map[string]string{"Series Title": ""}),
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != "x  y" {
		t.Errorf("got %q want %q (single space gap from token consumption)", got, "x  y")
	}
}

func TestReplaceTokens_LowerCaseTokenLowersReplacement(t *testing.T) {
	t.Parallel()
	got := organizer.ReplaceTokens(
		"{series title}",
		handlers(map[string]string{"series title": "South Park"}),
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != "south park" {
		t.Errorf("got %q want %q", got, "south park")
	}
}

func TestReplaceTokens_UpperCaseTokenUppersReplacement(t *testing.T) {
	t.Parallel()
	got := organizer.ReplaceTokens(
		"{SERIES TITLE}",
		handlers(map[string]string{"SERIES TITLE": "South Park"}),
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != "SOUTH PARK" {
		t.Errorf("got %q want %q", got, "SOUTH PARK")
	}
}

func TestReplaceTokens_SeparatorReplacesSpacesInValue(t *testing.T) {
	t.Parallel()
	// `{Series.Title}` → separator=`.` → `South Park` becomes `South.Park`
	got := organizer.ReplaceTokens(
		"{Series.Title}",
		handlers(map[string]string{"Series.Title": "South Park"}),
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != "South.Park" {
		t.Errorf("got %q want %q", got, "South.Park")
	}
}

func TestReplaceTokens_EscapedBraces(t *testing.T) {
	t.Parallel()
	got := organizer.ReplaceTokens(
		"{{not a token}}",
		map[string]organizer.TokenHandler{},
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != "{not a token}" {
		t.Errorf("got %q want %q", got, "{not a token}")
	}
}

func TestReplaceTokens_UnknownTokenDropsToEmpty(t *testing.T) {
	t.Parallel()
	got := organizer.ReplaceTokens(
		"{Unknown Token} after",
		map[string]organizer.TokenHandler{},
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != " after" {
		t.Errorf("got %q want %q", got, " after")
	}
}

func TestReplaceTokens_CustomFormatPassedToHandler(t *testing.T) {
	t.Parallel()
	var captured string
	h := map[string]organizer.TokenHandler{
		organizer.NormalizeTokenKey("episode"): func(tm organizer.TokenMatch) string {
			captured = tm.CustomFormat
			return "01"
		},
	}
	got := organizer.ReplaceTokens(
		"{episode:00}",
		h,
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != "01" {
		t.Errorf("got %q", got)
	}
	if captured != "00" {
		t.Errorf("CustomFormat captured = %q want %q", captured, "00")
	}
}

func TestReplaceTokens_TrailingDashTrimmedFromCustomFormat(t *testing.T) {
	t.Parallel()
	var captured string
	h := map[string]organizer.TokenHandler{
		organizer.NormalizeTokenKey("episode"): func(tm organizer.TokenMatch) string {
			captured = tm.CustomFormat
			return ""
		},
	}
	// `{episode:00-}` — the `-` is intended as a suffix in C# (via the
	// lookbehind that prevents `-` from being included in customFormat).
	// The Go port post-trims trailing `-`/space from customFormat to
	// match the C# behaviour.
	organizer.ReplaceTokens("{episode:00-}", h, organizer.DefaultCleanNameOptions(), false)
	if captured != "00" {
		t.Errorf("CustomFormat captured = %q want %q", captured, "00")
	}
}

func TestReplaceTokens_CleanFileNameAppliedToValue(t *testing.T) {
	t.Parallel()
	// Replacement contains illegal `?` → CleanFileName replaces with `!`
	// (with default ReplaceIllegalCharacters=true).
	got := organizer.ReplaceTokens(
		"{Series Title}",
		handlers(map[string]string{"Series Title": "Is this okay?"}),
		organizer.DefaultCleanNameOptions(), false,
	)
	if got != "Is this okay!" {
		t.Errorf("got %q want %q", got, "Is this okay!")
	}
}

func TestReplaceTokens_EscapeDoublesBracesInOutput(t *testing.T) {
	t.Parallel()
	// When escape=true, replacements containing `{` or `}` are doubled
	// up so a downstream ReplaceTokens pass treats them as escaped.
	got := organizer.ReplaceTokens(
		"{Series Title}",
		handlers(map[string]string{"Series Title": "{a}"}),
		organizer.DefaultCleanNameOptions(), true,
	)
	if got != "{{a}}" {
		t.Errorf("got %q want %q", got, "{{a}}")
	}
}

func TestReplaceTokens_EscapeBracesPassThroughIfEscaping(t *testing.T) {
	t.Parallel()
	// `{{` should NOT be unescaped when escape=true.
	got := organizer.ReplaceTokens(
		"{{literal}}",
		map[string]organizer.TokenHandler{},
		organizer.DefaultCleanNameOptions(), true,
	)
	if got != "{{literal}}" {
		t.Errorf("got %q want %q", got, "{{literal}}")
	}
}

func TestFormatNumber(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value  int
		format string
		want   string
	}{
		{5, "00", "05"},
		{5, "0", "5"},
		{42, "00", "42"},
		{42, "0000", "0042"},
		{5, "", "5"},
		{0, "00", "00"},
		{-5, "00", "-05"},
		{5, "garbage", "5"}, // non-zero-padding format → bare decimal
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.format+"_"+tc.want, func(t *testing.T) {
			t.Parallel()
			got := organizer.FormatNumber(tc.value, tc.format)
			if got != tc.want {
				t.Errorf("FormatNumber(%d,%q) = %q want %q", tc.value, tc.format, got, tc.want)
			}
		})
	}
}

func TestTokenMatch_DefaultValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tm   organizer.TokenMatch
		def  string
		want string
	}{
		{organizer.TokenMatch{}, "default", "default"},
		{organizer.TokenMatch{Prefix: " ("}, "default", ""},
		{organizer.TokenMatch{Suffix: ") "}, "default", ""},
		{organizer.TokenMatch{Prefix: " (", Suffix: ") "}, "default", ""},
	}
	for _, tc := range cases {
		got := tc.tm.DefaultValue(tc.def)
		if got != tc.want {
			t.Errorf("DefaultValue(%+v): got %q want %q", tc.tm, got, tc.want)
		}
	}
}
