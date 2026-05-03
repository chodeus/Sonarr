package quality

import "testing"

// TestIsRawHD exercises RawHDRegex via release-name fragments.
// The C# fixture has no RawHD-only test method; the regex feeds the
// `should_parse_raw_quality` rows where RawHD detection IS the
// classification, so the positive cases here mirror that intent.
func TestIsRawHD(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"RawHD plain", true},
		{"Series.RawHD.x264", true},
		{"Series.Raw-HD.x264", true},
		{"Series.Raw_HD.x264", true},
		{"Series.Raw.HD.x264", true},
		{"Series Raw HD x264", true},
		// \b anchors reject substring shapes.
		{"xRawHDx", false},
		{"Series.RawHDStuff.x264", false},
		{"empty", false},
		{"Series.Raw.x264", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := IsRawHD(tc.in); got != tc.want {
				t.Fatalf("IsRawHD(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsMPEG2 covers the MPEG-2 detection that flips HDTV releases
// to RAWHD at `QualityParser.cs:231`. Cases drawn from the
// `should_parse_raw_quality` fixture rows at QualityParserFixture.cs:386-393.
func TestIsMPEG2(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// Verbatim fixture rows.
		{"POI S02E11 1080i HDTV DD5.1 MPEG2-TrollHD", true},
		{"How I Met Your Developer S01E18 Nothing Good Happens After Sonarr 720p HDTV DD5.1 MPEG2-TrollHD", true},
		{"Series.Title.S07E11.1080i.HDTV.DD5.1.MPEG2-NTb.ts", true},
		// Separator variants the `[-_. ]?` allows.
		{"Series.MPEG-2.x264", true},
		{"Series.MPEG_2.x264", true},
		{"Series.MPEG.2.x264", true},
		{"Series MPEG 2 x264", true},
		// Case-SENSITIVE: lowercase rejected.
		{"Series.mpeg2.x264", false},
		{"Series.Mpeg2.x264", false},
		// \b anchors reject substring shapes.
		{"Series.MPEG2X.x264", false},
		{"Series.xMPEG2.x264", false},
		{"empty", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := IsMPEG2(tc.in); got != tc.want {
				t.Fatalf("IsMPEG2(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsHighDefPdtv covers the HR-WS detection that flips
// PDTV/SDTV/DSR/TVRip releases to HDTV720p at `QualityParser.cs:308`.
// Cases drawn from QualityParserFixture.cs:149-150.
func TestIsHighDefPdtv(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// Verbatim fixture rows.
		{"The.Series.US.S12E17.HR.WS.PDTV.X264-DIMENSION", true},
		{"The.Series.The.Lost.Sonarr.Summer.HR.WS.PDTV.x264-DHD", true},
		// Separator variants from the `[-_. ]` class.
		{"Series.hr-ws.PDTV", true},
		{"Series.hr_ws.PDTV", true},
		{"Series hr ws PDTV", true},
		{"Series.HR.WS.PDTV", true},
		// No anchors — substring matches admitted.
		{"prefixhr.wssuffix", true},
		// Negative cases.
		{"Series.HRWS.PDTV", false},
		{"Series.hrws.PDTV", false},
		{"empty", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := IsHighDefPdtv(tc.in); got != tc.want {
				t.Fatalf("IsHighDefPdtv(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsAnimeWebDL covers the [WEB] / [WEB ... / (WEB ... shape that
// flags anime-WebDL releases at `QualityParser.cs:392`. Positive
// cases drawn from `should_parse_webdl*` rows in
// QualityParserFixture.cs:96, 212, 255-260, 286.
func TestIsAnimeWebDL(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// [Web][...] arm.
		{"[HorribleSubs] Series Title! S01 [Web][MKV][h264][480p][AAC 2.0][Softsubs (HorribleSubs)]", true},
		{"[HorribleSubs] Series Title! S01 [Web][MKV][h264][720p][AAC 2.0]", true},
		{"[HorribleSubs] Series Title! S01 [Web][MKV][h264][1080p][AAC 2.0]", true},
		{"[HorribleSubs] Series Title! S01 [Web][MKV][h264][2160p][AAC 2.0]", true},
		// [WEB ... arm.
		{"[LostYears] Series Title - 01-17 (WEB 1080p x264 10-bit AAC) [Dual-Audio]", true},
		{"Series Title Season 2 (WEB 1080p HEVC Opus) [Netaro]", true},
		// [WEB. arm.
		{"[Group] Series Title [WEB.x264].mkv", true},
		// Negative cases — bare WEB (caught by main SourceRegex, not
		// the anime helper).
		{"Series.S01E01.WEB-DL.x264", false},
		{"Series.S01E01.WEBRip.x264", false},
		{"empty", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := IsAnimeWebDL(tc.in); got != tc.want {
				t.Fatalf("IsAnimeWebDL(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsAnimeBluray covers both arms of AnimeBlurayRegex: the
// `bd720`/`bd1080`/`bd2160` no-separator resolution arm AND the
// `[-_. (\[]bd[-_. )\]]` separator-anchored arm. Positive cases
// drawn from `should_parse_bluray*` anime rows in
// QualityParserFixture.cs:311-333.
func TestIsAnimeBluray(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// Arm 1: bd<resolution> no-separator.
		{"[FFF] DATE A Sonarr Dev - 01 [BD][720p-AAC][0601BED4]", true},
		{"[coldhell] Series v3 [BD720p][03192D4C]", true},
		{"[FFF] Series no Muromi-san - 10 [BD][1080p-FLAC][0C4091AF]", true},
		{"[coldhell] Series v2 [BD1080p][5A45EABE].mkv", true},
		{"Series.bd720.x264", true},
		{"Series.bd1080.x264", true},
		{"Series.bd2160.x264", true},
		// Arm 2: bd surrounded by separators (lookbehind/lookahead in C#).
		{"Series.bd.x264", true},
		{"Series-bd-x264", true},
		{"Series_bd_x264", true},
		{"Series bd x264", true},
		{"Series(bd)x264", true},
		{"Series[bd]x264", true},
		// Case-insensitive.
		{"Series.BD.x264", true},
		{"Series.Bd.x264", true},
		// Negative cases — the lookbehind/lookahead semantic rejects
		// these. The Go RE2 rewrite consumes the surrounding chars
		// directly, so the same negatives apply.
		{"Seriesbdx264", false},      // no separators
		{"xbdy", false},              // letters on both sides
		{"Series.bdrip.x264", false}, // bd followed by `r` (not separator)
		// IMPORTANT: the C# regex matches `bd` even when it appears
		// as the start of a larger word like `bdrip`, but ONLY if
		// the `bd` substring itself is bracketed by separators. Here
		// `bdrip` has `.bd` but `r` after — fails.
		{"empty", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := IsAnimeBluray(tc.in); got != tc.want {
				t.Fatalf("IsAnimeBluray(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestClassifyOtherSource covers the HD-TV / SD-TV final-fallback
// branch at `QualityParser.cs:589-595`. Positive cases drawn from
// `should_parse_other_source_quality` rows in
// QualityParserFixture.cs:461-462.
func TestClassifyOtherSource(t *testing.T) {
	tests := []struct {
		in   string
		want SourceTag
	}{
		// Verbatim fixture rows.
		{"Series.Title.S01E01.HD.TV", SourceTagHDTV},
		{"Series.Title.S01E01.SD.TV", SourceTagSDTV},
		// Separator variants from the `[-_. ]` class.
		{"Series.HD-TV.x264", SourceTagHDTV},
		{"Series.HD_TV.x264", SourceTagHDTV},
		{"Series HD TV x264", SourceTagHDTV},
		{"Series.SD-TV.x264", SourceTagSDTV},
		{"Series.SD_TV.x264", SourceTagSDTV},
		// Case-insensitive.
		{"Series.hd-tv.x264", SourceTagHDTV},
		{"Series.sd-tv.x264", SourceTagSDTV},
		// HDTV (no separator) is the main SourceRegex hdtv arm —
		// OtherSourceRegex requires the separator and won't match
		// `HDTV` directly.
		{"Series.HDTV.x264", SourceTagUnknown},
		{"Series.SDTV.x264", SourceTagUnknown},
		// HDTV captured first (leftmost-match-wins on the alternation).
		{"Series.HD-TV.SD-TV.x264", SourceTagHDTV},
		// Empty.
		{"", SourceTagUnknown},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyOtherSource(tc.in); got != tc.want {
				t.Fatalf("ClassifyOtherSource(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
