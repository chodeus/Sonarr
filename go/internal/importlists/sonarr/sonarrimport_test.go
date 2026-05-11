package sonarr_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/importlists/sonarr"
)

// fakeStatusRecorder records the (RecordSuccess, RecordFailure)
// calls Fetch should make per the C# `SonarrImport.cs:83-91`
// recording semantics.
type fakeStatusRecorder struct {
	success atomic.Int32
	failure atomic.Int32
}

func (r *fakeStatusRecorder) RecordSuccess(_ context.Context, _ int) error {
	r.success.Add(1)
	return nil
}

func (r *fakeStatusRecorder) RecordFailure(_ context.Context, _ int, _ time.Duration) error {
	r.failure.Add(1)
	return nil
}

func settingsJSON(t *testing.T, s sonarr.Settings) *string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	return &out
}

// happy-path stub server returning two series, one matching every
// filter the test configures.
func stubSeriesServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/series":
			_, _ = w.Write([]byte(body))
		case "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"HD"},{"id":2,"name":"Any"}]`))
		case "/api/v3/tag":
			_, _ = w.Write([]byte(`[{"id":5,"label":"4k"},{"id":7,"label":"anime"}]`))
		case "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/mnt/tv"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newImport(t *testing.T, srvURL string, settings sonarr.Settings, recorder *fakeStatusRecorder, doer *http.Client) (importlists.Provider, *importlists.ImportList) {
	t.Helper()
	settings.BaseURL = srvURL
	settings.APIKey = "k"
	row := &importlists.ImportList{
		ID:             3,
		Name:           "PeerList",
		Implementation: sonarr.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	deps := importlists.Dependencies{
		HTTPClient:    doer,
		StatusService: recorder,
	}
	p, err := sonarr.New(row, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, row
}

// SonarrImport metadata: Name/ListType/MinRefreshInterval pinned.
func TestSonarrImport_Metadata(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{Name: "X", Implementation: sonarr.Implementation}
	p, err := sonarr.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "Sonarr" {
		t.Errorf("Name: got %q want Sonarr", p.Name())
	}
	if p.ListType() != importlists.ListTypeProgram {
		t.Errorf("ListType: got %v want Program", p.ListType())
	}
	if p.MinRefreshInterval() != 5*time.Minute {
		t.Errorf("MinRefreshInterval: got %v want 5m", p.MinRefreshInterval())
	}
	if p.Definition() != row {
		t.Errorf("Definition: pointer not preserved")
	}
}

// Happy path: no filters, two remote series, dedup pass-through,
// ImportListID + ImportList stamped in.
func TestSonarrImport_Fetch_NoFilters(t *testing.T) {
	t.Parallel()
	body := `[
		{"title":"A","tvdbId":1,"qualityProfileId":1,"languageProfileId":1,"rootFolderPath":"/mnt/tv","tags":[5]},
		{"title":"B","tvdbId":2,"qualityProfileId":2,"languageProfileId":2,"rootFolderPath":"/mnt/tv","tags":[7]}
	]`
	srv := stubSeriesServer(t, body)
	defer srv.Close()

	rec := &fakeStatusRecorder{}
	p, row := newImport(t, srv.URL, sonarr.Settings{}, rec, srv.Client())
	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.AnyFailure {
		t.Errorf("AnyFailure: true on success")
	}
	if len(got.Series) != 2 {
		t.Fatalf("Series: got %d want 2", len(got.Series))
	}
	want := []importlists.ItemInfo{
		{TvdbID: 1, Title: "A", ImportListID: row.ID, ImportList: row.Name},
		{TvdbID: 2, Title: "B", ImportListID: row.ID, ImportList: row.Name},
	}
	if !reflect.DeepEqual(got.Series, want) {
		t.Errorf("Fetch:\n got %+v\nwant %+v", got.Series, want)
	}
	if rec.success.Load() != 1 || rec.failure.Load() != 0 {
		t.Errorf("recorder: success=%d failure=%d want 1/0", rec.success.Load(), rec.failure.Load())
	}
}

// Filter cases — one per filter type.
func TestSonarrImport_Fetch_Filters(t *testing.T) {
	t.Parallel()
	body := `[
		{"title":"A","tvdbId":1,"qualityProfileId":1,"languageProfileId":11,"rootFolderPath":"/mnt/tv","tags":[5,7]},
		{"title":"B","tvdbId":2,"qualityProfileId":2,"languageProfileId":22,"rootFolderPath":"/mnt/anime","tags":[7]}
	]`
	cases := []struct {
		name     string
		settings sonarr.Settings
		wantTvdb []int
	}{
		{"ProfileID_1_keeps_A", sonarr.Settings{ProfileIDs: []int{1}}, []int{1}},
		{"ProfileID_2_keeps_B", sonarr.Settings{ProfileIDs: []int{2}}, []int{2}},
		{"LanguageProfileID_11_keeps_A", sonarr.Settings{LanguageProfileIDs: []int{11}}, []int{1}},
		{"TagID_5_keeps_only_A", sonarr.Settings{TagIDs: []int{5}}, []int{1}},
		{"TagID_7_keeps_both", sonarr.Settings{TagIDs: []int{7}}, []int{1, 2}},
		{"RootFolder_mnt_tv_substring_keeps_A", sonarr.Settings{RootFolderPaths: []string{"/mnt/tv"}}, []int{1}},
		{"RootFolder_case_insensitive", sonarr.Settings{RootFolderPaths: []string{"/MNT/ANIME"}}, []int{2}},
		{"all_filters_empty_keeps_both", sonarr.Settings{}, []int{1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := stubSeriesServer(t, body)
			defer srv.Close()
			p, _ := newImport(t, srv.URL, tc.settings, &fakeStatusRecorder{}, srv.Client())
			got, err := p.Fetch(context.Background())
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			gotTvdb := make([]int, len(got.Series))
			for i, s := range got.Series {
				gotTvdb[i] = s.TvdbID
			}
			if !reflect.DeepEqual(gotTvdb, tc.wantTvdb) {
				t.Errorf("kept tvdb ids: got %v want %v", gotTvdb, tc.wantTvdb)
			}
		})
	}
}

// SyncSeasonMonitoring=true carries Seasons hint through.
func TestSonarrImport_Fetch_SyncSeasonMonitoring(t *testing.T) {
	t.Parallel()
	body := `[
		{"title":"A","tvdbId":1,"qualityProfileId":1,"languageProfileId":1,"rootFolderPath":"/mnt","tags":[],
		 "seasons":[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":false}]}
	]`
	srv := stubSeriesServer(t, body)
	defer srv.Close()

	p, _ := newImport(t, srv.URL, sonarr.Settings{SyncSeasonMonitoring: true}, &fakeStatusRecorder{}, srv.Client())
	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Series) != 1 {
		t.Fatalf("series count: %d", len(got.Series))
	}
	if len(got.Series[0].Seasons) != 2 ||
		got.Series[0].Seasons[0].SeasonNumber != 1 || !got.Series[0].Seasons[0].Monitored ||
		got.Series[0].Seasons[1].SeasonNumber != 2 || got.Series[0].Seasons[1].Monitored {
		t.Errorf("Seasons: %+v", got.Series[0].Seasons)
	}
}

// SyncSeasonMonitoring=false drops the Seasons hint.
func TestSonarrImport_Fetch_SeasonMonitoringDisabledByDefault(t *testing.T) {
	t.Parallel()
	body := `[
		{"title":"A","tvdbId":1,"qualityProfileId":1,"languageProfileId":1,"rootFolderPath":"/mnt","tags":[],
		 "seasons":[{"seasonNumber":1,"monitored":true}]}
	]`
	srv := stubSeriesServer(t, body)
	defer srv.Close()

	p, _ := newImport(t, srv.URL, sonarr.Settings{}, &fakeStatusRecorder{}, srv.Client())
	got, _ := p.Fetch(context.Background())
	if len(got.Series) != 1 || len(got.Series[0].Seasons) != 0 {
		t.Errorf("Seasons not dropped: %+v", got.Series[0])
	}
}

// Hard failure on Fetch: server returns 500, expect RecordFailure
// + AnyFailure=true + empty Series slice.
func TestSonarrImport_Fetch_HardFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p, _ := newImport(t, srv.URL, sonarr.Settings{}, rec, srv.Client())
	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: expected nil err, got %v", err)
	}
	if !got.AnyFailure {
		t.Errorf("AnyFailure: false want true")
	}
	if len(got.Series) != 0 {
		t.Errorf("Series: got %d want 0", len(got.Series))
	}
	if rec.failure.Load() != 1 || rec.success.Load() != 0 {
		t.Errorf("recorder: success=%d failure=%d want 0/1", rec.success.Load(), rec.failure.Load())
	}
}

// Test() short-circuits with missing-ApiKey before hitting the network.
func TestSonarrImport_Test_MissingAPIKey(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{
		ID:             1,
		Name:           "x",
		Implementation: sonarr.Implementation,
		Settings:       settingsJSON(t, sonarr.Settings{BaseURL: "http://peer"}),
	}
	p, err := sonarr.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	failures, err := p.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 1 || failures[0].PropertyName != "ApiKey" {
		t.Errorf("expected ApiKey failure, got %+v", failures)
	}
}

// RequestAction("getProfiles") returns the sorted options payload.
func TestSonarrImport_RequestAction_GetProfiles(t *testing.T) {
	t.Parallel()
	srv := stubSeriesServer(t, `[]`)
	defer srv.Close()
	p, _ := newImport(t, srv.URL, sonarr.Settings{}, &fakeStatusRecorder{}, srv.Client())
	got, err := p.RequestAction(context.Background(), "getProfiles", nil)
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("payload shape: %T", got)
	}
	opts, ok := m["options"].([]map[string]any)
	if !ok || len(opts) != 2 {
		t.Fatalf("options: %v", m["options"])
	}
	// Sort: case-insensitive on Name — "Any" before "HD"
	if opts[0]["name"] != "Any" || opts[1]["name"] != "HD" {
		t.Errorf("sort order: %v", opts)
	}
}

// RequestAction with empty APIKey returns the {"devices": []}
// short-circuit per C# `SonarrImport.cs:99-105`.
func TestSonarrImport_RequestAction_EmptyAPIKey(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{
		ID:             1,
		Name:           "x",
		Implementation: sonarr.Implementation,
		Settings:       settingsJSON(t, sonarr.Settings{BaseURL: "http://peer"}),
	}
	p, _ := sonarr.New(row, importlists.Dependencies{})
	got, err := p.RequestAction(context.Background(), "getProfiles", nil)
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, _ := got.(map[string]any)
	if m == nil {
		t.Fatalf("payload: %v", got)
	}
	devices, ok := m["devices"]
	if !ok {
		t.Errorf("expected devices key, got %v", m)
	}
	if d, _ := devices.([]any); len(d) != 0 {
		t.Errorf("devices not empty: %v", devices)
	}
}

// RequestAction with unknown action returns {} per C# `:169`.
func TestSonarrImport_RequestAction_UnknownAction(t *testing.T) {
	t.Parallel()
	srv := stubSeriesServer(t, `[]`)
	defer srv.Close()
	p, _ := newImport(t, srv.URL, sonarr.Settings{}, &fakeStatusRecorder{}, srv.Client())
	got, err := p.RequestAction(context.Background(), "unknownAction", nil)
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok || len(m) != 0 {
		t.Errorf("expected {}, got %v", got)
	}
}

// Slice 4.AM closes sub-arc I3a by registering against the package-
// global Default Registry. Verify the (Implementation, ConfigContract)
// pair is reachable from a freshly-imported test binary (this is
// triggered by the file-scope `_` reference to `sonarr.New` above,
// which pulls the package in and runs init()).
func TestRegister_DefaultRegistered(t *testing.T) {
	t.Parallel()
	if !importlists.Default().Has(sonarr.Implementation, sonarr.ConfigContract) {
		t.Errorf("Default registry missing (%s, %s) — init() did not run",
			sonarr.Implementation, sonarr.ConfigContract)
	}
}

// Settings JSON deserialisation round-trip: ensure the New
// constructor parses a real persisted settings JSON.
func TestSonarrImport_New_SettingsJSON(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{
		Name:           "x",
		Implementation: sonarr.Implementation,
		Settings: settingsJSON(t, sonarr.Settings{
			BaseURL:              "http://peer:8989",
			APIKey:               "abc",
			SyncSeasonMonitoring: true,
			ProfileIDs:           []int{1, 2},
			TagIDs:               []int{5},
			RootFolderPaths:      []string{"/mnt/tv"},
		}),
	}
	p, err := sonarr.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("New: nil")
	}
}
