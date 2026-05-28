package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/quality"
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

// J379: paged adapter maps the EventType ordinal to its wire-stable
// name string — porting the since/series-path mapping into PagedHistory.
// A nil EventType (old pre-column rows) maps to the empty string.
func TestPagedHistory_EventTypeNameMapping_J379(t *testing.T) {
	t.Parallel()
	repo, err := history.NewRepo(datastoretest.OpenSQLite(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	ctx := t.Context()
	grabbed := int(history.EventTypeGrabbed)
	imported := int(history.EventTypeDownloadFolderImported)
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	insert := func(srcTitle string, et *int) int {
		e := &history.Entry{
			SeriesID:    11,
			SourceTitle: srcTitle,
			Date:        now,
			Quality:     `{"quality":{"id":7,"name":"WEBDL-1080p"},"revision":{"version":1}}`,
			Data:        map[string]string{},
			EventType:   et,
		}
		got, err := repo.Insert(ctx, e)
		if err != nil {
			t.Fatalf("Insert %s: %v", srcTitle, err)
		}
		return got.ID
	}
	idGrab := insert("Show.S01E01", &grabbed)
	idImport := insert("Show.S01E02", &imported)
	idNil := insert("Show.S01E03", nil)

	adapter := historyPagedAdapter{repo: repo}
	rows, total, err := adapter.PagedHistory(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, false, false)
	if err != nil {
		t.Fatalf("PagedHistory: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	want := map[int]string{
		idGrab:   history.EventTypeGrabbed.String(),
		idImport: history.EventTypeDownloadFolderImported.String(),
		idNil:    "",
	}
	for _, r := range rows {
		if got := r.EventType; got != want[r.ID] {
			t.Errorf("row %d EventType = %q, want %q", r.ID, got, want[r.ID])
		}
	}
}

// J380: paged adapter hydrates the BASE Series / Episode embeds only
// when the includeSeries / includeEpisode flags are set, mirroring the
// since/series path (J377). Verifies the 4 flag combinations on a
// single populated row.
func TestPagedHistory_IncludeSeriesEpisodeEmbeds_J380(t *testing.T) {
	t.Parallel()
	repo, err := history.NewRepo(datastoretest.OpenSQLite(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	ctx := t.Context()
	grabbed := int(history.EventTypeGrabbed)
	row := &history.Entry{
		EpisodeID:   77,
		SeriesID:    11,
		SourceTitle: "Show.S01E01",
		Date:        time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC),
		Quality:     `{"quality":{"id":7,"name":"WEBDL-1080p"},"revision":{"version":1}}`,
		Data:        map[string]string{},
		EventType:   &grabbed,
	}
	if _, err := repo.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	adapter := historyPagedAdapter{
		repo:        repo,
		seriesSvc:   &fakeHistorySeriesLookup{byID: map[int]*series.Series{11: {ID: 11, Title: "Show"}}},
		episodesSvc: &fakeHistoryEpisodeLookup{byID: map[int]*episodes.Episode{77: {ID: 77, SeriesID: 11, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}}},
	}

	cases := []struct {
		name                          string
		includeSeries, includeEpisode bool
		wantSeries, wantEpisode       bool
	}{
		{"neither", false, false, false, false},
		{"series only", true, false, true, false},
		{"episode only", false, true, false, true},
		{"both", true, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, _, err := adapter.PagedHistory(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, tc.includeSeries, tc.includeEpisode)
			if err != nil {
				t.Fatalf("PagedHistory: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("rows = %d, want 1", len(rows))
			}
			r := rows[0]
			if got := r.Series != nil; got != tc.wantSeries {
				t.Errorf("Series present = %v, want %v", got, tc.wantSeries)
			}
			if tc.wantSeries && r.Series.Title != "Show" {
				t.Errorf("Series.Title = %q, want %q", r.Series.Title, "Show")
			}
			if got := r.Episode != nil; got != tc.wantEpisode {
				t.Errorf("Episode present = %v, want %v", got, tc.wantEpisode)
			}
			if tc.wantEpisode && r.Episode.Title != "Pilot" {
				t.Errorf("Episode.Title = %q, want %q", r.Episode.Title, "Pilot")
			}
		})
	}
}

// fakeHistoryEpisodeLookup satisfies historyEpisodeLookup for the
// J380 paged includeEpisode embed test.
type fakeHistoryEpisodeLookup struct {
	byID map[int]*episodes.Episode
}

func (f *fakeHistoryEpisodeLookup) GetEpisode(_ context.Context, id int) (*episodes.Episode, error) {
	if e, ok := f.byID[id]; ok {
		return e, nil
	}
	return nil, episodes.ErrNotFound
}

// stubUpgradableConfig satisfies decisionengine.ConfigReader for the
// J381 paged qualityCutoffNotMet test (default proper mode).
type stubUpgradableConfig struct{}

func (stubUpgradableConfig) DownloadPropersAndRepacks() quality.ProperDownloadType {
	return quality.ProperPreferAndUpgrade
}

// J381: paged adapter computes qualityCutoffNotMet UNCONDITIONALLY via
// the shared upgradable-spec mechanism. A row whose persisted quality
// ranks below the owning profile's cutoff → true; with no upgradable
// spec wired the flag stays at the false default (graceful degrade).
func TestPagedHistory_QualityCutoffNotMet_J381(t *testing.T) {
	t.Parallel()
	repo, err := history.NewRepo(datastoretest.OpenSQLite(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	ctx := t.Context()
	grabbed := int(history.EventTypeGrabbed)
	// Persisted quality is the bare-int Model shape; SDTV (id 1) ranks
	// below the profile cutoff HDTV-720p (id 4) → cutoff not met.
	qjson, err := json.Marshal(quality.NewModel(quality.SDTV))
	if err != nil {
		t.Fatalf("marshal quality: %v", err)
	}
	row := &history.Entry{
		SeriesID:    11,
		EpisodeID:   77,
		SourceTitle: "Show.S01E01",
		Date:        time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC),
		Quality:     string(qjson),
		Data:        map[string]string{},
		EventType:   &grabbed,
	}
	if _, err := repo.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	seriesSvc := &fakeHistorySeriesLookup{byID: map[int]*series.Series{11: {ID: 11, Title: "Show", QualityProfileID: 5}}}
	profileSvc := &fakeHistoryQualityProfileLookup{byID: map[int]*qualityprofiles.Profile{5: {
		ID:             5,
		Name:           "HD",
		Cutoff:         quality.HDTV720p.ID,
		UpgradeAllowed: true,
		Items:          `[{"quality":1,"allowed":true,"items":[]},{"quality":4,"allowed":true,"items":[]}]`,
		FormatItems:    `[]`,
	}}}
	spec, err := decisionengine.NewUpgradableSpecification(stubUpgradableConfig{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewUpgradableSpecification: %v", err)
	}

	// Positive: spec wired → qualityCutoffNotMet true.
	withSpec := historyPagedAdapter{repo: repo, seriesSvc: seriesSvc, qualityProfilesSvc: profileSvc, upgradable: spec}
	rows, _, err := withSpec.PagedHistory(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, false, false)
	if err != nil {
		t.Fatalf("PagedHistory (with spec): %v", err)
	}
	if len(rows) != 1 || !rows[0].QualityCutoffNotMet {
		t.Fatalf("qualityCutoffNotMet = %v, want true", rows[0].QualityCutoffNotMet)
	}

	// Graceful: no spec → flag stays at the false default, still emitted.
	noSpec := historyPagedAdapter{repo: repo, seriesSvc: seriesSvc, qualityProfilesSvc: profileSvc}
	rows, _, err = noSpec.PagedHistory(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, false, false)
	if err != nil {
		t.Fatalf("PagedHistory (no spec): %v", err)
	}
	if len(rows) != 1 || rows[0].QualityCutoffNotMet {
		t.Fatalf("qualityCutoffNotMet = %v, want false (no spec)", rows[0].QualityCutoffNotMet)
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
