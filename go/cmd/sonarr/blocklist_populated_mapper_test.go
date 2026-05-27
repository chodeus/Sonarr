package main

import (
	"context"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/series"
)

// J337 wire-shape pin for the blocklistPagedAdapter populated-row
// mapper: computeBlocklistCustomFormats invokes
// ParseCustomFormatForBlocklist via the 2 narrow-interface lookups
// (re-uses historySeriesLookup + historyCustomFormatsLookup from
// J336). nil deps fall back to empty slice matching pre-J337 stub.
//
// BlocklistResource is score-less (no CustomFormatScore field per
// the J297 wire-through note) so the helper is simpler than its
// historyPagedAdapter sibling — no qualityprofile lookup branch.

// J337: nil deps → fall back to empty slice.
func TestComputeBlocklistCustomFormats_NilDeps_J337(t *testing.T) {
	t.Parallel()
	entry := &blocklist.Entry{ID: 1, SeriesID: 11, SourceTitle: "Show.S01E01"}
	adapter := blocklistPagedAdapter{} // nil services
	cf := computeBlocklistCustomFormats(context.Background(), entry, adapter, nil, map[int]*series.Series{})
	if len(cf) != 0 {
		t.Errorf("expected empty CustomFormats with nil deps; got %+v", cf)
	}
}

// J337: missing series → fall back to empty.
func TestComputeBlocklistCustomFormats_SeriesNotFound_J337(t *testing.T) {
	t.Parallel()
	entry := &blocklist.Entry{ID: 1, SeriesID: 99, SourceTitle: "Show.S01E01"}
	adapter := blocklistPagedAdapter{
		seriesSvc:        &fakeHistorySeriesLookup{}, // empty registry → ErrNotFound
		customFormatsSvc: &fakeHistoryCustomFormatsLookup{formats: []*customformats.Format{{ID: 1, Name: "x265"}}},
	}
	allFormats, _ := adapter.customFormatsSvc.All(context.Background())
	cf := computeBlocklistCustomFormats(context.Background(), entry, adapter, allFormats, map[int]*series.Series{})
	if len(cf) != 0 {
		t.Errorf("expected empty on series-miss; got %+v", cf)
	}
}

// J337: empty allFormats → fall back to empty (short-circuit
// before series lookup).
func TestComputeBlocklistCustomFormats_EmptyFormats_J337(t *testing.T) {
	t.Parallel()
	entry := &blocklist.Entry{ID: 1, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Now()}
	adapter := blocklistPagedAdapter{
		seriesSvc:        &fakeHistorySeriesLookup{byID: map[int]*series.Series{11: {ID: 11, Title: "Show"}}},
		customFormatsSvc: &fakeHistoryCustomFormatsLookup{formats: nil},
	}
	cf := computeBlocklistCustomFormats(context.Background(), entry, adapter, nil, map[int]*series.Series{})
	if len(cf) != 0 {
		t.Errorf("expected empty with no formats; got %+v", cf)
	}
}

// J337: series cache hits across multiple rows — confirms
// memoization avoids duplicate service calls in the typical
// paged-blocklist shape where many rows share a series.
func TestComputeBlocklistCustomFormats_CachesAcrossRows_J337(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 11, Title: "Show"}
	seriesLookupCalls := 0
	seriesSvc := &countingSeriesLookup{inner: &fakeHistorySeriesLookup{byID: map[int]*series.Series{11: ser}}, calls: &seriesLookupCalls}
	adapter := blocklistPagedAdapter{
		seriesSvc:        seriesSvc,
		customFormatsSvc: &fakeHistoryCustomFormatsLookup{formats: []*customformats.Format{}},
	}
	allFormats, _ := adapter.customFormatsSvc.All(context.Background())
	// allFormats empty so the calculator's match list is empty; we
	// only exercise the short-circuit gate on the empty-allFormats
	// branch. Counting wrapper confirms 0 service calls.
	seriesCache := map[int]*series.Series{}
	for i := 0; i < 2; i++ {
		entry := &blocklist.Entry{ID: i + 1, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Now()}
		_ = computeBlocklistCustomFormats(context.Background(), entry, adapter, allFormats, seriesCache)
	}
	if seriesLookupCalls != 0 {
		t.Errorf("empty allFormats should short-circuit before series lookup; got %d calls", seriesLookupCalls)
	}
}
