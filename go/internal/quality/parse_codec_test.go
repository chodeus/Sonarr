package quality

import "testing"

// TestParseCodec exercises the 5-branch CodecRegex via release
// names drawn from the should_parse_*_quality fixtures in
// `src/NzbDrone.Core.Test/ParserTests/QualityParserFixture.cs`.
// The C# fixture exercises codec detection only as part of full
// ParseQuality calls, so these cases are extracted from those rows
// (the [TestCase] strings whose codec is the discriminating factor).
func TestParseCodec(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Codec
	}{
		// x264 — drawn from should_parse_sdtv_quality / should_parse_hdtv_720p / etc.
		{"x264 plain", "The.Series.S01E13.x264-CtrlSD", CodecX264},
		{"x264 uppercase via ignorecase", "Series.S07E01.ARE.YOU.1080P.HDTV.X264-QCF", CodecX264},
		{"x264 trailing tag", "The Series Show S02E08 HDTV x264 FTP", CodecX264},
		{"x264 hyphenated", "The.Series.S01E10.The.Leviathan.480p.WEB-DL.x264-mSD", CodecX264},

		// h264 — drawn from should_parse_webdl720p / should_parse_webdl1080p rows.
		{"h264 lowercase", "Series S04E22 720p WEB-DL DD5.1 H264-EbP.mkv", CodecH264},
		{"h264 with prefix", "into.the.Series.s03e16.h264.720p-web-handbrake.mkv", CodecH264},
		{"h264 anime tag", "[HorribleSubs] Series Title! S01 [Web][MKV][h264][480p][AAC 2.0][Softsubs (HorribleSubs)]", CodecH264},

		// XvidHD — listed before Xvid in the alternation so the longer
		// token wins on leftmost match. No fixture exercises this
		// directly; synthesised to pin the alternation order.
		{"XvidHD prefers xvidhd over xvid branch", "Some.Release.XvidHD.Group", CodecXvidHD},

		// Xvid — drawn from should_parse_sdtv_quality / DVD rows.
		{"Xvid plain", "The.Series.S04E05.HDTV.XviD-LOL", CodecXvid},
		{"Xvid lowercase", "The.Series.S02E15.xvid", CodecXvid},
		{"Xvid in DVD release", "The.Series.S03E06.DVDRip.XviD-WiDE", CodecXvid},

		// divx — drawn from should_parse_sdtv_quality.
		{"divx plain", "The.Series.S02E15.divx", CodecDivX},

		// Negative cases.
		{"empty", "", CodecUnknown},
		{"no codec token", "The.Series.S01E01.HDTV-RlsGrp", CodecUnknown},
		// `Hi10P` is a profile name — it should NOT match h264 since
		// h264 is bordered by \b on both sides and Hi10P doesn't
		// contain the literal substring `h264`.
		{"Hi10P with H264 inside brackets matches h264", "[Eveyuu] Series Title - 10 [Hi10P 1280x720 H264][10B23BD8]", CodecH264},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseCodec(tc.in)
			if got != tc.want {
				t.Fatalf("ParseCodec(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseCodec_AlternationOrder pins the leftmost-match-wins
// behaviour for `Xvid` vs `XvidHD`. RE2 evaluates alternation
// branches in order on each starting position, but BOTH branches
// match at the same anchor; the first listed branch (xvidhd) wins.
// Removing or reordering xvidhd would silently corrupt this case.
func TestParseCodec_AlternationOrder(t *testing.T) {
	if got := ParseCodec("Series.XvidHD-Grp"); got != CodecXvidHD {
		t.Fatalf("XvidHD should map to CodecXvidHD, got %v", got)
	}
	// Bare Xvid (no HD suffix) must still hit CodecXvid.
	if got := ParseCodec("Series.Xvid-Grp"); got != CodecXvid {
		t.Fatalf("Xvid should map to CodecXvid, got %v", got)
	}
}
