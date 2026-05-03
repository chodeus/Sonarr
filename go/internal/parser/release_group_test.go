package parser_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

// Black-box tests for ReleaseGroupParser.
//
// Cases ported verbatim from
// `src/NzbDrone.Core.Test/ParserTests/ReleaseGroupParserFixture.cs`.
// The fixture has 5 main test methods totalling ~150 cases:
//   - should_parse_release_group (~50 cases) — the heavy regex's bread-and-butter
//   - should_parse_exception_release_group (~65 cases) — manual-list overrides
//   - should_parse_anime_release_groups (5 cases)
//   - should_not_parse_anime_hash_as_release_group (1 case)
//   - should_not_parse_episode_title_as_release_group (3 cases)
//   - should_not_include_repost_in_release_group (~25 cases)
//   - should_not_include_language_in_release_group (3 cases)
//
// Each case asserts (input, expected_release_group) where expected can
// be `null` (Go: empty string) when no group should be detected.

func TestParseReleaseGroup_Standard(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		// from `should_parse_release_group`
		{"Series.2009.S01E14.English.HDTV.XviD-LOL", "LOL"},
		{"Series 2009 S01E14 English HDTV XviD LOL", ""},
		{"Series Now S05 EXTRAS DVDRip XviD RUNNER", ""},
		{"Series.Title.S01.EXTRAS.DVDRip.XviD-RUNNER", "RUNNER"},
		{"2020.Series.2011.12.02.PDTV.XviD-C4TV", "C4TV"},
		{"The.Series.S03E115.DVDRip.XviD-OSiTV", "OSiTV"},
		{"Series Title - S01E01 - Pilot [HTDV-480p]", ""},
		{"Series Title - S01E01 - Pilot [HTDV-720p]", ""},
		{"Series Title - S01E01 - Pilot [HTDV-1080p]", ""},
		{"The.Series.Title.S04E13.720p.WEB-DL.AAC2.0.H.264-Cyphanix", "Cyphanix"},
		{"Series.S02E01.720p.WEB-DL.DD5.1.H.264.mkv", ""},
		{"Series Title S01E01 Episode Title", ""},
		{"The Series Title - 2014-06-02 - Thomas Piketty.mkv", ""},
		{"The Series Title S12E17 May 23, 2014.mp4", ""},
		{"The Series Title 10x11 - Wild Devs Cant Be Broken [rl].avi", "rl"},
		{"[ www.Torrenting.com ] - Series.S03E14.720p.HDTV.X264-DIMENSION", "DIMENSION"},
		{"Series S02E09 HDTV x264-2HD [eztv]-[rarbg.com]", "2HD"},
		{"7s-Series-s02e01-720p.mkv", ""},
		{"The.Series.S09E13.720p.HEVC.x265-MeGusta-Pre", "MeGusta"},
		{"Series Title - S01E01 - Episode Title [RlsGroup]", "RlsGroup"},
		{"Red Series S01 E01-E02 1080p AMZN WEBRip DDP5.1 x264 monkee", ""},
		{"Series.Title.S01E05.The-Anivers" + "ary.WEBDL-1080p.mkv", ""},
		{"Series.Title.S01E05.The-Anivers" + "ary.HDTV-1080p.mkv", ""},
		{"Series US (2010) S04 (1080p BDRip x265 10bit DTS-HD MA 5 1 - WEM)[TAoE]", "TAoE"},
		{"The.Series.S03E04.2160p.Amazon.WEBRip.DTS-HD.MA.5.1.x264", ""},
		{"SomeShow.S20E13.1080p.BluRay.DTS-X.MA.5.1.x264", ""},
		{"SomeShow.S20E13.1080p.BluRay.DTS-MA.5.1.x264", ""},
		{"SomeShow.S20E13.1080p.BluRay.DTS-ES.5.1.x264", ""},
		{"SomeShow.S20E13.1080p.Blu-Ray.DTS-ES.5.1.x264", ""},
		{"SomeShow.S20E13.1080p.Blu-Ray.DTS-ES.5.1.x264-ROUGH [PublicHD]", "ROUGH"},
		{"SomeShow S01E168 1080p WEB-DL AAC 2.0 x264-Erai-raws", "Erai-raws"},
		{"The.Good.Series.S05E03.Series.of.Intelligence.1080p.10bit.AMZN.WEB-DL.DDP5.1.HEVC-Vyndros", "Vyndros"},
		{"[Tenrai-Sensei] Series [BD][1080p][HEVC 10bit x265][Dual Audio]", "Tenrai-Sensei"},
		{"[Erai-raws] Series - 0955 ~ 1005 [1080p]", "Erai-raws"},
		{"[Exiled-Destiny] Series Title", "Exiled-Destiny"},
		{"Series.Title.S01E09.1080p.DSNP.WEB-DL.DDP2.0.H.264-VARYG", "VARYG"},
		{"Series (1997) - S01E01-02 - Children of the Gods (Showtime) (1080p.BD.DD5.1.x265-TheSickle[TAoE])", "TAoE"},
		{"Series Title S01 [2160p REMUX] [HEVC DV HYBRID HDR10+ Dolby TrueHD Atmos 7 1 24-bit Audio English] [Data Lass]", ""},
		{"Series Title S01 [2160p REMUX] [HEVC DV HYBRID HDR10+ Dolby TrueHD Atmos 7 1 24-bit Audio English]-DataLass", "DataLass"},
		{"Series Title S01 REMUX Dual Audio AVC 1080p 8-Bit-ZR-", "ZR"},
		{"Show.Name.2009.S01.1080p.BluRay.DTS5.1.x264-D-Z0N3", "D-Z0N3"},
		{"Series Stampede S01 MULTi 1080p BD x265 Opus AAC -Báleygr", "Báleygr"},
		{"Series S01E01 VOSTFR 1080p WEB x265 EAC3 -Hveðrungr", "Hveðrungr"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseReleaseGroup(tc.title)
			if got != tc.want {
				t.Errorf("ParseReleaseGroup(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

func TestParseReleaseGroup_Exception(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		// from `should_parse_exception_release_group` — manual-list groups
		{"Show.Name.S01E01.1080p.WEB-DL.H264.Fight-BB.mkv", "Fight-BB"},
		{"Show Name (2021) Season 1 S01 (1080p BluRay x265 HEVC 10bit AAC 5.1 Tigole) [QxR]", "QxR"},
		{"Show Name (2021) Season 1 S01 (1080p BluRay x265 HEVC 10bit AAC 2.0 afm72) [QxR]", "QxR"},
		{"Show Name (2021) Season 1 S01 (1080p BluRay x265 HEVC 10bit AAC 2.0 Panda) [QxR]", "QxR"},
		{"Show Name (2020) Season 1 S01 (1080p AMZN WEB-DL x265 HEVC 10bit EAC3 2.0 Ghost) [QxR]", "QxR"},
		{"The Show S08E09 The Series.1080p.AMZN.WEB-DL.x265.10bit.EAC3.6.0-Qman[UTR]", "UTR"},
		{"The Show S03E07 Fire and Series[1080p x265 10bit S87 Joy]", "Joy"},
		{"The Show (2016) - S02E01 - Soul Series #1 (1080p NF WEBRip x265 ImE)", "ImE"},
		{"The Show (2020) - S02E03 - Fighting His Series(1080p ATVP WEB-DL x265 t3nzin)", "t3nzin"},
		{"[Anime Time] A Show [BD][Dual Audio][1080p][HEVC 10bit x265][AAC][Eng Sub] [Batch] Title)", "Anime Time"},
		{"[Project Angel] Anime Series [DVD 480p] [10-bit x265 HEVC | Opus]", "Project Angel"},
		{"[Hakata Ramen] Show Title - Season 2 - Revival of The Commandments", "Hakata Ramen"},
		{"Show Name (2022) S01 (2160p DSNP WEB-DL H265 DV HDR DDP Atmos 5.1 English - HONE)", "HONE"},
		{"Series.Title.S01E09.1080p.DSNP.WEB-DL.DDP2.0.H.264-VARYG (Blue Lock, Multi-Subs)", "VARYG"},
		{"Series.Title (2014) S09E10 (1080p AMZN WEB-DL x265 HEVC 10bit DDP 5.1 Vyndros)", "Vyndros"},
		{"Series Title S02E03 Title 4k to 1080p DSNP WEBrip x265 DDP 5 1 Releaser[SEV]", "SEV"},
		{"Series Title (2005) - S01E01 - A Hard Day's Night (1080p DSNP WEB-DL x265 Garshasp).mkv", "Garshasp"},
		{"Series Title (2015) - S02E04 - Smoke & Mirrors (1080p BluRay x265 Kappa).mkv", "Kappa"},
		{"Series Title (2019) - S01E01 - Episode 1 (1080p WEB-DL x265 Natty).mkv", "Natty"},
		{"Series Title (2021) - S01E02 - Here Goes Nothing (1080p WEB-DL x265 SAMPA).mkv", "SAMPA"},
		{"Series Title (2021) - S01E01 - Aftermath (1080p DSNP WEB-DL x265 YOGI).mkv", "YOGI"},
		{"Series Title (2012) - S01E01 - Episode 1 (1080p BluRay x265 r00t).mkv", "r00t"},
		{"Series Title - S01E01 - Girls Gone Wild Exposed (720p x265 EDGE2020).mkv", "EDGE2020"},
		{"Series.Title.S01E02.1080p.BluRay.Remux.AVC.FLAC.2.0-E.N.D", "E.N.D"},
		{"Series Title S01 1080p Blu-ray Remux AVC FLAC 2.0 - KRaLiMaRKo", "KRaLiMaRKo"},
		{"Series Title S01 1080p Blu-ray Remux AVC DTS-HD MA 2.0 - BluDragon", "BluDragon"},
		{"Example (2013) S01E01 (1080p iP WEBRip x265 SDR AAC 2.0 English - DarQ)", "DarQ"},
		{"Series.Title.S08E03.720p.WEB.DL.AAC2.0.H.264.KCRT", "KCRT"},
		{"Series Title S02E05 2160p WEB-DL DV HDR ENG DDP5.1 Atmos H265 MP4-BEN THE MEN", "BEN THE MEN"},
		{"Series.S01E05.1080p.WEB-DL.DDP5.1.H264-BEN.THE.MEN", "BEN.THE.MEN"},
		{"Series (2008) S05E01 (1080p BDRip DDP2.0 x265) - Vialle", "Vialle"},
		{"The Series (2023) S03E01 (1080p DS4K NF Webrip DV HDR DDP5.1 x265) - Vialle.mkv", "Vialle"},

		// FAKE-suffix negatives — t3nzinFAKE / VialleFAKE must NOT be detected
		// (the trailing FAKE breaks the exact-list match).
		{"The Show (2020) - S02E03 - Fighting His Series(1080p ATVP WEB-DL x265 t3nzinFAKE)", ""},
		{"The Series (2023) S03E01 (1080p DS4K NF Webrip DV HDR DDP5.1 x265) - VialleFAKE.mkv", ""},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseReleaseGroup(tc.title)
			if got != tc.want {
				t.Errorf("ParseReleaseGroup(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

func TestParseReleaseGroup_Anime(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		// from `should_parse_anime_release_groups`
		{"[FFF] Series Title!! - S01E11 - Someday, With Sonarr", "FFF"},
		{"[HorribleSubs] Series Title!! - S01E12 - Sonarr Going Well!!", "HorribleSubs"},
		{"[Anime-Koi] Series Title - S01E06 - Guys From Sonarr", "Anime-Koi"},
		{"[Anime-Koi] Series Title - S01E07 - A High-Grade Sonarr", "Anime-Koi"},
		{"[Anime-Koi] Series Title 2 - 01 [h264-720p][28D54E2C]", "Anime-Koi"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseReleaseGroup(tc.title)
			if got != tc.want {
				t.Errorf("ParseReleaseGroup(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

func TestParseReleaseGroup_AnimeHashNegative(t *testing.T) {
	// from `should_not_parse_anime_hash_as_release_group`
	got := parser.ParseReleaseGroup("Terrible.Anime.Title.001.DBOX.480p.x264-iKaos [v3] [6AFFEF6B]")
	if got != "" {
		t.Errorf("ParseReleaseGroup anime-hash = %q, want empty", got)
	}
}

func TestParseReleaseGroup_EpisodeTitleNegative(t *testing.T) {
	// from `should_not_parse_episode_title_as_release_group`
	cases := []string{
		"Some TV (1979) - S02E10 - Hart-Shaped Murder [SDTV][AAC 2.0][x264]",
		"Some TV (1979) - S02E10 - Hart-Shaped Murder [HDTV-480p][AAC 2.0][x264]",
		"Some TV (1979) - S02E10 - Hart-Shaped Murder [480p-HDTV][AAC 2.0][x264]",
	}
	for _, title := range cases {
		t.Run(title, func(t *testing.T) {
			got := parser.ParseReleaseGroup(title)
			if got != "" {
				t.Errorf("ParseReleaseGroup(%q) = %q, want empty", title, got)
			}
		})
	}
}

func TestParseReleaseGroup_LanguageNotIncluded(t *testing.T) {
	// from `should_not_include_language_in_release_group`
	cases := []struct {
		title string
		want  string
	}{
		{"Series.Title.S02E04.720p.WEBRip.x264-SKGTV English", "SKGTV"},
		{"Series.Title.S02E04.720p.WEBRip.x264-SKGTV_English", "SKGTV"},
		{"Series.Title.S02E04.720p.WEBRip.x264-SKGTV.English", "SKGTV"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseReleaseGroup(tc.title)
			if got != tc.want {
				t.Errorf("ParseReleaseGroup(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

func TestParseReleaseGroup_RepostStripped(t *testing.T) {
	// from `should_not_include_repost_in_release_group` — these
	// exercise the `-(RP|Obfuscated|Rakuv*|...)+$` chain in
	// CleanReleaseGroupRegex.
	cases := []struct {
		title string
		want  string
	}{
		{"Series.Title.S02E04.720p.WEB-DL.AAC2.0.H.264-EVL-RP", "EVL"},
		{"Series.Title.S02E04.720p.WEB-DL.AAC2.0.H.264-EVL-RP-RP", "EVL"},
		{"Series.Title.S02E04.720p.WEB-DL.AAC2.0.H.264-EVL-Obfuscated", "EVL"},
		{"Series.Title.S04E04.720p.BluRay.x264-xHD-NZBgeek", "xHD"},
		{"Series.Title.S05E11.720p.HDTV.X264-DIMENSION-NZBgeek", "DIMENSION"},
		{"Series.Title.S04E04.720p.BluRay.x264-xHD-1", "xHD"},
		{"Series.Title.S05E11.720p.HDTV.X264-DIMENSION-1", "DIMENSION"},
		{"series.title.s40e11.kevin.hart_sia.720p.hdtv.x264-w4f-sample.mkv", "w4f"},
		{"The.Series.2017.S05E02.1080p.WEB-DL.DD5.1.H264-EVL-Scrambled", "EVL"},
		{"Series.S01E08.Haunted.Hayride.720p.AMZN.WEBRip.DDP5.1.x264-NTb-postbot", "NTb"},
		{"Series.S01E08.Haunted.Hayride.720p.AMZN.WEBRip.DDP5.1.x264-NTb-xpost", "NTb"},
		{"Series.Title.S08E05.The.Forgotten.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb-Rakuv", "NTb"},
		{"The.Series.S30E01.Devs.Not.Dead.1080p.AMZN.WEB-DL.DDP5.1.H264-QOQ-Rakuv02", "QOQ"},
		{"Lie.To.Developers.S01E13.720p.BluRay.x264-SiNNERS-Rakuvfinhel", "SiNNERS"},
		{"Who.is.Sonarr.S01E01.INTERNAL.720p.HDTV.x264-aAF-RakuvUS-Obfuscated", "aAF"},
		{"Deadly.Development.S01E10.Sink.With.Code.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTG-WhiteRev", "NTG"},
		{"The.Sonarr.Series.S09E12.Developers.REPACK.1080p.AMZN.WEB-DL.DD.5.1.H.264-CasStudio-BUYMORE", "CasStudio"},
		{"2.Tired.Developers.S02E24.1080p.AMZN.WEBRip.DD5.1.x264-CasStudio-AsRequested", "CasStudio"},
		{"Series.S04E11.Lines.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb-AlternativeToRequested", "NTb"},
		{"Series.S16E04.Third.Wheel.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb-GEROV", "NTb"},
		{"Series.and.Title.S10E06.Dev.n.Play.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb-Z0iDS3N", "NTb"},
		{"Absolute.Series.S02E06.The.House.of.Sonarr.DVDRip.x264-MaG-Chamele0n", "MaG"},
		{"The.Series.Title.S08E08.1080p.BluRay.x264-ROVERS-4P", "ROVERS"},
		{"Series.Title.S01E02.720p.BluRay.X264-REWARD-4Planet", "REWARD"},
		{"Series.S01E01.Rites.of.Passage.1080p.BluRay.x264-DON-AlteZachen", "DON"},
		{"Series.Title.S04E06.Episode.Name.720p.WEB-DL.DD5.1.H.264-HarrHD-RePACKPOST", "HarrHD"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseReleaseGroup(tc.title)
			if got != tc.want {
				t.Errorf("ParseReleaseGroup(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}
