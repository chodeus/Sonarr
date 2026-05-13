package customformats_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/series"
)

func TestParseCustomFormatForBlocklist_NilEntry(t *testing.T) {
	t.Parallel()
	got := customformats.ParseCustomFormatForBlocklist(nil, &series.Series{ID: 1, Title: "X"}, nil)
	if got != nil {
		t.Errorf("nil entry should return nil, got %+v", got)
	}
}

func TestParseCustomFormatForBlocklist_EmptyFormats(t *testing.T) {
	t.Parallel()
	entry := &blocklist.Entry{SeriesID: 7, SourceTitle: "Show.S01E01.720p.WEB-DL"}
	got := customformats.ParseCustomFormatForBlocklist(entry, &series.Series{ID: 7, Title: "Show"}, nil)
	if len(got) != 0 {
		t.Errorf("empty formats returned %d matches, want 0", len(got))
	}
}

func TestParseCustomFormatForBlocklist_MatchesAgainstReleaseTitle(t *testing.T) {
	t.Parallel()
	entry := &blocklist.Entry{
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
	got := customformats.ParseCustomFormatForBlocklist(entry, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 1 || got[0].Name != "Has720p" {
		t.Fatalf("matched format = %+v, want Has720p", got)
	}
}

func TestParseCustomFormatForBlocklist_NilSizeDegradesToZero(t *testing.T) {
	t.Parallel()
	entry := &blocklist.Entry{
		SeriesID:    7,
		SourceTitle: "Show.S01E01.720p.WEB-DL",
		Size:        nil,
	}
	// Test runs without panic — nil Size has to degrade cleanly.
	_ = customformats.ParseCustomFormatForBlocklist(entry, &series.Series{ID: 7, Title: "Show"}, nil)
}

func TestParseCustomFormatForBlocklist_SizeProvidedThreaded(t *testing.T) {
	t.Parallel()
	size := int64(1_500_000_000)
	entry := &blocklist.Entry{
		SeriesID:    7,
		SourceTitle: "Show.S01E01.720p.WEB-DL",
		Size:        &size,
	}
	// Size threaded through to CustomFormatInput — exercise via a
	// SizeSpec consuming the same input. Use the actual SizeSpec
	// implementation from the package to verify wire-through.
	sizeSpec := &customformats.SizeSpec{}
	sizeSpec.Min = 0.5 // 0.5 GB → 500_000_000 bytes
	sizeSpec.Max = 2.0 // 2.0 GB → 2_000_000_000 bytes
	formats := []*customformats.Format{{
		ID:             1,
		Name:           "SizeBand",
		Specifications: []customformats.Specification{sizeSpec},
	}}
	got := customformats.ParseCustomFormatForBlocklist(entry, &series.Series{ID: 7, Title: "Show"}, formats)
	if len(got) != 1 {
		t.Errorf("size 1.5GB should fall within 0.5-2GB band; got %+v", got)
	}
}
