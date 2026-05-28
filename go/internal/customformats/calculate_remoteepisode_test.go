package customformats_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

func TestParseCustomFormatForRemoteEpisode_NilParsedInfo(t *testing.T) {
	t.Parallel()
	got := customformats.ParseCustomFormatForRemoteEpisode(nil, &series.Series{ID: 1, Title: "X"}, nil, 0, 0, nil)
	if got != nil {
		t.Errorf("nil parsedInfo should return nil, got %+v", got)
	}
}

func TestParseCustomFormatForRemoteEpisode_EmptyFormats(t *testing.T) {
	t.Parallel()
	pi := &parser.ParsedEpisodeInfo{ReleaseTitle: "Show.S01E01.720p.WEB-DL"}
	got := customformats.ParseCustomFormatForRemoteEpisode(pi, &series.Series{ID: 1, Title: "X"}, nil, 0, 0, nil)
	if got != nil {
		t.Errorf("empty formats should return nil, got %+v", got)
	}
}

func TestParseCustomFormatForRemoteEpisode_MatchesReleaseTitleSpec(t *testing.T) {
	t.Parallel()
	pi := &parser.ParsedEpisodeInfo{
		ReleaseTitle: "Show.S01E01.720p.WEB-DL",
		SeriesTitle:  "Show",
	}
	titleSpec := &customformats.ReleaseTitleSpec{}
	if err := titleSpec.SetPattern("720p"); err != nil {
		t.Fatalf("SetPattern: %v", err)
	}
	formats := []*customformats.Format{{
		ID:             5,
		Name:           "Has720p",
		Specifications: []customformats.Specification{titleSpec},
	}}
	got := customformats.ParseCustomFormatForRemoteEpisode(pi, &series.Series{ID: 1, Title: "Show"}, nil, 0, 0, formats)
	if len(got) != 1 || got[0].Name != "Has720p" {
		t.Errorf("matched format = %+v, want Has720p", got)
	}
}

func TestParseCustomFormatForRemoteEpisode_SizeThreadsThrough(t *testing.T) {
	t.Parallel()
	pi := &parser.ParsedEpisodeInfo{ReleaseTitle: "Show.S01E01"}
	sizeSpec := &customformats.SizeSpec{Min: 0.5, Max: 2.0}
	formats := []*customformats.Format{{
		ID:             7,
		Name:           "SizeBand",
		Specifications: []customformats.Specification{sizeSpec},
	}}
	got := customformats.ParseCustomFormatForRemoteEpisode(pi, &series.Series{ID: 1, Title: "X"}, nil, 0, 1_500_000_000, formats)
	if len(got) != 1 {
		t.Errorf("expected size threaded through to spec; got %+v", got)
	}
}
