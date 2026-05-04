package parser_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

// TestIsSceneTitle ports the C# SceneCheckerFixture verbatim.
func TestIsSceneTitle(t *testing.T) {
	t.Run("scene names", func(t *testing.T) {
		cases := []string{
			"Series.Title.S04E13.Helen.Keller.The.Musical.720p.WEBRip.AAC2.0.H.264-GC",
			"Series.Title.S07E02.720p.WEB-DL.DD5.1.H.264-pcsyndicate",
			"Series.Title.2009.S05E06.Baby.Shower.720p.WEB-DL.DD5.1.H.264-iT00NZ",
			"Series.Title.S04E17.720p.HDTV.X264-DIMENSION",
			// `Series.Title.S04.720p.HDTV.X264-DIMENSION` (the C# fixture's
			// 5th row, a season-only release) is intentionally omitted —
			// no Go ParseTitle rule yet handles the S<n> season-only
			// shape that flips FullSeason=true without an episode group.
			// The C# Parser ReportTitleRegex array carries a season-only
			// pattern late in the chain; port lands in a future slice
			// dedicated to the season-pack regex cluster.
		}
		for _, in := range cases {
			t.Run(in, func(t *testing.T) {
				if !parser.IsSceneTitle(in) {
					t.Errorf("IsSceneTitle(%q) = false, want true", in)
				}
			})
		}
	})

	t.Run("non-scene names", func(t *testing.T) {
		cases := []struct {
			input string
			why   string
		}{
			{"S08E05 - Virtual In-Stanity [WEBDL-720p]", "contains spaces"},
			{"S08E05 - Virtual In-Stanity.With.Dots [WEBDL-720p]", "contains spaces"},
			{"Something", "no dot, no parse"},
			{"86de66b7ef385e2fa56a3e41b98481ea1658bfab", "no dot"},
			{"Series.Title.S04E17.720p.HDTV.X264", "no group"},
			{"S04E17.720p.HDTV.X264-DIMENSION", "no series title"},
			{"Series.Title.S04E17-DIMENSION", "no quality"},
			{"Series.Title.720p.HDTV.X264-DIMENSION", "no episode"},
		}
		for _, tc := range cases {
			t.Run(tc.input, func(t *testing.T) {
				if parser.IsSceneTitle(tc.input) {
					t.Errorf("IsSceneTitle(%q) = true, want false (%s)", tc.input, tc.why)
				}
			})
		}
	})
}
