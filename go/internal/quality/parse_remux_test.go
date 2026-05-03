package quality

import "testing"

// TestIsRemux exercises the two-arm RemuxRegex via release names
// drawn verbatim from `should_parse_bluray1080p_remux_quality` and
// `should_parse_bluray2160p_remux_quality` in
// `src/NzbDrone.Core.Test/ParserTests/QualityParserFixture.cs:322-381`.
// The C# fixture exercises remux detection only as part of full
// ParseQuality calls, so these cases extract the rows whose Remux
// classification is the discriminating factor; negatives draw from
// non-remux fixtures + the false-positive rows the C# regex's
// anchored shape was specifically designed to reject (release-group
// tokens like `[TMS-Remux]` and `[RandomRemux]`).
func TestIsRemux(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		// Positives — branch A (prefix-anchored): separator + Remux\b.
		{"REMUX hyphen-prefixed", "Series.Title.S01E08.The.Well.BluRay.1080p.AVC.DTS-HD.MA.5.1.REMUX-FraMeSToR", true},
		{"Remux dot-separated mid-name", "Series.Title.2x11.Nato.Per.La.Truffa.Bluray.Remux.AVC.1080p.AC3.ITA", true},
		{"BD Remux with space separator", "Series!!! on ICE - S01E12[JP BD Remux][ENG subs]", true},
		{"BD 2160p Remux", "Series!!! on ICE - S01E12[JP BD 2160p Remux][ENG subs]", true},
		{"BDRemux concatenated", "Series Title Season 2 (BDRemux 1080p HEVC FLAC) [Netaro]", true},
		{"BDRemux concatenated brackets", "[Vodes] Series Title - Other Title (2020) [BDRemux 1080p HEVC Dual-Audio]", true},
		{"UHD BLURAY REMUX literal", "[Dolby Vision] Sonarr.of.Series.S07.MULTi.UHD.BLURAY.REMUX.DV-NoTag", true},
		{"Hybrid-REMUX literal", "Series Title S01 2018 1080p BluRay Hybrid-REMUX AVC TRUEHD 5.1 Dual Audio-ZR-", true},
		{"Hybrid-REMUX dot-separated", "Series.Title.S01.2018.1080p.BluRay.Hybrid-REMUX.AVC.TRUEHD.5.1.Dual.Audio-ZR-", true},
		{"480i AVC REMUX-Group", "Adventures.of.Sonic.the.Hedgehog.S01.BluRay.480i.DD.2.0.AVC.REMUX-FraMeSToR", true},

		// Positives — branch B (suffix-anchored): Remux + sep + Nnnnp.
		{"Remux 1080p suffix", "1080p Remux", true},
		{"Remux 2160p suffix", "2160p Remux", true},
		{"Remux dot 1080p", "Some.Release.Remux.1080p.Group", true},

		// Positives via the resolution-leading anchor `\d{4}p-`.
		{"1080p- prefix into Remux", "Series.Title.S01E01.1080p-Remux.Group", true},

		// Negatives — false-positive shapes the anchored regex must
		// reject. These all appear in the QualityParserFixture as
		// non-remux releases.
		{"TMS-Remux release-group token", "[TMS-Remux].Series.Title.X.21.720p.[76EA1C53].mkv", false},
		{"RandomRemux release-group token", "[RandomRemux] Series - 01 [720p BD][043EA407].mkv", false},
		{"plain release no remux", "The.Series.S01E01.1080p.HDTV.x264-Grp", false},
		{"empty input", "", false},

		// Branch B requires the trailing 4-digit `p` resolution.
		// Bare `Remux ` without resolution should NOT match branch B
		// (would require branch A's prefix anchor).
		{"Remux trailing without resolution and no leading sep", "Remux nothing else here", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsRemux(tc.in); got != tc.want {
				t.Fatalf("IsRemux(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
