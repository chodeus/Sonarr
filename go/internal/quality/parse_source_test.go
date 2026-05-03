package quality

import "testing"

// TestParseSource exercises the 11-branch SourceRegex via release
// names drawn from the should_parse_*_quality fixtures in
// `src/NzbDrone.Core.Test/ParserTests/QualityParserFixture.cs`.
// The C# fixture exercises source detection only as part of full
// ParseQuality calls; cases here extract the rows whose source-tag
// classification is the discriminating factor. Coverage targets
// each named group in turn plus the two RE2 lookaround rewrites
// (BD-at-end filter, AMZN/NF/DP-WEB-Rip filter).
func TestParseSource(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want SourceTag
	}{
		// bluray group — main alternation arms.
		{"BluRay", "Series.Title.S01E08.The.Well.BluRay.1080p.x264-Grp", SourceTagBluray},
		{"Blu-Ray hyphenated", "Series.Title.S01E08.Blu-Ray.1080p.x264-Grp", SourceTagBluray},
		{"HDDVD", "Series.Title.S01E01.HDDVD.x264", SourceTagBluray},
		{"HD-DVD", "Series.Title.S01E01.HD-DVD.x264", SourceTagBluray},
		{"BDMux", "Series.Title.1x04.ITA.BDMux.x264-NovaRip", SourceTagBluray},
		{"bare BD with follow", "Series.S01E12 BD Remux ENG", SourceTagBluray},

		// blurayBD post-filter: BD at end MUST NOT match (the
		// `BD(?!$)` semantic). Without the post-filter, this would
		// resolve to SourceTagBluray.
		{"BD at end of string -> filtered", "Series.S01E01.BD", SourceTagUnknown},
		{"BD followed by space at end - boundary admits", "Series.S01E01.BD ", SourceTagBluray},

		// webdl group — main arms.
		{"WEB-DL", "Series.Title.S01E08.1080p.WEB-DL.DD5.1.H264", SourceTagWebDL},
		{"WEBDL plain", "Series.Title.S01E08.WEBDL.1080p", SourceTagWebDL},
		{"WEB.DL.mux suffix", "Series.Title.S01E08.WEB.DLmux.1080p", SourceTagWebDL},
		{"AmazonHD", "Series.S01E08.AmazonHD.x264-TVS", SourceTagWebDL},
		{"NetflixUHD", "Series.S01E08.German.DL.NetflixUHD.x264", SourceTagWebDL},
		{"NetflixHD", "Series 2016 German DL NetflixHD x264-TVS", SourceTagWebDL},
		{"WEB.x264 mid-name", "Series.Title.2016.03.14.1080p.WEB.x264-spamTV", SourceTagWebDL},
		{"WEB.h264 mid-name", "Series.Title.2016.03.14.1080p.WEB.h264-spamTV", SourceTagWebDL},
		{"WEB.HEVC", "Series.Title.S01E01.1080p.WEB.HEVC.x265", SourceTagWebDL},
		{"WEB.DDP5.1", "Series.S01E01.1080p.WEB.DDP5.1.x264-Grp", SourceTagWebDL},
		{"720p-WEB- prefix", "Series.S01E01.720p-WEB-DLRip-Grp", SourceTagWebDL},
		{"-WEB-1080p suffix", "Series.S01E01-WEB-1080p-Grp", SourceTagWebDL},

		// webdl case-sensitive `(?-i:WEB)$` arm — only matches
		// uppercase `WEB` at end. The C# `[. ](?-i:WEB)$` requires a
		// preceding `.` or space.
		{".WEB at end uppercase", "Series.Title.S01E01.1080p.WEB", SourceTagWebDL},
		{".web at end lowercase NOT matched by (?-i:WEB)$", "Series.Title.S01E01.1080p.web", SourceTagUnknown},

		// webdlamzn group — AMZN/NF/DP arm.
		{"AMZN.WEB-", "Series.S01E07.A.Prayer.For.Mad.Sweeney.720p.AMZN.WEB-DD5.1.x264", SourceTagWebDL},
		{"NF.WEB.", "Series.S04E03.The.False.Bride.1080p.NF.WEB.DDP5.1.x264-NTb", SourceTagWebDL},
		{"DP-WEB-", "Series.S01E01.1080p.DP-WEB-h264-Grp", SourceTagWebDL},

		// webdlamzn post-filter: AMZN/NF/DP-WEB followed by Rip MUST
		// NOT match. Without the post-filter, the AMZN/NF/DP-WEB-
		// arm would admit `AMZN.WEB.Rip-` as webdl. Filtered out,
		// no other arm matches `WEB.Rip` (the webrip group only
		// covers `WebRip` / `Web-Rip` / `WEBMux` literals — no
		// period-separated `WEB.Rip` form). C# returns Unknown for
		// these inputs by the same logic.
		{"AMZN.WEB.Rip -> filtered, no other source", "Series.S01E01.AMZN.WEB.Rip-Grp", SourceTagUnknown},
		{"NF.WEB.Rip -> filtered, no other source", "Series.S01E01.NF.WEB.Rip.x264", SourceTagUnknown},

		// webrip group.
		{"WebRip", "Series.Title.S04E01.720p.WebRip.AAC2.0.x264", SourceTagWebRip},
		// `Web-Rip` arm — note the C# fixture has no Web-Rip case
		// because the resolution-WEB-resolution arm of webdl
		// (`(?:720|1080|2160)p[-. ]WEB[-. ]`) would consume
		// `720p.Web-` first when both appear in the same name. To
		// exercise the bare webrip arm, the input must NOT have a
		// preceding NNNNp. separator.
		{"Web-Rip hyphenated, no resolution prefix", "Series.Title.S04E01.Web-Rip.AAC2.0.x264", SourceTagWebRip},
		{"WEBMux", "Series.Title.1x04.ITA.WEBMux.x264-NovaRip", SourceTagWebRip},

		// hdtv group.
		{"HDTV plain", "The.Series.S08E05.720p.HDTV.X264-DIMENSION", SourceTagHDTV},

		// bdrip group.
		{"BDRip", "Series.S03E06.DVDRip.BDRip.XviD-WiDE", SourceTagBDRip},
		{"BDLight", "Series.S01E01.BDLight.x264-Grp", SourceTagBDRip},

		// brrip group.
		{"BRRip", "Series.S01E01.BRRip.XviD-Grp", SourceTagBRRip},

		// dvd group.
		{"DVD plain", "Series.S03E06.DVD.Rip.XviD-WiDE", SourceTagDVD},
		{"DVDRip combined", "Series.S03E06.DVDRip.XviD-WiDE", SourceTagDVD},
		{"NTSC", "Series.S01E13.NTSC.x264-CtrlSD", SourceTagDVD},
		{"PAL", "Series.S01E13.PAL.x264-CtrlSD", SourceTagDVD},
		{"xvidvd", "the.Series.1x13.circles.ws.xvidvd-tns", SourceTagDVD},

		// dsr group.
		{"DSR plain", "Series.S01E04.DSR.x264.2HD", SourceTagDSR},
		{"WS-DSR with separator", "Series.S10E27.WS-DSR.XviD-2HD", SourceTagDSR},

		// pdtv group.
		{"PDTV", "Series.2011.S02E01.WS.PDTV.x264-TLA", SourceTagPDTV},

		// sdtv group.
		{"SDTV", "Series.S01E01.SDTV.XviD-Grp", SourceTagSDTV},

		// tvrip group.
		{"TVRip", "Series.S03.TVRip.XviD-NOGRP", SourceTagTVRip},

		// LastOrDefault semantic: rightmost source wins when multiple
		// tokens are present. WebRip late + Bluray early -> WebRip.
		{"multiple sources, last wins (BluRay then HDTV)", "Series.S01E01.BluRay.x264.HDTV", SourceTagHDTV},

		// Negative cases.
		{"empty input", "", SourceTagUnknown},
		{"no source token", "Series.S01E01.Random.x264-Grp", SourceTagUnknown},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseSource(tc.in); got != tc.want {
				t.Fatalf("ParseSource(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseSource_CaseInsensitiveWEB pins the `(?-i:WEB)$` inline
// flag-negation behaviour: the bare WEB-at-end branch is
// case-SENSITIVE while the rest of the regex is case-insensitive.
// Both `WEB` and `WEBDL` should match (different arms); `web` at end
// must not match the case-sensitive WEB$ branch — but the
// case-insensitive `WEBDL` arm is unaffected, so `Series.webdl` still
// matches.
func TestParseSource_CaseInsensitiveWEB(t *testing.T) {
	cases := []struct {
		in   string
		want SourceTag
	}{
		// Uppercase WEB at end via `[. ](?-i:WEB)$` arm.
		{"Series.Title.1080p.WEB", SourceTagWebDL},
		// Lowercase web at end - the `(?-i:WEB)$` arm REJECTS this,
		// and no other arm matches a bare lowercase `web` token.
		{"Series.Title.1080p.web", SourceTagUnknown},
		// Mixed case `Web` at end - same: `(?-i:WEB)$` rejects.
		{"Series.Title.1080p.Web", SourceTagUnknown},
		// `WEBDL` at end matches the case-INSENSITIVE main webdl arm.
		{"Series.Title.1080p.webdl", SourceTagWebDL},
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got := ParseSource(c.in); got != c.want {
				t.Fatalf("ParseSource(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
