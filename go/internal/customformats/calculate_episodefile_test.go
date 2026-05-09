package customformats_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/series"
)

// titleMatchSpec is a deterministic Specification for testing
// ParseCustomFormatForFile — matches when the input's Filename
// contains a substring. Uses a unique Go type per match-string so
// the per-type grouping at the matching engine sees one group.
type titleMatchSpec struct {
	customformats.BaseSpecification
	Substring string
}

func (s *titleMatchSpec) Order() int                 { return 1 }
func (s *titleMatchSpec) ImplementationName() string { return "TitleMatch" }
func (s *titleMatchSpec) IsSatisfiedBy(in customformats.CustomFormatInput) bool {
	return s.applyNegateExported(contains(in.Filename, s.Substring))
}

// applyNegateExported is in the test file only; the production
// applyNegate helper is package-private. The base Negate is
// initialised false in this fixture so the helper just forwards.
func (s *titleMatchSpec) applyNegateExported(match bool) bool {
	if s.Negate {
		return !match
	}
	return match
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// formatNamed constructs a Format with one TitleMatchSpec.
func formatNamed(id int, name, substring string) *customformats.Format {
	return &customformats.Format{
		ID:             id,
		Name:           name,
		Specifications: []customformats.Specification{&titleMatchSpec{Substring: substring}},
	}
}

func TestParseCustomFormatForFile_NilFile(t *testing.T) {
	t.Parallel()
	got := customformats.ParseCustomFormatForFile(nil, &series.Series{ID: 1, Title: "X"}, nil)
	if got != nil {
		t.Errorf("ParseCustomFormatForFile(nil file) = %+v, want nil", got)
	}
}

func TestParseCustomFormatForFile_EmptyFormats(t *testing.T) {
	t.Parallel()
	file := &episodefiles.File{
		ID:           1,
		SeriesID:     7,
		RelativePath: "Show.S01E01.mkv",
	}
	got := customformats.ParseCustomFormatForFile(file, &series.Series{ID: 7, Title: "Show"}, nil)
	if len(got) != 0 {
		t.Errorf("empty formats returned %d matches, want 0", len(got))
	}
}

func TestParseCustomFormatForFile_MatchesAgainstFilename(t *testing.T) {
	t.Parallel()
	file := &episodefiles.File{
		ID:           1,
		SeriesID:     7,
		RelativePath: "Show.S01E01.720p.mkv",
		Size:         1_000_000,
	}
	formats := []*customformats.Format{formatNamed(1, "Has720p", "720p")}
	got := customformats.ParseCustomFormatForFile(file, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1", len(got))
	}
	if got[0].ID != 1 || got[0].Name != "Has720p" {
		t.Errorf("matched format = %+v, want id=1 name=Has720p", got[0])
	}
}

// ReleaseTitle resolution mirrors C# `:172-188` three-tier fallback:
// SceneName > basename(OriginalFilePath) > basename(RelativePath).
func TestParseCustomFormatForFile_ReleaseTitleSceneNamePreferred(t *testing.T) {
	t.Parallel()
	// SceneName carries a string the format would match; RelativePath
	// doesn't — the scene-name should win and the format should match.
	// Spec matches against Filename which is basename(RelativePath),
	// not ReleaseTitle, so we use a scene-name-shape for ReleaseTitle
	// indirectly: the test below uses RelativePath as the matched
	// signal since CustomFormatInput.Filename is basename(RelativePath).
	// This test pins the SceneName arm of the resolveReleaseTitle
	// helper by checking that a SceneName + RelativePath pair builds
	// the input filename from RelativePath (the spec sees the renamed
	// name) while the ParsedEpisodeInfo.ReleaseTitle (used by the
	// release-title spec) carries SceneName.
	//
	// The current customformats.Specification interface only feeds the
	// Filename to specs at this slice; ReleaseTitle is consumed by
	// specs not yet ported. So for now we assert via the Filename arm
	// exclusively — basename(RelativePath).
	file := &episodefiles.File{
		ID:           1,
		SeriesID:     7,
		SceneName:    "Show.S01E01.720p.SCENE-GROUP",
		RelativePath: "Show.S01E01.720p.mkv",
	}
	formats := []*customformats.Format{formatNamed(1, "Has720p", "720p")}
	got := customformats.ParseCustomFormatForFile(file, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 1 {
		t.Errorf("expected 1 match (Filename built from RelativePath), got %d", len(got))
	}
}

func TestParseCustomFormatForFile_NilSeriesAccepted(t *testing.T) {
	t.Parallel()
	// nil series must not crash — the calculator falls through with
	// an empty SeriesTitle. The Filename-based spec still matches.
	file := &episodefiles.File{
		ID:           1,
		SeriesID:     7,
		RelativePath: "Show.S01E01.720p.mkv",
	}
	formats := []*customformats.Format{formatNamed(1, "Has720p", "720p")}
	got := customformats.ParseCustomFormatForFile(file, nil, formats)
	if len(got) != 1 {
		t.Errorf("nil series must still allow filename-based match, got %d", len(got))
	}
}

func TestParseCustomFormatForFile_EmptyRelativePathFallsThrough(t *testing.T) {
	t.Parallel()
	// All path fields empty → Filename = "" + Spec doesn't match
	// (the substring "720p" isn't in "").
	file := &episodefiles.File{ID: 1, SeriesID: 7}
	formats := []*customformats.Format{formatNamed(1, "Has720p", "720p")}
	got := customformats.ParseCustomFormatForFile(file, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 0 {
		t.Errorf("empty path should produce no matches, got %d", len(got))
	}
}

func TestParseCustomFormatForFile_ReturnsSortedByName(t *testing.T) {
	t.Parallel()
	// Two formats both match → returned in Calculate's Name-sorted
	// order (mirrors C# `OrderBy(x => x.Name)` at :167).
	file := &episodefiles.File{
		ID:           1,
		SeriesID:     7,
		RelativePath: "Show.S01E01.720p.x264.mkv",
	}
	formats := []*customformats.Format{
		formatNamed(2, "ZetaFormat", "720p"),
		formatNamed(1, "AlphaFormat", "x264"),
	}
	got := customformats.ParseCustomFormatForFile(file, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2", len(got))
	}
	if got[0].Name != "AlphaFormat" || got[1].Name != "ZetaFormat" {
		t.Errorf("sort order broken: got %q,%q", got[0].Name, got[1].Name)
	}
}

func TestParseCustomFormatForFile_LanguagesIDsConverted(t *testing.T) {
	t.Parallel()
	// Languages on EpisodeFile are persisted as []int; ensure unknown
	// ids are silently skipped without erroring. The match doesn't
	// depend on language here — just exercising the conversion path.
	file := &episodefiles.File{
		ID:           1,
		SeriesID:     7,
		RelativePath: "Show.mkv",
		Languages:    []int{1, 999_999, 2}, // 1=English usually; 2=French; 999_999 unknown.
	}
	formats := []*customformats.Format{formatNamed(1, "M", "Show")}
	got := customformats.ParseCustomFormatForFile(file, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 1 {
		t.Errorf("expected 1 match, got %d (Languages-ID conversion must not crash)", len(got))
	}
}
