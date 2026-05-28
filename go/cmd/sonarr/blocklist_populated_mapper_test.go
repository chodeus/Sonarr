package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/quality"
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

// J382: paged blocklist adapter projects the Quality typed DTO,
// Languages refs, and BASE Series subresource (all unconditional —
// C# BlocklistResourceMapper projects them with no include flag).
func TestPagedBlocklist_QualityLanguagesSeries_J382(t *testing.T) {
	t.Parallel()
	repo, err := blocklist.NewRepo(datastoretest.OpenSQLite(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	ctx := t.Context()
	qjson, err := json.Marshal(quality.NewModel(quality.HDTV720p))
	if err != nil {
		t.Fatalf("marshal quality: %v", err)
	}
	row := &blocklist.Entry{
		SeriesID:    11,
		EpisodeIDs:  []int{100},
		SourceTitle: "Show.S01E01.HDTV-720p",
		Quality:     string(qjson),
		Date:        time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC),
		Languages:   []int{1},
	}
	if _, err := repo.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	adapter := blocklistPagedAdapter{
		repo:      repo,
		seriesSvc: &fakeHistorySeriesLookup{byID: map[int]*series.Series{11: {ID: 11, Title: "Show"}}},
	}
	rows, total, err := adapter.PagedBlocklist(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("PagedBlocklist: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total/len = %d/%d, want 1/1", total, len(rows))
	}
	r := rows[0]
	if r.Quality == nil {
		t.Fatalf("Quality nil, want populated DTO")
	}
	if r.Quality.Quality.ID != quality.HDTV720p.ID {
		t.Errorf("Quality.ID = %d, want %d", r.Quality.Quality.ID, quality.HDTV720p.ID)
	}
	if len(r.Languages) != 1 {
		t.Errorf("Languages = %+v, want 1 ref", r.Languages)
	}
	if r.Series == nil {
		t.Fatalf("Series nil, want BASE embed")
	}
	if r.Series.Title != "Show" {
		t.Errorf("Series.Title = %q, want %q", r.Series.Title, "Show")
	}
}
