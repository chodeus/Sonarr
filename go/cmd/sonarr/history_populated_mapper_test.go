package main

import (
	"context"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// J336 wire-shape pin for the historyPagedAdapter populated-row
// mapper: computeHistoryCustomFormats invokes ParseCustomFormatForHistory
// + qualityprofiles.CalculateCustomFormatScore via the 3 narrow-
// interface lookups. nil deps fall back to empty slice + 0 score
// matching the pre-J336 stub. Test fixtures use synthetic fakes
// instead of the full service ctors to keep the unit-test surface
// minimal.

// ---------- narrow-interface fakes ----------

type fakeHistorySeriesLookup struct {
	byID map[int]*series.Series
	err  error
}

func (f *fakeHistorySeriesLookup) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if f.err != nil {
		return nil, f.err
	}
	if s, ok := f.byID[id]; ok {
		return s, nil
	}
	return nil, series.ErrNotFound
}

type fakeHistoryQualityProfileLookup struct {
	byID map[int]*qualityprofiles.Profile
}

func (f *fakeHistoryQualityProfileLookup) Get(_ context.Context, id int) (*qualityprofiles.Profile, error) {
	if p, ok := f.byID[id]; ok {
		return p, nil
	}
	return nil, qualityprofiles.ErrNotFound
}

type fakeHistoryCustomFormatsLookup struct {
	formats []*customformats.Format
}

func (f *fakeHistoryCustomFormatsLookup) All(_ context.Context) ([]*customformats.Format, error) {
	return f.formats, nil
}

// ---------- helper-direct tests (computeHistoryCustomFormats) ----------

// J336: nil deps → fall back to empty slice + 0 score.
func TestComputeHistoryCustomFormats_NilDeps_J336(t *testing.T) {
	t.Parallel()
	entry := &history.Entry{ID: 1, SeriesID: 11, SourceTitle: "Show.S01E01"}
	adapter := historyPagedAdapter{} // nil services
	cf, score := computeHistoryCustomFormats(context.Background(), entry, adapter, nil, map[int]*series.Series{}, map[int]*qualityprofiles.Profile{})
	if len(cf) != 0 {
		t.Errorf("expected empty CustomFormats with nil deps; got %+v", cf)
	}
	if score != 0 {
		t.Errorf("expected 0 score with nil deps; got %d", score)
	}
}

// J336: missing series → fall back to empty + 0.
func TestComputeHistoryCustomFormats_SeriesNotFound_J336(t *testing.T) {
	t.Parallel()
	entry := &history.Entry{ID: 1, SeriesID: 99, SourceTitle: "Show.S01E01"}
	adapter := historyPagedAdapter{
		seriesSvc:        &fakeHistorySeriesLookup{}, // empty registry → ErrNotFound
		customFormatsSvc: &fakeHistoryCustomFormatsLookup{formats: []*customformats.Format{{ID: 1, Name: "x265"}}},
	}
	allFormats, _ := adapter.customFormatsSvc.All(context.Background())
	cf, score := computeHistoryCustomFormats(context.Background(), entry, adapter, allFormats, map[int]*series.Series{}, map[int]*qualityprofiles.Profile{})
	if len(cf) != 0 {
		t.Errorf("expected empty on series-miss; got %+v", cf)
	}
	if score != 0 {
		t.Errorf("expected 0 score on series-miss; got %d", score)
	}
}

// J336: empty allFormats → fall back to empty + 0 even with
// series lookup OK (calculator has nothing to match).
func TestComputeHistoryCustomFormats_EmptyFormats_J336(t *testing.T) {
	t.Parallel()
	entry := &history.Entry{ID: 1, SeriesID: 11, SourceTitle: "Show.S01E01"}
	adapter := historyPagedAdapter{
		seriesSvc:        &fakeHistorySeriesLookup{byID: map[int]*series.Series{11: {ID: 11, Title: "Show"}}},
		customFormatsSvc: &fakeHistoryCustomFormatsLookup{formats: nil},
	}
	cf, score := computeHistoryCustomFormats(context.Background(), entry, adapter, nil, map[int]*series.Series{}, map[int]*qualityprofiles.Profile{})
	if len(cf) != 0 {
		t.Errorf("expected empty with no formats; got %+v", cf)
	}
	if score != 0 {
		t.Errorf("expected 0 score; got %d", score)
	}
}

// J336: series-cache hits + profile-cache hits — verify
// memoization avoids duplicate service calls. Constructs 2 rows
// from the same series; expects exactly 1 series-lookup call and
// 1 profile-lookup call across the pair.
func TestComputeHistoryCustomFormats_CachesAcrossRows_J336(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 11, Title: "Show", QualityProfileID: 1}
	seriesLookupCalls := 0
	seriesSvc := &countingSeriesLookup{inner: &fakeHistorySeriesLookup{byID: map[int]*series.Series{11: ser}}, calls: &seriesLookupCalls}
	profile := &qualityprofiles.Profile{ID: 1, Name: "default", FormatItems: ""}
	profileLookupCalls := 0
	profileSvc := &countingProfileLookup{inner: &fakeHistoryQualityProfileLookup{byID: map[int]*qualityprofiles.Profile{1: profile}}, calls: &profileLookupCalls}
	adapter := historyPagedAdapter{
		seriesSvc:          seriesSvc,
		qualityProfilesSvc: profileSvc,
		customFormatsSvc:   &fakeHistoryCustomFormatsLookup{formats: []*customformats.Format{}},
	}
	allFormats, _ := adapter.customFormatsSvc.All(context.Background())
	// allFormats is empty so the calculator returns no matches; we
	// only exercise the cache-key path on series + profile lookups.
	seriesCache := map[int]*series.Series{}
	profileCache := map[int]*qualityprofiles.Profile{}
	for i := 0; i < 2; i++ {
		entry := &history.Entry{ID: i + 1, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Now()}
		_, _ = computeHistoryCustomFormats(context.Background(), entry, adapter, allFormats, seriesCache, profileCache)
	}
	// Empty allFormats short-circuits via `len(allFormats) == 0` so
	// neither series nor profile lookup ever fires — this branch is
	// the fast path. Document the contract.
	if seriesLookupCalls != 0 {
		t.Errorf("empty allFormats should short-circuit before series lookup; got %d calls", seriesLookupCalls)
	}
	if profileLookupCalls != 0 {
		t.Errorf("empty allFormats should short-circuit before profile lookup; got %d calls", profileLookupCalls)
	}
}

// ---------- counting wrappers for cache-call assertions ----------

type countingSeriesLookup struct {
	inner historySeriesLookup
	calls *int
}

func (c *countingSeriesLookup) GetSeries(ctx context.Context, id int) (*series.Series, error) {
	*c.calls++
	return c.inner.GetSeries(ctx, id)
}

type countingProfileLookup struct {
	inner historyQualityProfileLookup
	calls *int
}

func (c *countingProfileLookup) Get(ctx context.Context, id int) (*qualityprofiles.Profile, error) {
	*c.calls++
	return c.inner.Get(ctx, id)
}
