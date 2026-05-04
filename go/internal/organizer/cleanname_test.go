package organizer_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/organizer"
)

// CleanFileName: ports the C# CleanFilenameFixture cases. Each case
// runs CleanFileName(name, NamingConfig.Default) — i.e.
// `ReplaceIllegalCharacters=true, ColonReplacementFormat=Smart`.
func TestCleanFileNameDefault(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		// Path separator characters get the "+" replacement (with
		// ReplaceIllegalCharacters=true, BadCharacters[0/1] both → "+").
		{`C:\foo\bar`, "C-+foo+bar"}, // backslash → +; colon → -
		{"foo/bar", "foo+bar"},
		// `<` / `>` get deleted (GoodCharacters[2/3] are empty).
		{"<tag>", "tag"},
		// `?` → "!"
		{"What is this?", "What is this!"},
		// `*` → "-"
		{"a*b", "a-b"},
		// `|` deleted; `"` deleted.
		{`a|b"c`, "abc"},
		// Smart colon: `: ` → ` - ` then bare `:` → `-`.
		{"South Park: Bigger", "South Park - Bigger"},
		{"South Park:Bigger", "South Park-Bigger"},
		// Trim leading spaces/dots and trailing spaces.
		{"  .Foo bar  ", "Foo bar"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := organizer.CleanFileNameDefault(tc.in)
			if got != tc.want {
				t.Errorf("CleanFileNameDefault(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ColonReplacementFormat variants — ports the C# ColonReplacementFixture.
func TestCleanFileName_ColonReplacement(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		opts        organizer.CleanNameOptions
		input, want string
	}{
		{
			name:  "Delete (ReplaceIllegalCharacters=true)",
			opts:  organizer.CleanNameOptions{ReplaceIllegalCharacters: true, ColonReplacementFormat: organizer.ColonDelete},
			input: "South Park: Bigger",
			want:  "South Park Bigger",
		},
		{
			name:  "Dash",
			opts:  organizer.CleanNameOptions{ReplaceIllegalCharacters: true, ColonReplacementFormat: organizer.ColonDash},
			input: "South Park: Bigger",
			want:  "South Park- Bigger",
		},
		{
			name:  "SpaceDash",
			opts:  organizer.CleanNameOptions{ReplaceIllegalCharacters: true, ColonReplacementFormat: organizer.ColonSpaceDash},
			input: "South Park: Bigger",
			want:  "South Park - Bigger",
		},
		{
			// `:` → ` - ` leaves the trailing space from the original
			// `: ` intact, producing `South Park -  Bigger` (double
			// space after the dash). The fixture-side
			// integration collapses doubles via downstream rules; pure
			// CleanFileName preserves them verbatim.
			name:  "SpaceDashSpace",
			opts:  organizer.CleanNameOptions{ReplaceIllegalCharacters: true, ColonReplacementFormat: organizer.ColonSpaceDashSpace},
			input: "South Park: Bigger",
			want:  "South Park -  Bigger",
		},
		{
			name:  "Smart with space",
			opts:  organizer.CleanNameOptions{ReplaceIllegalCharacters: true, ColonReplacementFormat: organizer.ColonSmart},
			input: "South Park: Bigger",
			want:  "South Park - Bigger",
		},
		{
			name:  "Smart without space",
			opts:  organizer.CleanNameOptions{ReplaceIllegalCharacters: true, ColonReplacementFormat: organizer.ColonSmart},
			input: "South Park:Bigger",
			want:  "South Park-Bigger",
		},
		{
			// Custom format ` -- `: `:` → ` -- `; original space after
			// `:` survives, leaving a double space.
			name:  "Custom (e.g. ` -- `)",
			opts:  organizer.CleanNameOptions{ReplaceIllegalCharacters: true, ColonReplacementFormat: organizer.ColonCustom, CustomColonReplacementFormat: " -- "},
			input: "Foo: Bar",
			want:  "Foo --  Bar",
		},
		{
			name:  "ReplaceIllegalCharacters=false strips colons regardless of format",
			opts:  organizer.CleanNameOptions{ReplaceIllegalCharacters: false, ColonReplacementFormat: organizer.ColonDash},
			input: "South Park: Bigger",
			want:  "South Park Bigger",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := organizer.CleanFileName(tc.input, tc.opts)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ReplaceIllegalCharacters=false → BadCharacters get deleted (no
// substitution).
func TestCleanFileName_ReplaceIllegalCharactersFalse(t *testing.T) {
	t.Parallel()
	opts := organizer.CleanNameOptions{ReplaceIllegalCharacters: false}
	got := organizer.CleanFileName(`a|b"c?d*e<f>g\h/i`, opts)
	if got != "abcdefghi" {
		t.Errorf("got %q want all-stripped", got)
	}
}

// CleanFolderName: collapses runs of separator chars, trims spaces+dots.
// Mirrors the C# FileNameCleanupRegex pass — uses a single instance of
// the run character.
func TestCleanFolderName(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"South Park", "South Park"},
		{"a..b...c", "a.b.c"},
		{"a---b--c", "a-b-c"},
		{"a___b__c", "a_b_c"},
		{"a   b  c", "a b c"},
		{".  trim. .  ", "trim"},
		{".. ..", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := organizer.CleanFolderName(tc.in)
			if got != tc.want {
				t.Errorf("CleanFolderName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
