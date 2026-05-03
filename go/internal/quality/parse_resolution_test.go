package quality_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

// TestParseResolution covers the explicit-resolution test cases
// drawn from src/NzbDrone.Core.Test/ParserTests/QualityParserFixture.cs
// — the relevant `should_parse_*_quality` tests that pin a
// resolution-bearing release name to a known Quality with a known
// pixel height.
func TestParseResolution(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title string
		want  quality.Resolution
	}{
		// 360p sources.
		{"[HorribleSubs] The Series - 03 [360p].mkv", quality.Resolution360p},

		// 480p sources — all four spellings.
		{"[HorribleSubs] The Series - 32 [480p]", quality.Resolution480p},
		{"[CR] The Series - 004 [480p][48CE2D0F]", quality.Resolution480p},
		{"Series.Title.S01E01.848x480", quality.Resolution480p},
		{"The.Series.S01E10.The.Leviathan.480p.WEB-DL.x264-mSD", quality.Resolution480p},

		// 540p sources.
		{"[SubsPlease] Series Title (540p) [AB649D32].mkv", quality.Resolution540p},
		{"[Erai-raws] Series Title [540p][Multiple Subtitle].mkv", quality.Resolution540p},

		// 720p sources — multiple spellings.
		{"Series.Title.S01E01.1280x720", quality.Resolution720p},
		{"The.Series.S04E13.720p.WEB-DL.AAC2.0.H.264-Cyphanix", quality.Resolution720p},

		// 1080p sources.
		{"Series.Title.S01E01.1920x1080", quality.Resolution1080p},
		{"The.Series.S04E13.1080p.WEB-DL.AAC2.0.H.264-Cyphanix", quality.Resolution1080p},
		{"Series.Title.S01E01.1080i", quality.Resolution1080p},
		{"Series.Title.S01E01.FHD.x264-Group", quality.Resolution1080p},
		{"Series.Title.S01E01.1440p.WEB", quality.Resolution1080p},

		// 2160p sources — explicit.
		{"Series.Title.S01E01.2160p.WEB-DL", quality.Resolution2160p},
		{"Series.Title.S01E01.3840x2160.WEB", quality.Resolution2160p},

		// 2160p sources — alternative path (UHD / [4K]).
		{"Series.Title.S01E01.UHD.WEB-DL", quality.Resolution2160p},
		{"Series.Title.S01E01.[4K].WEB-DL", quality.Resolution2160p},

		// Unknown — no resolution token.
		{"Series.Title.S01E01.HDTV.x264", quality.ResolutionUnknown},
		{"plain.title.no.tags.mkv", quality.ResolutionUnknown},
		{"", quality.ResolutionUnknown},
	}
	for _, c := range cases {
		if got := quality.ParseResolution(c.title); got != c.want {
			t.Errorf("ParseResolution(%q) = %d, want %d", c.title, got, c.want)
		}
	}
}

// TestParseResolution_PriorityOrder confirms the C# priority order
// is preserved when multiple resolution tokens appear in the same
// title. The C# code checks groups in order R360p → R2160p, but
// RE2 alternation captures only the first leftmost match — so a
// title containing both "1080p" and "720p" (in that order) returns
// 1080p (whichever appears first wins).
func TestParseResolution_LeftmostMatchWins(t *testing.T) {
	t.Parallel()
	if got := quality.ParseResolution("Series 1080p downscale to 720p"); got != quality.Resolution1080p {
		t.Errorf("leftmost wins: got %d, want 1080p", got)
	}
	if got := quality.ParseResolution("Series 720p before 1080p tag"); got != quality.Resolution720p {
		t.Errorf("leftmost wins: got %d, want 720p", got)
	}
}

// TestParseResolution_4kToDisambiguate verifies the
// 4k-prefix-or-suffix patterns inside the R2160p branch are
// recognised on their own (not via the alternative path).
func TestParseResolution_4kPatterns(t *testing.T) {
	t.Parallel()
	cases := []string{
		"Series.Title.S01E01.4k.UHD.WEB",
		"Series.Title.S01E01.UHD.4k.WEB",
		"Series.Title.S01E01.4k.HEVC.WEB",
		"Series.Title.S01E01.HEVC.4k.WEB",
	}
	for _, c := range cases {
		if got := quality.ParseResolution(c); got != quality.Resolution2160p {
			t.Errorf("ParseResolution(%q) = %d, want 2160p", c, got)
		}
	}
}
