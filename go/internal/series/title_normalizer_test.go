package series_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

// TestNormalizeTitle_PrecomputedOverride is a verbatim port of the
// C# `should_use_precomputed_title` test at
// `SeriesTitleNormalizerFixture.cs:13-17`.
func TestNormalizeTitle_PrecomputedOverride(t *testing.T) {
	t.Parallel()
	got := series.NormalizeTitle("A to Z", 281588)
	if got != "a to z" {
		t.Errorf("NormalizeTitle(A to Z, 281588) = %q, want \"a to z\"", got)
	}
}

// TestNormalizeTitle_GeneralCases is a verbatim port of the C#
// `should_normalize_title` cases at
// `SeriesTitleNormalizerFixture.cs:23-44`. All cases pass tvdbId=0
// to bypass the precomputed override and exercise the general
// `parser.NormalizeTitle` + lowercasing path.
func TestNormalizeTitle_GeneralCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title string
		want  string
	}{
		{"2 Broke Girls", "2 broke girls"},
		{"Archer (2009)", "archer 2009"},
		{"The Office (US)", "office us"},
		{"The Mentalist", "mentalist"},
		{"The Good Wife", "good wife"},
		{"The Newsroom (2012)", "newsroom 2012"},
		{"Special Agent Oso", "special agent oso"},
		{"A.N.T. Farm", "ant farm"},
		{"A.I.C.O. -Incarnation-", "aico incarnation"},
		{"A.D. The Bible Continues", "ad the bible continues"},
		{"A.P. Bio", "ap bio"},
		{"A-Team", "ateam"},
		{"The A-Team", "ateam"},
		{"And Just Like That", "and just like that"},
	}
	for _, tc := range cases {
		got := series.NormalizeTitle(tc.title, 0)
		if got != tc.want {
			t.Errorf("NormalizeTitle(%q, 0) = %q, want %q", tc.title, got, tc.want)
		}
	}
}
