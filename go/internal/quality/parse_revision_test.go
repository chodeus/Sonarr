package quality_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

// TestParseRevision_RepackAndProper ports
// `should_be_able_to_parse_repack` from
// src/NzbDrone.Core.Test/ParserTests/QualityParserFixture.cs:493-504.
func TestParseRevision_RepackAndProper(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title    string
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
	for _, c := range cases {
		got := quality.ParseRevision(c.title)
		if got.Version != c.version {
			t.Errorf("ParseRevision(%q).Version = %d, want %d", c.title, got.Version, c.version)
		}
		if got.IsRepack != c.isRepack {
			t.Errorf("ParseRevision(%q).IsRepack = %v, want %v", c.title, got.IsRepack, c.isRepack)
		}
	}
}

// TestParseRevision_AnimeVersion ports
// `should_be_able_to_parse_anime_version`.
func TestParseRevision_AnimeVersion(t *testing.T) {
	t.Parallel()
	const title = "[MTBB] Series Title - S02E02 - 027 - Episode Title [WEBDL-1080p v2][x264][AAC]"
	got := quality.ParseRevision(title)
	if got.Version != 2 {
		t.Errorf("ParseRevision(%q).Version = %d, want 2", title, got.Version)
	}
}

// TestParseRevision_NoTagsDefaultsToV1 confirms a release name with
// no PROPER / REPACK / RERIP / vN / REAL tags returns the default
// Revision (Version=1, Real=0, IsRepack=false).
func TestParseRevision_NoTagsDefaultsToV1(t *testing.T) {
	t.Parallel()
	got := quality.ParseRevision("Series.Title.S01E01.720p.HDTV.x264-Group")
	if got != quality.NewRevision() {
		t.Errorf("ParseRevision (plain) = %+v, want default v1", got)
	}
}

// TestParseRevision_RealMatchesAreCaseSensitive confirms the C#
// RealRegex case-sensitive declaration is preserved. Lowercase
// "real" is NOT a real-tag.
func TestParseRevision_RealMatchesAreCaseSensitive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title string
		want  int
	}{
		{"Series.Title.S01E01.REAL.720p.HDTV", 1},
		{"Series.Title.S01E01.REAL.PROPER.REAL", 2}, // two REALs counted
		{"Series.Title.S01E01.real.720p.HDTV", 0},   // lowercase ignored
		{"Series.Title.S01E01.Real.720p.HDTV", 0},   // titlecase ignored
	}
	for _, c := range cases {
		got := quality.ParseRevision(c.title).Real
		if got != c.want {
			t.Errorf("ParseRevision(%q).Real = %d, want %d", c.title, got, c.want)
		}
	}
}

// TestParseRevision_ProperBumpsVersion confirms a PROPER tag without
// a v-tag bumps Version to 2; with a v-tag it bumps to that+1.
func TestParseRevision_ProperBumpsVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title string
		want  int
	}{
		{"Series.Title.S01E01.PROPER.720p.HDTV", 2},
		// "[v2]" tag + PROPER → version captured as 2, then PROPER bumps to 3.
		{"Series.Title.S01E01.[v2].PROPER.720p.HDTV", 3},
	}
	for _, c := range cases {
		got := quality.ParseRevision(c.title).Version
		if got != c.want {
			t.Errorf("ParseRevision(%q).Version = %d, want %d", c.title, got, c.want)
		}
	}
}

// TestParseRevision_UnderscoreToSpaceNormalisation confirms the C#
// `name.Replace('_', ' ')` step before regex matching: an
// underscore-separated release name still matches PROPER / REPACK /
// vN tags.
func TestParseRevision_UnderscoreToSpaceNormalisation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title    string
		isRepack bool
		version  int
	}{
		{"Series_Title_S01E01_PROPER_720p_HDTV", false, 2},
		{"Series_Title_S01E01_REPACK_720p_HDTV", true, 2},
	}
	for _, c := range cases {
		got := quality.ParseRevision(c.title)
		if got.Version != c.version || got.IsRepack != c.isRepack {
			t.Errorf("ParseRevision(%q) = %+v, want Version=%d IsRepack=%v",
				c.title, got, c.version, c.isRepack)
		}
	}
}
