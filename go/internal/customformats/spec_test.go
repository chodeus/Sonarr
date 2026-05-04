package customformats

import (
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

func TestReleaseTitleSpec_MatchesReleaseTitle(t *testing.T) {
	spec := &ReleaseTitleSpec{}
	if err := spec.SetPattern("HDTV"); err != nil {
		t.Fatal(err)
	}

	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{
			ReleaseTitle: "Series.Title.S01E05.HDTV.x264-LOL",
		},
	}
	if !spec.IsSatisfiedBy(input) {
		t.Errorf("expected match against ReleaseTitle")
	}
}

func TestReleaseTitleSpec_MatchesFilename(t *testing.T) {
	spec := &ReleaseTitleSpec{}
	if err := spec.SetPattern("WEB-DL"); err != nil {
		t.Fatal(err)
	}

	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{
			ReleaseTitle: "no quality token here",
		},
		Filename: "Series.Title.S01E05.WEB-DL.x264-LOL.mkv",
	}
	if !spec.IsSatisfiedBy(input) {
		t.Errorf("expected match against Filename when ReleaseTitle doesn't match")
	}
}

func TestReleaseTitleSpec_NoMatch(t *testing.T) {
	spec := &ReleaseTitleSpec{}
	if err := spec.SetPattern("HDTV"); err != nil {
		t.Fatal(err)
	}

	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{
			ReleaseTitle: "Series.Title.S01E05.WEB-DL.x264-LOL",
		},
	}
	if spec.IsSatisfiedBy(input) {
		t.Errorf("did not expect match")
	}
}

func TestReleaseTitleSpec_Negate(t *testing.T) {
	spec := &ReleaseTitleSpec{
		BaseSpecification: BaseSpecification{Negate: true},
	}
	if err := spec.SetPattern("HDTV"); err != nil {
		t.Fatal(err)
	}

	// HDTV is in the title — without Negate this would match. With
	// Negate, it should NOT satisfy.
	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{
			ReleaseTitle: "Series.Title.S01E05.HDTV.x264-LOL",
		},
	}
	if spec.IsSatisfiedBy(input) {
		t.Errorf("Negate=true should invert a positive match")
	}

	// And inverting a non-match should produce a positive.
	input.EpisodeInfo.ReleaseTitle = "Series.Title.S01E05.WEB-DL.x264-LOL"
	if !spec.IsSatisfiedBy(input) {
		t.Errorf("Negate=true should invert a non-match")
	}
}

func TestReleaseTitleSpec_NilEpisodeInfo(t *testing.T) {
	spec := &ReleaseTitleSpec{}
	if err := spec.SetPattern("HDTV"); err != nil {
		t.Fatal(err)
	}

	// Both EpisodeInfo and Filename empty → no match.
	if spec.IsSatisfiedBy(CustomFormatInput{}) {
		t.Errorf("empty input should not match")
	}

	// Only Filename set → matches if regex hits.
	input := CustomFormatInput{Filename: "Series.S01E05.HDTV.x264-LOL.mkv"}
	if !spec.IsSatisfiedBy(input) {
		t.Errorf("Filename-only input should match")
	}
}

func TestReleaseGroupSpec_Match(t *testing.T) {
	spec := &ReleaseGroupSpec{}
	if err := spec.SetPattern("LOL"); err != nil {
		t.Fatal(err)
	}

	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseGroup: "LOL"},
	}
	if !spec.IsSatisfiedBy(input) {
		t.Errorf("expected match")
	}
}

func TestReleaseGroupSpec_NoMatch(t *testing.T) {
	spec := &ReleaseGroupSpec{}
	if err := spec.SetPattern("LOL"); err != nil {
		t.Fatal(err)
	}

	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseGroup: "DIMENSION"},
	}
	if spec.IsSatisfiedBy(input) {
		t.Errorf("did not expect match")
	}
}

func TestReleaseGroupSpec_NilEpisodeInfo(t *testing.T) {
	spec := &ReleaseGroupSpec{}
	if err := spec.SetPattern("LOL"); err != nil {
		t.Fatal(err)
	}
	if spec.IsSatisfiedBy(CustomFormatInput{}) {
		t.Errorf("nil EpisodeInfo should not match")
	}
}

func TestRegexSpec_InvalidPattern(t *testing.T) {
	r := &RegexSpec{}
	err := r.SetPattern(`(unclosed`)
	if err == nil {
		t.Errorf("expected compile error on invalid pattern")
	}
	if r.re != nil {
		t.Errorf("re should be nil after failed compile")
	}
}

func TestRegexSpec_EmptyPattern(t *testing.T) {
	r := &RegexSpec{}
	if err := r.SetPattern(""); err != nil {
		t.Errorf("empty pattern should not error")
	}
	if r.matchString("anything") {
		t.Errorf("empty pattern should not match anything")
	}
}

func TestRegexSpec_CaseInsensitive(t *testing.T) {
	r := &RegexSpec{}
	if err := r.SetPattern("hdtv"); err != nil {
		t.Fatal(err)
	}
	if !r.matchString("Series.S01E05.HDTV.x264") {
		t.Errorf("pattern should match case-insensitively")
	}
}

func TestSpecOrder(t *testing.T) {
	if (&ReleaseTitleSpec{}).Order() != 1 {
		t.Errorf("ReleaseTitleSpec.Order() should be 1")
	}
	if (&ReleaseGroupSpec{}).Order() != 9 {
		t.Errorf("ReleaseGroupSpec.Order() should be 9")
	}
}

func TestSpecImplementationName(t *testing.T) {
	if (&ReleaseTitleSpec{}).ImplementationName() != "Release Title" {
		t.Errorf("ReleaseTitleSpec name mismatch")
	}
	if (&ReleaseGroupSpec{}).ImplementationName() != "Release Group" {
		t.Errorf("ReleaseGroupSpec name mismatch")
	}
}

// TestSpecificationInterfaceConformance verifies both typed specs
// satisfy the Specification interface — a compile-time guard
// against breaking interface conformance silently.
func TestSpecificationInterfaceConformance(t *testing.T) {
	var _ Specification = &ReleaseTitleSpec{}
	var _ Specification = &ReleaseGroupSpec{}
}
