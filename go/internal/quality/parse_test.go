package quality

import "testing"

// TestParseQuality_FixtureRows ports a representative subset of the
// `should_parse_X_quality` table-driven cases from
// `src/NzbDrone.Core.Test/ParserTests/QualityParserFixture.cs`. The
// full fixture is 1,585 [TestCase] rows; this test ports ~3-5 per
// fixture method to validate the major decision-matrix branches end-
// to-end. Subsequent commits can pad each section with the remaining
// rows as drift-prevention.
//
// Each row cites the QualityParserFixture line and the originating
// fixture method via the `from` field. Failures should reproduce
// against the C# tree by running:
//
//	dotnet test --filter "QualityParserFixture.<method>"
func TestParseQuality_FixtureRows(t *testing.T) {
	tests := []struct {
		in       string
		want     Quality
		isRepack bool
		version  int
		from     string
	}{
		// should_parse_sdtv_quality (lines 50-72).
		{"S07E23 .avi ", SDTV, false, 1, "sdtv: avi extension"},
		{"The Series S02E01 HDTV XviD 2HD", SDTV, false, 1, "sdtv: HDTV+XviD"},
		{"The Series S05E11 PROPER HDTV XviD 2HD", SDTV, false, 2, "sdtv: PROPER bumps version"},
		{"The Series Show S02E08 HDTV x264 FTP", SDTV, false, 1, "sdtv: HDTV+x264"},
		{"The Series S01E04 DSR x264 2HD", SDTV, false, 1, "sdtv: DSR+x264"},
		{"The Series S01E04 Series Death Train DSR x264 MiNDTHEGAP", SDTV, false, 1, "sdtv: DSR+x264"},
		{"The.Series.S03E06.HDTV-WiDE", SDTV, false, 1, "sdtv: bare HDTV no resolution"},
		{"[HorribleSubs] The Series - 32 [480p]", SDTV, false, 1, "sdtv: 480p anime no source"},
		{"The.Series.S03.TVRip.XviD-NOGRP", SDTV, false, 1, "sdtv: TVRip"},

		// should_parse_dvd_quality (lines 78-83).
		{"The.Series.S01E13.NTSC.x264-CtrlSD", DVD, false, 1, "dvd: NTSC"},
		{"The.Series.S03E06.DVDRip.XviD-WiDE", DVD, false, 1, "dvd: DVDRip"},
		{"The.Series.S03E06.DVD.Rip.XviD-WiDE", DVD, false, 1, "dvd: DVD."},
		{"the.Series.1x13.circles.ws.xvidvd-tns", DVD, false, 1, "dvd: xvidvd"},

		// should_parse_webdl480p_quality (lines 90-97).
		{"The.Series.S01E10.The.Leviathan.480p.WEB-DL.x264-mSD", WEBDL480p, false, 1, "webdl480p: 480p WEB-DL"},
		{"The.Series.S01E08.Das.geloeschte.Ich.German.Dubbed.DL.AmazonHD.x264-TVS", WEBDL480p, false, 1, "webdl480p: AmazonHD no resolution"},
		{"The.Series.S01E04.Rod.Trip.mit.meinem.Onkel.German.DL.NetflixUHD.x264", WEBDL480p, false, 1, "webdl480p: NetflixUHD no resolution"},
		{"[HorribleSubs] Series Title! S01 [Web][MKV][h264][480p][AAC 2.0][Softsubs (HorribleSubs)]", WEBDL480p, false, 1, "webdl480p: anime [Web]+480p"},
		{"Series.Title.S13E11.Ausgebacken.German.AmazonSD.h264-4SF", WEBDL480p, false, 1, "webdl480p: AmazonSD"},

		// should_parse_bluray480p_quality (lines 103-113).
		{"SERIES.S03E01-06.DUAL.BDRip.XviD.AC3.-HELLYWOOD", Bluray480p, false, 1, "bluray480p: BDRip+XviD"},
		{"The.Series.S01E05.480p.BluRay.DD5.1.x264-HiSD", Bluray480p, false, 1, "bluray480p: 480p BluRay"},
		{"[Doki] Series - 02 (848x480 XviD BD MP3) [95360783]", Bluray480p, false, 1, "bluray480p: anime BD+848x480"},

		// should_parse_webrip480p_quality (lines 119-121).
		{"The.Series.S02E10.480p.HULU.WEBRip.x264-Puffin", WEBRip480p, false, 1, "webrip480p"},
		{"Series.Title.1x04.ITA.WEBMux.x264-NovaRip", WEBRip480p, false, 1, "webrip480p: WEBMux no resolution"},

		// should_parse_bluray576p_quality (line 127).
		{"The.Series.S01E05.576p.BluRay.DD5.1.x264-HiSD", Bluray576p, false, 1, "bluray576p"},

		// should_parse_hdtv720p_quality (lines 134-154).
		{"The Series S04E87 REPACK 720p HDTV x264 aAF", HDTV720p, true, 2, "hdtv720p: REPACK bumps version + sets repack"},
		{"The.Series.S08E05.720p.HDTV.X264-DIMENSION", HDTV720p, false, 1, "hdtv720p"},
		{"The.Series.S01E08.Tourmaline.Nepal.720p.HDTV.x264-DHD", HDTV720p, false, 1, "hdtv720p"},
		{"The.Series.US.S12E17.HR.WS.PDTV.X264-DIMENSION", HDTV720p, false, 1, "hdtv720p: HR-WS PDTV upgrade"},
		{"The.Series.S05E06.720p.HDTV.x264-FHD", HDTV720p, false, 1, "hdtv720p"},

		// should_parse_hdtv1080p_quality (lines 161-175).
		{"Series.S07E01.ARE.YOU.1080P.HDTV.X264-QCF", HDTV1080p, false, 1, "hdtv1080p"},
		{"Series.S07E01.ARE.YOU.1080P.HDTV.x264-QCF", HDTV1080p, false, 1, "hdtv1080p case"},
		{"Series.S07E01.ARE.YOU.1080P.HDTV.proper.X264-QCF", HDTV1080p, false, 2, "hdtv1080p: proper bumps version"},

		// should_parse_hdtv2160p_quality (lines 182-184).
		{"Series.Title.S01E05.2160p.HDTV.x264-1HD", HDTV2160p, false, 1, "hdtv2160p"},

		// should_parse_webdl720p_quality (lines 196-217).
		{"Series S04E22 720p WEB-DL DD5.1 H264-EbP.mkv", WEBDL720p, false, 1, "webdl720p: 720p WEB-DL"},
		{"Series.S04.720p.Web-Dl.Dd5.1.h264-P2PACK", WEBDL720p, false, 1, "webdl720p: Web-Dl"},
		{"Series.S06E23.720p.WebHD.h264-euHD", WEBDL720p, false, 1, "webdl720p: WebHD"},
		{"Series.Title.2016.03.14.720p.WEB.x264-spamTV", WEBDL720p, false, 1, "webdl720p: 720p WEB.x264"},
		{"into.the.Series.s03e16.h264.720p-web-handbrake.mkv", WEBDL720p, false, 1, "webdl720p: 720p-web-"},

		// should_parse_webrip720p_quality (lines 223-226).
		{"Series.Title.S04E01.720p.WEBRip.AAC2.0.x264-NFRiP", WEBRip720p, false, 1, "webrip720p"},
		{"Series.Title.S01E07.A.Prayer.For.Mad.Sweeney.720p.AMZN.WEBRip.DD5.1.x264-NTb", WEBRip720p, false, 1, "webrip720p: AMZN.WEBRip"},

		// should_parse_webdl1080p_quality (lines 232-263).
		{"Series S09E03 1080p WEB DL DD5 1 H264 NFHD", WEBDL1080p, false, 1, "webdl1080p: spaces"},
		{"Series.S08E01.1080p.WEB-DL.DD5.1.H264-NFHD", WEBDL1080p, false, 1, "webdl1080p"},
		{"Series.Title.2016.03.14.1080p.WEB.x264-spamTV", WEBDL1080p, false, 1, "webdl1080p: 1080p WEB.x264"},
		{"Series.S04E03.The.False.Bride.1080p.NF.WEB.DDP5.1.x264-NTb[rartv]", WEBDL1080p, false, 1, "webdl1080p: NF.WEB+DDP5.1"},

		// should_parse_webrip1080p_quality (lines 272-275).
		{"Series.Title.S01E03.1080p.AMZN.WEBRip.DD5.1.x264-NTb", WEBRip1080p, false, 1, "webrip1080p"},

		// should_parse_webdl2160p_quality (lines 281-290).
		{"Series.S01E03.2160p.WEB-DL.DDP5.1.HDR.HEVC-AJP69", WEBDL2160p, false, 1, "webdl2160p"},
		{"[HorribleSubs] Series Title! S01 [Web][MKV][h264][2160p][AAC 2.0][Softsubs (HorribleSubs)]", WEBDL2160p, false, 1, "webdl2160p: anime [Web]+2160p"},

		// should_parse_webrip2160p_quality (line 301).
		{"Series.Title.S01E01.2160p.WEBRip.AAC2.0.x264-NTb", WEBRip2160p, false, 1, "webrip2160p"},

		// should_parse_bluray720p_quality (lines 308-323).
		{"[FFF] DATE A Sonarr Dev - 01 [BD][720p-AAC][0601BED4]", Bluray720p, false, 1, "bluray720p: anime [BD][720p]"},
		{"[coldhell] Series v3 [BD720p][03192D4C]", Bluray720p, false, 1, "bluray720p: anime [BD720p]"},
		{"Series.S01E12.720p.BluRay.x264-DEMAND", Bluray720p, false, 1, "bluray720p"},

		// should_parse_bluray1080p_quality (lines 332-342).
		{"[FFF] Series no Muromi-san - 10 [BD][1080p-FLAC][0C4091AF]", Bluray1080p, false, 1, "bluray1080p: anime [BD][1080p]"},
		{"[coldhell] Series v2 [BD1080p][5A45EABE].mkv", Bluray1080p, false, 1, "bluray1080p: anime [BD1080p]"},
		{"Series.S01E07.1080p.BluRay.x264-DEMAND", Bluray1080p, false, 1, "bluray1080p"},

		// should_parse_bluray1080p_remux_quality (lines 348-357).
		{"Series.Title.S01E08.The.Well.BluRay.1080p.AVC.DTS-HD.MA.5.1.REMUX-FraMeSToR", Bluray1080pRemux, false, 1, "bluray1080p remux"},
		{"Series Title S01 2018 1080p BluRay Hybrid-REMUX AVC TRUEHD 5.1 Dual Audio-ZR-", Bluray1080pRemux, false, 1, "bluray1080p Hybrid-REMUX"},

		// should_parse_bluray2160p_quality (lines 366-368).
		{"Series.Title.S01E01.2160p.BluRay.x265-Grp", Bluray2160p, false, 1, "bluray2160p"},

		// should_parse_bluray2160p_remux_quality (lines 374-380).
		{"Series.Title.S01E08.The.Sonarr.BluRay.2160p.AVC.DTS-HD.MA.5.1.REMUX-FraMeSToR", Bluray2160pRemux, false, 1, "bluray2160p remux"},

		// should_parse_raw_quality (lines 386-394).
		{"POI S02E11 1080i HDTV DD5.1 MPEG2-TrollHD", RawHD, false, 1, "raw: HDTV+MPEG2"},
		{"Series.Title.S07E11.1080i.HDTV.DD5.1.MPEG2-NTb.ts", RawHD, false, 1, "raw: HDTV+MPEG2 .ts"},

		// quality_parse (lines 400-407).
		{"The.Series.S02E15", Unknown, false, 1, "unknown: bare title"},
		{"Series.Title - 11x11 - Title", Unknown, false, 1, "unknown: bare title"},
		{"Series.Title.S01E01.webm", Unknown, false, 1, "unknown: .webm extension explicitly mapped to Unknown"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.from, func(t *testing.T) {
			t.Parallel()
			got := ParseQuality(tc.in)
			if got.Quality.ID != tc.want.ID {
				t.Errorf("ParseQuality(%q).Quality = %v, want %v", tc.in, got.Quality, tc.want)
			}
			if got.Revision.Version != tc.version {
				t.Errorf("ParseQuality(%q).Revision.Version = %d, want %d", tc.in, got.Revision.Version, tc.version)
			}
			if got.Revision.IsRepack != tc.isRepack {
				t.Errorf("ParseQuality(%q).Revision.IsRepack = %v, want %v", tc.in, got.Revision.IsRepack, tc.isRepack)
			}
		})
	}
}

// TestParseQuality_DetectionSources ports the should_parse_*_from_*
// fixture methods (lines 430-491) which assert SourceDetectionSource
// + ResolutionDetectionSource — orthogonal to which Quality was
// resolved.
func TestParseQuality_DetectionSources(t *testing.T) {
	type want struct {
		source     DetectionSource
		resolution DetectionSource
	}
	tests := []struct {
		in   string
		want want
		from string
	}{
		// should_parse_full_quality_from_name: both Name.
		{"Series - S01E01 - Title [HDTV-1080p]", want{DetectionName, DetectionName}, "full from name: [HDTV-1080p]"},
		{"Series.Title.S01E01.Bluray720p", want{DetectionName, DetectionName}, "full from name: Bluray720p"},
		{"Series.Title.S01E01.848x480.dvd", want{DetectionName, DetectionName}, "full from name: 848x480.dvd"},
		{"Series.Title.S01E01.1280x720.Bluray", want{DetectionName, DetectionName}, "full from name: 1280x720.Bluray"},

		// should_parse_resolution_from_name: source=Unknown, resolution=Name.
		{"[CR] Series Title - 004 [480p][48CE2D0F]", want{DetectionUnknown, DetectionName}, "res from name: [480p]"},
		{"Series.Title.S01E01.848x480", want{DetectionUnknown, DetectionName}, "res from name: 848x480"},
		{"Series.Title.S01E01.1280x720", want{DetectionUnknown, DetectionName}, "res from name: 1280x720"},

		// should_parse_source_from_name: source=Name, resolution=Unknown.
		{"Series.Title.2011.S02E01.WS.PDTV.x264-REPACK-TLA", want{DetectionName, DetectionUnknown}, "source from name: PDTV no res"},
		{"Series.Title.S01E01.HD.TV", want{DetectionName, DetectionUnknown}, "source from name: HD.TV"},
		{"Series.Title.S01E01.SD.TV", want{DetectionName, DetectionUnknown}, "source from name: SD.TV"},

		// should_parse_quality_from_extension: both Extension.
		{"Series.Title.S01E02.Chained.Heat.mkv", want{DetectionExtension, DetectionExtension}, "ext: .mkv"},
		{"Series - S01E01 - Title.avi", want{DetectionExtension, DetectionExtension}, "ext: .avi"},

		// should_parse_resolution_from_name_and_source_from_extension:
		// source=Extension, resolution=Name.
		{"Series.Title.S01E02.Chained.Heat.1080p.mkv", want{DetectionExtension, DetectionName}, "split: 1080p+mkv"},
		{"Series - S01E01 - Title.720p.avi", want{DetectionExtension, DetectionName}, "split: 720p+avi"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.from, func(t *testing.T) {
			t.Parallel()
			got := ParseQuality(tc.in)
			if got.SourceDetectionSource != tc.want.source {
				t.Errorf("ParseQuality(%q).SourceDetectionSource = %v, want %v", tc.in, got.SourceDetectionSource, tc.want.source)
			}
			if got.ResolutionDetectionSource != tc.want.resolution {
				t.Errorf("ParseQuality(%q).ResolutionDetectionSource = %v, want %v", tc.in, got.ResolutionDetectionSource, tc.want.resolution)
			}
		})
	}
}

// TestParseQuality_RepackVersion ports the should_be_able_to_parse_repack
// fixture method (lines 493-498) — pins the proper/repack version
// bumping at the ParseQuality entry-point level (the standalone
// ParseRevision tests cover the helper directly).
func TestParseQuality_RepackVersion(t *testing.T) {
	tests := []struct {
		in       string
		isRepack bool
		version  int
	}{
		{"Series Title S04E87 REPACK 720p HDTV x264 aAF", true, 2},
		{"Series.Title.S04E87.REPACK.720p.HDTV.x264-aAF", true, 2},
		{"Series.Title.S04E87.REPACK2.720p.HDTV.x264-aAF", true, 3},
		{"Series.Title.S04E87.PROPER.720p.HDTV.x264-aAF", false, 2},
		{"Series.Title.S01E07.RERIP.720p.BluRay.x264-DEMAND", true, 2},
		{"Series.Title.S01E07.RERIP2.720p.BluRay.x264-DEMAND", true, 3},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := ParseQuality(tc.in)
			if got.Revision.Version != tc.version {
				t.Errorf("Version = %d, want %d", got.Revision.Version, tc.version)
			}
			if got.Revision.IsRepack != tc.isRepack {
				t.Errorf("IsRepack = %v, want %v", got.Revision.IsRepack, tc.isRepack)
			}
		})
	}
}

// TestParseQuality_AnimeVersion ports the should_be_able_to_parse_anime_version
// fixture (line 506): the [v2] anime version tag bumps Revision.Version
// to 2 without setting IsRepack.
func TestParseQuality_AnimeVersion(t *testing.T) {
	in := "[MTBB] Series Title - S02E02 - 027 - Episode Title [WEBDL-1080p v2][x264][AAC]"
	got := ParseQuality(in)
	if got.Revision.Version != 2 {
		t.Errorf("Version = %d, want 2", got.Revision.Version)
	}
	if got.Revision.IsRepack {
		t.Error("IsRepack = true, want false (anime version is NOT a repack)")
	}
}

// TestParseQuality_EmptyInput pins the empty-name guard at the top of
// ParseQuality (`QualityParser.cs:72-75`).
func TestParseQuality_EmptyInput(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		got := ParseQuality(in)
		if got.Quality.ID != Unknown.ID {
			t.Errorf("ParseQuality(%q) = %v, want Unknown", in, got.Quality)
		}
	}
}

// TestParseQuality_SelfQualityEnumName ports the
// `parsing_our_own_quality_enum_name` fixture (line 411): for each
// canonical Quality name, embedding it as `My series S01E01 [<Name>]`
// in a release name should round-trip back to the same Quality. This
// exercises the resolution-only fallback paths plus the Bluray720p /
// Bluray1080p concatenated-token fallbacks.
func TestParseQuality_SelfQualityEnumName(t *testing.T) {
	cases := []Quality{
		SDTV, DVD, WEBDL480p, Bluray480p, Bluray576p,
		HDTV720p, HDTV1080p, HDTV2160p,
		WEBDL720p, WEBDL1080p, WEBDL2160p,
		Bluray720p, Bluray1080p, Bluray2160p,
		Bluray1080pRemux, Bluray2160pRemux,
	}
	for _, q := range cases {
		q := q
		t.Run(q.Name, func(t *testing.T) {
			t.Parallel()
			in := "My series S01E01 [" + q.Name + "]"
			got := ParseQuality(in)
			if got.Quality.ID != q.ID {
				t.Errorf("ParseQuality(%q).Quality = %v, want %v", in, got.Quality, q)
			}
		})
	}
}
