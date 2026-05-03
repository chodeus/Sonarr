package quality

import "testing"

// TestParseSource_FixtureSpotCheck runs ParseSource against rows
// drawn verbatim from QualityParserFixture.cs and asserts the
// expected SourceTag based on which fixture method the row belongs
// to (e.g. should_parse_sdtv_quality rows expect SourceTagSDTV /
// SourceTagPDTV / SourceTagDSR / SourceTagDVD depending on the
// release-name token). This is a parity safety net layered on top of
// TestParseSource — if ParseSource ever drifts from the C# regex
// behaviour on real fixture rows, this test catches it.
func TestParseSource_FixtureSpotCheck(t *testing.T) {
	cases := []struct {
		in   string
		want SourceTag
		from string
	}{
		// should_parse_sdtv_quality rows.
		{"The.Series.S01E13.x264-CtrlSD", SourceTagUnknown, "sdtv (no source token)"},
		{"The.Series.S04E05.HDTV.XviD-LOL", SourceTagHDTV, "sdtv via HDTV"},
		{"Series.S10E27.WS.DSR.XviD-2HD", SourceTagDSR, "sdtv via WS.DSR"},
		{"The.Series.US.S12E17.HR.WS.PDTV.X264-DIMENSION", SourceTagPDTV, "sdtv via PDTV"},
		{"the.Series.1x13.circles.ws.xvidvd-tns", SourceTagDVD, "dvd via xvidvd"},
		{"the_Series.9x18.sunshine_days.ac3.ws_dvdrip_xvid-fov.avi", SourceTagUnknown, "underscore-form pre-normalise"},

		// should_parse_dvd_quality rows.
		{"The.Series.S03E06.DVDRip.XviD-WiDE", SourceTagDVD, "dvd via DVDRip"},
		{"The.Series.S03E06.DVD.Rip.XviD-WiDE", SourceTagDVD, "dvd via DVD."},
		{"The.Series.S01E13.NTSC.x264-CtrlSD", SourceTagDVD, "dvd via NTSC"},

		// should_parse_webdl_* rows.
		{"The.Series.S01E10.The.Leviathan.480p.WEB-DL.x264-mSD", SourceTagWebDL, "webdl via WEB-DL"},
		{"Series.S04E03.The.False.Bride.1080p.NF.WEB.DDP5.1.x264-NTb[rartv]", SourceTagWebDL, "webdl via NF.WEB. + DDP5.1"},
		{"Series.Title.S01E07.A.Prayer.For.Mad.Sweeney.720p.AMZN.WEBRip.DD5.1.x264-NTb", SourceTagWebRip, "webrip via WEBRip"},

		// should_parse_webrip_* rows.
		{"The.Series.S02E10.480p.HULU.WEBRip.x264-Puffin", SourceTagWebRip, "webrip via WEBRip"},
		{"Series.Title.1x04.ITA.WEBMux.x264-NovaRip", SourceTagWebRip, "webrip via WEBMux"},

		// should_parse_bluray_* rows.
		{"SERIES.S03E01-06.DUAL.XviD.Bluray.AC3-REPACK.-HELLYWOOD.avi", SourceTagBluray, "bluray via Bluray"},
		{"SERIES.S03E01-06.DUAL.BDRip.XviD.AC3.-HELLYWOOD", SourceTagBDRip, "bdrip via BDRip"},
		{"Series.Title.S01E08.The.Well.BluRay.1080p.AVC.DTS-HD.MA.5.1.REMUX-FraMeSToR", SourceTagBluray, "bluray remux via BluRay"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.from, func(t *testing.T) {
			t.Parallel()
			if got := ParseSource(c.in); got != c.want {
				t.Fatalf("ParseSource(%q) [%s] = %v, want %v", c.in, c.from, got, c.want)
			}
		})
	}
}
