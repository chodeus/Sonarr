package parser_test

import (
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

// TestParsePath ports the C# PathParserFixture cases that DON'T
// require strategy 2 or 3 (season-folder + simple-episode-number).
// Those land in slice 2.13.4b once SeasonFolderRegex and
// SimpleEpisodeNumberRegex are ported.
//
// Path inputs use `filepath.Join` to stay platform-portable.
func TestParsePath(t *testing.T) {
	cases := []struct {
		name        string
		segments    []string
		wantSeason  int
		wantEpisode int
	}{
		{
			// Strategy 1: filename alone parses.
			name:        "filename has S-E",
			segments:    []string{"z:", "tv shows", "series title (2003)", "Season 3", "S03E05 - Title.mkv"},
			wantSeason:  3,
			wantEpisode: 5,
		},
		{
			name:        "filename has S-E in Specials folder",
			segments:    []string{"z:", "tv shows", "series title", "Specials", "S00E16 - Dear Title - SD TV.avi"},
			wantSeason:  0,
			wantEpisode: 16,
		},
		{
			name:        "filename has Nx-E shorthand",
			segments:    []string{"C:", "Test", "TV", "Series.4x05.HDTV.XviD-LOL"},
			wantSeason:  4,
			wantEpisode: 5,
		},
		{
			// Strategy 4: filename hash → fall back to dir + filename.
			name:        "hash filename, dir has S-E",
			segments:    []string{"E:", "Downloads", "tv", "Series.Title.S01E01.720p.HDTV", "ajifajjjeaeaeqwer_eppj.avi"},
			wantSeason:  1,
			wantEpisode: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tc.segments...)
			got := parser.ParsePath(path)
			if got == nil {
				t.Fatalf("ParsePath(%q) = nil, want a result", path)
			}
			if got.SeasonNumber != tc.wantSeason {
				t.Errorf("SeasonNumber = %d, want %d", got.SeasonNumber, tc.wantSeason)
			}
			if len(got.EpisodeNumbers) != 1 || got.EpisodeNumbers[0] != tc.wantEpisode {
				t.Errorf("EpisodeNumbers = %v, want [%d]", got.EpisodeNumbers, tc.wantEpisode)
			}
		})
	}
}

// TestParsePath_FallsBackToDirAndExt covers strategy 5: filename
// produces no result AND dir+filename produces no result, but
// dir+ext does. Synthesised input where the filename is a non-
// parseable hash and dir is a clean S-E + extension.
func TestParsePath_FallsBackToDirAndExt(t *testing.T) {
	path := filepath.Join("Series.Title.S05E07.720p.HDTV.X264-LOL", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef.mkv")
	got := parser.ParsePath(path)
	if got == nil {
		t.Fatalf("ParsePath(%q) = nil, want a result", path)
	}
	if got.SeasonNumber != 5 || len(got.EpisodeNumbers) != 1 || got.EpisodeNumbers[0] != 7 {
		t.Errorf("got season=%d episodes=%v, want season=5 episodes=[7]",
			got.SeasonNumber, got.EpisodeNumbers)
	}
}
