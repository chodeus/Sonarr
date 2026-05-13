package customformats_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/series"
)

func TestParseCustomFormatForHistory_NilEntry(t *testing.T) {
	t.Parallel()
	got := customformats.ParseCustomFormatForHistory(nil, &series.Series{ID: 1, Title: "X"}, nil)
	if got != nil {
		t.Errorf("nil entry should return nil, got %+v", got)
	}
}

func TestParseCustomFormatForHistory_EmptyFormats(t *testing.T) {
	t.Parallel()
	entry := &history.Entry{SeriesID: 7, SourceTitle: "Show.S01E01.720p.WEB-DL"}
	got := customformats.ParseCustomFormatForHistory(entry, &series.Series{ID: 7, Title: "Show"}, nil)
	if len(got) != 0 {
		t.Errorf("empty formats returned %d matches, want 0", len(got))
	}
}

func TestParseCustomFormatForHistory_MatchesAgainstReleaseTitle(t *testing.T) {
	t.Parallel()
	entry := &history.Entry{
		SeriesID:    7,
		SourceTitle: "Show.S01E01.720p.WEB-DL-RELGRP",
	}
	titleSpec := &customformats.ReleaseTitleSpec{}
	if err := titleSpec.SetPattern("720p"); err != nil {
		t.Fatalf("SetPattern: %v", err)
	}
	formats := []*customformats.Format{{
		ID:             1,
		Name:           "Has720p",
		Specifications: []customformats.Specification{titleSpec},
	}}
	got := customformats.ParseCustomFormatForHistory(entry, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 1 || got[0].Name != "Has720p" {
		t.Fatalf("matched format = %+v, want Has720p", got)
	}
}

func TestParseCustomFormatForHistory_DataSizeReadFromMap(t *testing.T) {
	t.Parallel()
	entry := &history.Entry{
		SeriesID:    7,
		SourceTitle: "Show.S01E01.720p.WEB-DL",
		Data:        map[string]string{"size": "1500000000"}, // 1.4 GiB
	}
	sizeSpec := &customformats.SizeSpec{Min: 0.5, Max: 2.0}
	formats := []*customformats.Format{{
		ID:             1,
		Name:           "SizeBand",
		Specifications: []customformats.Specification{sizeSpec},
	}}
	got := customformats.ParseCustomFormatForHistory(entry, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 1 {
		t.Errorf("expected 1 match (Data.size threaded through); got %+v", got)
	}
}

func TestParseCustomFormatForHistory_DataMalformedSizeDegradesToZero(t *testing.T) {
	t.Parallel()
	entry := &history.Entry{
		SeriesID:    7,
		SourceTitle: "Show.S01E01.720p.WEB-DL",
		Data:        map[string]string{"size": "not-an-int"},
	}
	// Use SizeSpec gating 0.5-2.0 GB. With size=0 the spec rejects
	// (0 > 0.5GB is false), so no match.
	sizeSpec := &customformats.SizeSpec{Min: 0.5, Max: 2.0}
	formats := []*customformats.Format{{
		ID:             1,
		Name:           "SizeBand",
		Specifications: []customformats.Specification{sizeSpec},
	}}
	got := customformats.ParseCustomFormatForHistory(entry, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 0 {
		t.Errorf("malformed size should degrade to 0 (no match against >0.5GB band); got %+v", got)
	}
}

func TestParseCustomFormatForHistory_NilDataNoPanic(t *testing.T) {
	t.Parallel()
	entry := &history.Entry{
		SeriesID:    7,
		SourceTitle: "Show.S01E01.720p.WEB-DL",
		Data:        nil,
	}
	// No panic + reads no data fields cleanly.
	_ = customformats.ParseCustomFormatForHistory(entry, &series.Series{ID: 7, Title: "Show"}, nil)
}

func TestParseCustomFormatForHistory_IndexerFlagsParsedAsInt(t *testing.T) {
	t.Parallel()
	// IndexerFlagSpec checks for a specific flag bit.
	// Build entry with Data.indexerFlags set to a known int value.
	entry := &history.Entry{
		SeriesID:    7,
		SourceTitle: "Show.S01E01.720p.WEB-DL",
		Data:        map[string]string{"indexerFlags": "1"}, // bit 0
	}
	flagSpec := &customformats.IndexerFlagSpec{Value: 1}
	formats := []*customformats.Format{{
		ID:             1,
		Name:           "FlagSet",
		Specifications: []customformats.Specification{flagSpec},
	}}
	got := customformats.ParseCustomFormatForHistory(entry, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 1 {
		t.Errorf("expected 1 match (Data.indexerFlags=1 should satisfy IndexerFlagSpec); got %+v", got)
	}
}
