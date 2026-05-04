package parser_test

// allSpecRows returns every fixture row ported from the C# fixture
// suite. Each row is one line via the helpers in parser_spec_test.go;
// the source comment above each section names the C# fixture file +
// fixture method the rows came from. Rows tagged via `*x` (sex / mex
// / dailyx / minix / animex) are XFail — the current Go ParseTitle
// implementation does NOT match the expected outcome. As subsequent
// slices land matching rules, those rows should flip from `*x` to the
// non-suffixed form.
//
// Order within each section follows the C# source for diff-ability.
//
// To re-classify rows after a slice lands: re-run `go test`. The
// dispatcher prints `NEWLY-PASSING row N` for every xfail row that
// now matches; flip those `*x` calls to non-suffixed and the xfail
// count drops by one. Conversely a `REGRESSION row N` log means a
// previously-passing row just broke; reorder rules or fix the
// matcher.
func allSpecRows() []specRow {
	var rows []specRow
	rows = append(rows, singleEpisodeRows()...)
	rows = append(rows, multiEpisodeRows()...)
	rows = append(rows, dailyEpisodeRows()...)
	rows = append(rows, miniSeriesRows()...)
	rows = append(rows, animeAbsoluteRows()...)
	return rows
}

// singleEpisodeRows ports SingleEpisodeParserFixture.cs's
// `should_parse_single_episode` cases.
func singleEpisodeRows() []specRow {
	return []specRow{
		// Currently passing — title + S<n>E<nn>:
		se("Series.With.Title.S02E15", "Series With Title", 2, 15),
		se("Series.Title.S02E10.720p.HDTV.x264-DIMENSION", "Series Title", 2, 10),
		se("Series.Title.s12e01.HDTV.x264-LOL", "Series Title", 12, 1),
		se("Series.Title.S03E06.DVDRip.XviD-WiDE", "Series Title", 3, 6),
		se("Series.Title.S10E27.WS.DSR.XviD-2HD", "Series Title", 10, 27),
		se("Series.Title.2010.S02E14.HDTV.XviD-LOL", "Series Title 2010", 2, 14),
		se("Series Title 0 S01E19 720p WEB DL DD5 1 H 264 NT", "Series Title 0", 1, 19),
		se("The Series Title S01E14 A Message Back 720p WEB DL DD5 1 H264 SURFER", "The Series Title", 1, 14),
		se("Series Title S01E07 WS PDTV XviD FUtV", "Series Title", 1, 7),
		se("Series.Title.S03E19.DVDRip.XviD-OSiTV", "Series Title", 3, 19),
		se("Series.Title.2009.S01E14.HDTV.XviD-LOL", "Series Title 2009", 1, 14),
		se("Series.Title.1995.S03E20.HDTV.XviD-LOL", "Series Title 1995", 3, 20),
		se("Series.Title.S03E115.DVDRip.XviD-OSiTV", "Series Title", 3, 115),
		se("Series Title - S02E21 - 94 Meetings - 720p TV.mkv", "Series Title", 2, 21),
		se("Series.Title.Inc.S03E19.DVDRip.\"XviD\"-OSiTV", "Series Title Inc", 3, 19),
		se("Series Title - S06E13 - 5 to 9 [DVD]", "Series Title", 6, 13),
		se("Series Title - S02E21 - 18-5-4", "Series Title", 2, 21),
		se("Series.Title.S01E07.21.0.Jump.Street.720p.WEB-DL.DD5.1.h.264-KiNGS", "Series Title", 1, 7),
		se("Series Title S01E06 3 Beers For Batali DVDRip XviD SPRiNTER", "Series Title", 1, 6),
		se("series.s03.e05.ws.dvdrip.xvid-m00tv", "series", 3, 5),
		se("Series Title - S1936E18 - I Love to Singa", "Series Title", 1936, 18),
		se("Series.S04E09.Swan.Song.1080p.WEB-DL.DD5.1.H.264-ECI", "Series", 4, 9),
		// fixture has misspelled `Galatic`; preserved verbatim since
		// the test asserts the title round-trip.
		// nolint:misspell
		se("Series.Hunters.Galatic.S05E607.720p.hdtv.x264", "Series Hunters Galatic", 5, 607),
		se("Series.Time.With.Dev1.And.Dev2.S01E20.720p.BluRay.x264-DEiMOS", "Series Time With Dev1 And Dev2", 1, 20),
		se("Series.S01E04.2-45.PM.[HDTV-720p].mkv", "Series", 1, 4),
		se("S01E04", "", 1, 4),
		se("10.Lines.You.Know.About.Code.S02E04.Prohibition.HDTV.XviD-AFG", "10 Lines You Know About Code", 2, 4),
		se("30 Series - S01E01 - Pilot.avi", "30 Series", 1, 1),
		se("666 Series Title - S01E01", "666 Series Title", 1, 1),
		se("Series 13 - S01E01", "Series 13", 1, 1),
		se("Don't Ever Trust The B---- in Code 23.S01E01", "Don't Ever Trust The B---- in Code 23", 1, 1),
		se("Warehouse.13.S01E01", "Warehouse 13", 1, 1),
		se("Never.Trust.The.B----.in.Code.23.S01E01", "Never Trust The B---- in Code 23", 1, 1),
		se("42 S01E01", "42", 1, 1),
		se("69.S01E01", "69", 1, 1),
		se("Series.S03E10.Alexandra.720p.WEB-DL.AAC2.0.H.264-CROM.mkv", "Series", 3, 10),

		// Title + Nx<NN> (currently passing):
		se("Series.Title.4x05.HDTV.XviD-LOL", "Series Title", 4, 5),
		se("Series_Title!_-_7x6_-_The_Scarlett_Getter_[SDTV]", "Series Title!", 7, 6),
		se("Series_Title_-_1x1_-_Live_and_Learn_[HDTV-720p]", "Series Title", 1, 1),
		se("Series - 2x12 - The Choice [HDTV-1080p].mkv", "Series", 2, 12),
		se("Series - 2x4 - New Car Smell [HDTV-1080p].mkv", "Series", 2, 4),
		se("Top Series - 07x03 - 2005.11.70", "Top Series", 7, 3),
		se("Top Series - 06x11 - 2005.08.07", "Top Series", 6, 11),
		se("Series_Title.19x06.720p_HDTV_x264-FoV", "Series Title", 19, 6),
		se("The_Big_Series_Title_-_6x12_-_The_Code_Review_Equivalency_[HDTV-720p]", "The Big Series Title", 6, 12),
		se("Series Title - 10x12 - 24 Hour Propane People [SDTV]", "Series Title", 10, 12),

		// 3-digit / 4-digit shortcut (currently passing):
		se("Series.and.a.Title.103.720p.HDTV.X264-DIMENSION", "Series and a Title", 1, 3),
		se("Series.and.a.Title.113.720p.HDTV.X264-DIMENSION", "Series and a Title", 1, 13),
		se("Series.Title.525", "Series Title", 5, 25),
		se("Series.and.a.Title.1013.720p.HDTV.X264-DIMENSION", "Series and a Title", 10, 13),

		// No-title forms (currently passing):
		se("S03E09 WS PDTV XviD FUtV", "", 3, 9),
		se("5x10 WS PDTV XviD FUtV", "", 5, 10),
		se("01x04 - Halloween, Part 1 - 720p WEB-DL", "", 1, 4),
		se("S6E02-Unwrapped-(Playing With Food) - [DarkData]", "", 6, 2),
		se("S06E03-Unwrapped-(Number Ones Unwrapped) - [DarkData]", "", 6, 3),
		se("01x04 - Halloween", "", 1, 4),
		se("1x04", "", 1, 4),
		se("1x1", "", 1, 1),

		// XFail — year-prefixed 3-digit form (Series.2009.416 → S4E16):
		sex("series.2009.416.hdtv-lol", "series 2009", 4, 16),
		sex("series.six-0.2010.217.hdtv-lol", "series six-0 2010", 2, 17),

		// XFail — title with bracketed-Nx<NN> form:
		se("Series Title-0 (2010) - 1x05 - Missing Title", "Series Title-0 (2010)", 1, 5),
		se("Series Title-0 (2010) - 1x05 - Title", "Series Title-0 (2010)", 1, 5),
		// Bracketed-Nx single forms — flipped by extending title+Nx
		// rule's pre-season separator class to admit `[`.
		se("Series (2009) - [06x16] - Room 147.mp4", "Series (2009)", 6, 16),
		se("Series Title [1x05] Episode Title", "Series Title", 1, 5),
		se("Series Title [S01E05] Episode Title", "Series Title", 1, 5),
		sex("[Impatience] Series - 0x01 [720p][34073169].mkv", "Series", 0, 1),
		se("Series Title - [02x01] - Episode 1", "Series Title", 2, 1),
		sex("[RlsGrp] Series Title - S01E27 - 24-Hour", "Series Title", 1, 27),

		// `S<n>.E<nn>` with separator before E (passing via title+S-E
		// rule's Arm A `[-_. ]+E\d{2,3}`):
		se("Series.Title.S15.E06.City.Sushi", "Series Title", 15, 6),
		se("Series Title - S15 E06 - City Code", "Series Title", 15, 6),
		se("Red Show - S02 - E06 - Parallel Series", "Red Show", 2, 6),

		// `S<n>-E<nn>` 1-digit + 3-digit season — flipped by the
		// single-S-E rule's `\d{1,3}` extension (season + episode) plus
		// split-arm trailing iteration to avoid `.N-NN.PM` regression.
		se("Series S1-E1-WEB-DL-1080p-NZBgeek", "Series", 1, 1),
		se("Series S1E1-WEB-DL-1080p-NZBgeek", "Series", 1, 1),
		se("Series.S010E16.720p.HDTV.X264-DIMENSION", "Series", 10, 16),
		se("Series - Season 1 - Episode 01 (Resolution).avi", "Series", 1, 1),
		se("Series Title Season 01 Episode 05 720p", "Series Title", 1, 5),

		// `S2009E09` 4-digit-season form (passing via the title+S-E
		// rule's `\d{4}` season alternation):
		se("2009x09 [SDTV].avi", "", 2009, 9),
		se("S2009E09 [SDTV].avi", "", 2009, 9),
		se("Series Week S2009E09 [SDTV].avi", "Series Week", 2009, 9),

		// Cap.NN forms (Spanish), bracketed [HDTV][Cap.101] etc.:
		se("Series Title - Temporada 2 [HDTV 720p][Cap.201][AC3 5.1 Castellano][www.pctnew.com]", "Series Title", 2, 1),
		se("Series Title - Temporada 2 [HDTV 720p][Cap.1901][AC3 5.1 Castellano][www.pctnew.com]", "Series Title", 19, 1),
		se("Series Title - Temporada 2 [HDTV 720p][Cap.408]", "Series Title", 4, 8),
		se("Series Title [HDTV][Cap.104](website.com).avi", "Series Title", 1, 4),
		se("Series Title [HDTV][Cap.402](website.com).avi", "Series Title", 4, 2),
		se("Series Title [HDTV 720p][Cap.101](website.com).mkv", "Series Title", 1, 1),
		se("Series - Temporada 1 - [HDTV 1080p][Cap.101](wolfmax4k.com)", "Series", 1, 1),
		se("Series [HDTV 1080p][Cap.101](wolfmax4k.com)", "Series", 1, 1),
		se("Series [HDTV 1080p][Cap. 101](wolfmax4k.com).mkv", "Series", 1, 1),

		// XFail — assorted unique shapes:
		// Note: these are from MultiEpisodeParserFixture (multi-form),
		// but rule 5 captures them as multi-episode [1,2] correctly.
		// The single-form spec accepts only [1] as the first; flip
		// to multi via me() in multiEpisodeRows() to avoid false fail.
		// Removed from singleEpisodeRows; covered in multiEpisodeRows.
		se("Series Title!.S2016E14.2016-01-20.avi", "Series Title!", 2016, 14),
		// XFail still — multi-digit episode in 4-digit-season form needs
		// the broader `\d{1,4}` episode shape on title+S-E.
		// 4-digit episode in 4-digit-season form — flipped by extending
		// single-S-E + findAllEpisodes to `\d{1,4}` for episode digits.
		se("Anime Title - S2020E1527 [1527] [2020-10-11] - Episode Title", "Anime Title", 2020, 1527),
		se("Anime Title - S2010E994 [0994] [2010-02-28] - Episode Title [x264 720p][AAC 2ch][HS][Shion+GakiDave]", "Anime Title", 2010, 994),
		se("Series.S006E18.Some.Title.Name-Part.1.1080p.WEB-DL.AAC2.0.H.264-Release", "Series", 6, 18),
		se("Series.2006.S006E18.Some.Title.Name-Part.1.1080p.WEB-DL.AAC2.0.H.264-Release", "Series 2006", 6, 18),
	}
}

// multiEpisodeRows ports MultiEpisodeParserFixture.cs's
// `should_parse_multiple_episodes` cases.
func multiEpisodeRows() []specRow {
	return []specRow{
		// Currently passing — title + S<n>E<nn>E<nn>:
		me("Series.S03E01.S03E02.720p.HDTV.X264-DIMENSION", "Series", 3, []int{1, 2}),
		me("Series.Title.S07E22E23.720p.HDTV.X264-DIMENSION", "Series Title", 7, []int{22, 23}),
		me("Series Title - S07E22 - S07E23 - And Lots of Security.. [HDTV-720p].mkv", "Series Title", 7, []int{22, 23}),
		me("Series.Kings.S02E09-E10.HDTV.x264-ASAP", "Series Kings", 2, []int{9, 10}),
		me("Hell on Series S02E09 E10 HDTV x264 EVOLVE", "Hell on Series", 2, []int{9, 10}),
		me("Hell.on.Series.S02E09-E10.720p.HDTV.x264-EVOLVE", "Hell on Series", 2, []int{9, 10}),
		me("S03E01.S03E02.720p.HDTV.X264-DIMENSION", "", 3, []int{1, 2}),
		me("S02E04E05.720p.BluRay-FUTV", "", 2, []int{4, 5}),
		me("S02E03-04-05.720p.BluRay-FUTV", "", 2, []int{3, 4, 5}),
		me("Series.S01E02E03.1080p.BluRay.x264-DeBTViD", "Series", 1, []int{2, 3}),
		me("Series.Title.S05E01-E02.720p.5.1Ch.BluRay", "Series Title", 5, []int{1, 2}),
		me("Series.Title.S05E01-02.720p.5.1Ch.BluRay", "Series Title", 5, []int{1, 2}),
		me("S01E01-E03 - Episode Title.HDTV-720p", "", 1, []int{1, 2, 3}),
		me("Series Title - S15E06-07 - City Sushi HDTV-720p", "Series Title", 15, []int{6, 7}),
		me("Series Title - S01E01-02-03 - Episode Title HDTV-720p", "Series Title", 1, []int{1, 2, 3}),
		// Bracketed [S01E01-02-03] form — flipped by title+S-E rule's
		// separator class extension to admit `[`.
		me("The Series Title (2010) - [S01E01-02-03] - Episode Title", "The Series Title (2010)", 1, []int{1, 2, 3}),
		me("Series Title (2011) - S01E23-E24 - ...E i nuovi orizzonti [HDTV 360p] [ITA].mkv", "Series Title (2011)", 1, []int{23, 24}),
		me("The Series Title! - S01E01-02-03", "The Series Title!", 1, []int{1, 2, 3}),
		me("Series Title! (2013) - S04E44-E45 - Il 200 spettacolare episodio da narcisisti!", "Series Title! (2013)", 4, []int{44, 45}),
		me("The Series US S01E01-E02 720p HDTV x264", "The Series US", 1, []int{1, 2}),

		// XFail — exotic separator forms:
		me("Series.S03E01-06.DUAL.BDRip.XviD.AC3.-HELLYWOOD", "Series", 3, []int{1, 2, 3, 4, 5, 6}),
		// Multi 3-digit shortcut — flipped by extending the 3-digit
		// shortcut rule with `+` quantifier on the season+episode
		// group + new buildThreeDigitShortcut Build that scans for
		// per-iteration `<sep><1-digit><2-digit>` triples.
		me("Series.Title.103.104.720p.HDTV.X264-DIMENSION", "Series Title", 1, []int{3, 4}),
		me("The Series S01e01 e02 ShoHD On Demand 1080i DD5 1 ALANiS", "The Series", 1, []int{1, 2}),
		me("Series.Title.2x04.2x05.720p.BluRay-FUTV", "Series Title", 2, []int{4, 5}),
		me("2x04x05.720p.BluRay-FUTV", "", 2, []int{4, 5}),
		// XFail — title + Nx_NN multi-episode form (8x01_02 with
		// underscore separator). Needs a title-bearing multi-Nx rule;
		// my Nx-episode rule fires first as single-episode (S8E1).
		mex("Series's Sonarr - 8x01_02 - Free Falling", "Series's Sonarr", 8, []int{1, 2}),
		me("8x01_02 - Free Falling", "", 8, []int{1, 2}),
		// Multi-Nx repeated forms — flipped by the new multi-Nx rule
		// (Parser.cs:120) with the buildTitleNxMulti Build that scans
		// `x<digits>` only (skipping `-<season>` digit runs that the
		// generic findAllEpisodes would otherwise capture).
		me("Series Kings - 2x9-2x10 - Served Code [SDTV] ", "Series Kings", 2, []int{9, 10}),
		me("Series Kings - 2x09-2x10 - Served Code [SDTV] ", "Series Kings", 2, []int{9, 10}),
		me("Series.S01E91-E100", "Series", 1, []int{91, 92, 93, 94, 95, 96, 97, 98, 99, 100}),
		me("Series.S29E161-E165.PDTV.x264-FQM", "Series", 29, []int{161, 162, 163, 164, 165}),
		me("Shortland.Series.S22E5363-E5366.HDTV.x264-FiHTV", "Shortland Series", 22, []int{5363, 5364, 5365, 5366}),
		me("the.Series.101.102.hdtv-lol", "the Series", 1, []int{1, 2}),
		// Extant 5-digit naming (Parser.cs:438) — flipped by the new
		// buildExtantMultiEpisode rule.
		me("Series.10708.hdtv-lol.mp4", "Series", 1, []int{7, 8}),
		me("Series.10910.hdtv-lol.mp4", "Series", 1, []int{9, 10}),
		me("World Series of Sonarr - 2010x15 - 2010x16 - HD TV.mkv", "World Series of Sonarr", 2010, []int{15, 16}),
		me("Series Title Season 01 Episode 05-06 720p", "Series Title", 1, []int{5, 6}),
		mex("The Series And The Code - S42 Ep10718 - Ep10722", "The Series And The Code", 42, []int{10718, 10719, 10720, 10721, 10722}),
		me("Series Title.S6.E1.E2.Episode Name.1080p.WEB-DL", "Series Title", 6, []int{1, 2}),
		me("Series Title.S6E1-E2.Episode Name.1080p.WEB-DL", "Series Title", 6, []int{1, 2}),
		me("Series Title.S6E1-S6E2.Episode Name.1080p.WEB-DL", "Series Title", 6, []int{1, 2}),
		me("Series Title.S6E1E2.Episode Name.1080p.WEB-DL", "Series Title", 6, []int{1, 2}),
		me("Series Title.S6E1-E2-E3.Episode Name.1080p.WEB-DL", "Series Title", 6, []int{1, 2, 3}),
		me("1x01-x03 - Episode Title.HDTV-720p", "", 1, []int{1, 2, 3}),
		mex("Series.Title.E07-E08.180612.1080p-NEXT", "Series Title", 1, []int{7, 8}),
		mex("Series Title? E11-E12 1080p HDTV AAC H.264-NEXT", "Series Title", 1, []int{11, 12}),
		mex("Series Title - [02x01x02] - Episode 1", "Series Title", 2, []int{1, 2}),
		mex("Series Title - [02x01-x02] - Episode 1", "Series Title", 2, []int{1, 2}),
		mex("Series Title - [02x01-02] - Episode 1", "Series Title", 2, []int{1, 2}),
		mex("Series Title (S15E06-08) City Sushi", "Series Title", 15, []int{6, 7, 8}),
		mex("Series Title (S05E06-08 of 24) City Sushi", "Series Title", 5, []int{6, 7, 8}),
		me("Series.S101E01-E02.Some.Title.Name.1080p.WEB-DL.AAC2.0.H.264-Release", "Series", 101, []int{1, 2}),
	}
}

// dailyEpisodeRows ports DailyEpisodeParserFixture.cs's
// `should_parse_daily_episode` cases.
func dailyEpisodeRows() []specRow {
	return []specRow{
		// Currently passing — canonical airdate forms:
		daily("Series Title 2011 04 18 Emma Roberts HDTV XviD BFF", "Series Title", "2011-04-18"),
		daily("A Late Talk Show 2011 04 15 1080i HDTV DD5 1 MPEG2 TrollHD", "A Late Talk Show", "2011-04-15"),
		daily("A.Late.Talk.Show.2010.10.11.Johnny.Knoxville.iTouch-MW", "A Late Talk Show", "2010-10-11"),
		daily("A Late Talk Show - 2011-04-12 - Gov. Deval Patrick", "A Late Talk Show", "2011-04-12"),
		daily("2011.01.10 - A Late Talk Show- HD TV.mkv", "", "2011-01-10"),
		daily("2011.03.13 - A Late Talk Show - HD TV.mkv", "", "2011-03-13"),
		daily("Series Title - 2013-10-30 - Episode Title (1) [HDTV-720p]", "Series Title", "2013-10-30"),
		// XFail — title contains both daily airdate AND s01e13. C#
		// resolves this via Parser.cs:161 (airdate+season+episode
		// hybrid rule, "capture season/episode only" — which despite
		// its name routes through the daily branch in the consumer).
		// The Go port's title+S-E rule fires first and captures S1E13;
		// fixing requires the hybrid rule + the consumer's airdate
		// preference logic.
		dailyx("The Show Series 2015 02 09 WEBRIP s01e13", "The Show Series", "2015-02-09"),
		daily("Tree_Series_2018_06_22_Seth_Meyers_720p_HEVC_x265-MeGusta", "Tree Series", "2018-06-22"),

		// Reversed-date forms (Parser.cs:354 / :358) — flipped by the
		// month-day-year + day-month-year rules with the C# ambiguous-
		// date logic (both ≤ 12 → ambiguous → reject).
		daily("The_Series_US_04.28.2014_hdtv.x264.Poke.mp4", "The Series US", "2014-04-28"),
		// 6-digit YYMMDD form — flipped by the SixDigitAirDateRegex
		// preprocessor that rewrites `_YYMMDD_` to `_20YY.MM.DD_`
		// before the standard 4-2-2 airdate rule fires.
		daily("A.Late.Talk.Show.140722.720p.HDTV.x264-YesTV", "A Late Talk Show", "2014-07-22"),
		daily("A_Late_Talk_Show_140722_720p_HDTV_x264-YesTV", "A Late Talk Show", "2014-07-22"),
		// 8-digit YYYYMMDD form — flipped by the dedicated 8-digit
		// airdate rule below the standard 4-2-2 rule.
		daily("Series and Title 20201013 Ep7432 [720p WebRip (x264)] [SUBS]", "Series and Title", "2020-10-13"),
		dailyx("Series Title (1955) - 1954-01-23 05 00 00 - Cottage for Sale.ts", "Series Title (1955)", "1954-01-23"),
		daily("Series Title - 30-04-2024 HDTV 1080p H264 AAC", "Series Title", "2024-04-30"),
		daily("Series.Title.13.02.2025.1080i.HDTV.MPA2.0.H.264", "Series Title", "2025-02-13"),
		dailyx("Series On TitleClub E76 2024 08 08 1080p WEB H264-RnB96 [TJET]", "Series On TitleClub", "2024-08-08"),
		daily("Series.2025.09.01.The.170.Million.Pound.Diamond.Scam.1080p.HDTV.H264-DEADPOOL'", "Series", "2025-09-01"),
		// No-title airdate at start (Parser.cs:33) — flipped by the
		// dedicated no-title-airdate rule placed before the no-title
		// multi/single + title+Nx rules.
		daily("20161024- Exotic Payback.21x41_720.mkv", "", "2016-10-24"),
		// Title-less airdate forms — flipped by the no-title airdate
		// rule (Parser.cs:33) that fires before no-title multi/single.
		daily("2018-11-14.1080.all.mp4", "", "2018-11-14"),
		daily("2019_08_20_1080_all.mp4", "", "2019-08-20"),
	}
}

// miniSeriesRows ports MiniSeriesEpisodeParserFixture.cs's
// `should_parse_mini_series_episode` and
// `should_parse_korean_series_episode` cases.
func miniSeriesRows() []specRow {
	return []specRow{
		mini("The.Big.Series.Leader.Part.2.DSR.XviD-SYS", "The Big Series Leader", 2),
		mini("Series and Show 2012 Part 1 REPACK 720p HDTV x264 2HD", "Series and Show 2012", 1),
		mini("Series Show.2016.E04.Power.720p.WEB-DL.DD5.1.H.264-MARS", "Series Show 2016", 4),
		// XFail — `e07-720p` form (no Part keyword, dash-separated).
		minix("kill-roy-was-here-e07-720p", "kill-roy-was-here", 7),

		// Korean F1RST/NEXT (passing via PreSubstitutionRegex rule 1):
		mini("It's a Series Title.E56.190121.720p-NEXT.mp4", "It's a Series Title", 56),
		mini("My Only Series Title.E37.190120.1080p-NEXT.mp4", "My Only Series Title", 37),
		mini("Series.E191.190121.720p-NEXT.mp4", "Series", 191),
		mini("The Series Title Challenge.E932.190120.720p-NEXT.mp4", "The Series Title Challenge", 932),
	}
}

// animeAbsoluteRows ports AbsoluteEpisodeNumberParserFixture.cs cases
// where the title parses to a single absolute-episode number (the
// majority of the file).
func animeAbsoluteRows() []specRow {
	return []specRow{
		// Currently passing — broad anime [SubGroup] form:
		anime("[Commie]_Some_Anime_Show_-_11_[65F220B4]", "Some Anime Show", 11),
		anime("[Underwater]_Some_Anime_Show_-_12_(720p)_[5C7BC4F9]", "Some Anime Show", 12),
		anime("[Commie]_Some_Anime_Show_-_15_[E76552EA]", "Some Anime Show", 15),
		anime("[HorribleSubs]_Some_Anime_Show_-_33_[720p]", "Some Anime Show", 33),
		anime("[HorribleSubs]_Some_Anime_Show_-_145_[720p]", "Some Anime Show", 145),
		anime("[HorribleSubs] Some Anime Show - 13 [1080p].mkv", "Some Anime Show", 13),
		anime("[K-F] Some Anime Show 214", "Some Anime Show", 214),
		anime("[FroZen] Series Title - 23 [DVD][7F6170E6]", "Series Title", 23),
		anime("[Commie] Series Title - 32 [0BA19D5B]", "Series Title", 32),
		anime("[HorribleSubs] Series Title - 32 [480p]", "Series Title", 32),
		anime("[CR] Series Title - 004 [480p][48CE2D0F]", "Series Title", 4),
		anime("[Chibiki] Series Title!! - 42 [360p][7A4FC77B]", "Series Title!!", 42),
		anime("[HorribleSubs] Series Title - 32 [1080p]", "Series Title", 32),
		anime("[DeadFish] Series Title - 09v2 [720p][AAC]", "Series Title", 9),
		anime("[SubDESU]_Show_One_07_(1280x720_x264-AAC)_[6B7FD717]", "Show One", 7),
		anime("[Chihiro]_Show!!_-_06_[848x480_H.264_AAC][859EEAFA]", "Show!!", 6),
		anime("[Doki]Series Title - 07 (1280x720 Hi10P AAC) [80AF7DDE]", "Series Title", 7),
		anime("[Underwater-FFF] No Series Title No Life - 01 (720p) [27AAA0A0].mkv", "No Series Title No Life", 1),
		anime("[Underwater-FFF] Series Title - 01 (720p) [27AAA0A0]", "Series Title", 1),
		anime("[S-T-D] Series Title! - 06 (1280x720 10bit AAC) [59B3F2EA].mkv", "Series Title!", 6),

		// XFail — title + absolute (no [SubGroup] brackets):
		animex("Some Anime Show - 031 - The Resolution to Kill [Lunar].avi", "Some Anime Show", 31),
		animex("Some Anime Show - 031 - The Resolution to Kill [Lunar]", "Some Anime Show", 31),
		animex("Some_Anime_Show_e66_time_is_money_part_one_marking_time", "Some Anime Show", 66),
		animex("Series Title - 010 (720p) [27AAA0A0].mkv", "Series Title", 10),
		animex("Initial_Series_Title - 01 DVD - Central Anime", "Initial Series Title", 1),
		animex("Initial_Series_Title_-_01(DVD)_-_(Central_Anime)[5AF6F1E4].mkv", "Initial Series Title", 1),
	}
}
