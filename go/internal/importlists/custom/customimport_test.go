package custom_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/importlists/custom"
)

// fakeStatusRecorder records the (RecordSuccess, RecordFailure)
// calls Fetch should make per the C# `CustomImport.cs:53/:60`
// recording semantics. Same shape as the sonarr-import precedent.
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

func settingsJSON(t *testing.T, s custom.Settings) *string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	return &out
}

func newImport(t *testing.T, srvURL string, recorder *fakeStatusRecorder, doer *http.Client) importlists.Provider {
	t.Helper()
	row := &importlists.ImportList{
		ID:             5,
		Name:           "MyCustomList",
		Implementation: custom.Implementation,
		Settings:       settingsJSON(t, custom.Settings{BaseURL: srvURL}),
	}
	deps := importlists.Dependencies{
		HTTPClient:    doer,
		StatusService: recorder,
	}
	p, err := custom.New(row, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestCustomImport_Metadata(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{Name: "X", Implementation: custom.Implementation}
	p, err := custom.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "Custom List" {
		t.Errorf("Name=%q want Custom List", p.Name())
	}
	if p.ListType() != importlists.ListTypeAdvanced {
		t.Errorf("ListType=%v want Advanced", p.ListType())
	}
	if p.MinRefreshInterval() != 6*time.Hour {
		t.Errorf("MinRefreshInterval=%v want 6h", p.MinRefreshInterval())
	}
	if p.Definition() != row {
		t.Errorf("Definition: pointer not preserved")
	}
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    custom.Settings
		want string
	}{
		{"Empty", custom.Settings{}, "BaseUrl"},
		{"NoScheme", custom.Settings{BaseURL: "example.com/list.json"}, "BaseUrl"},
		{"BadScheme", custom.Settings{BaseURL: "ftp://example.com"}, "BaseUrl"},
		{"OK", custom.Settings{BaseURL: "https://example.com/list.json"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fails := c.s.Validate()
			if c.want == "" {
				if len(fails) != 0 {
					t.Errorf("expected no failures, got %+v", fails)
				}
				return
			}
			found := false
			for _, f := range fails {
				if f.PropertyName == c.want {
					found = true
				}
			}
			if !found {
				t.Errorf("missing %s failure; got %+v", c.want, fails)
			}
		})
	}
}

func TestCustomImport_Fetch_StampsListIDAndDedups(t *testing.T) {
	t.Parallel()
	body := `[
		{"title":"A","tvdbId":1,"tmdbId":10,"imdbId":"tt001"},
		{"title":"B","tvdbId":2,"tmdbId":0,"imdbId":""},
		{"title":"A","tvdbId":1,"tmdbId":10,"imdbId":"tt001"}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, rec, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.AnyFailure {
		t.Errorf("AnyFailure=true on success")
	}
	if len(got.Series) != 2 {
		t.Fatalf("Series=%d want 2 (after dedup)", len(got.Series))
	}
	first := got.Series[0]
	if first.ImportListID != 5 || first.ImportList != "MyCustomList" {
		t.Errorf("ImportListID/ImportList not stamped: %+v", first)
	}
	if first.Title != "A" || first.TvdbID != 1 || first.TmdbID != 10 || first.ImdbID != "tt001" {
		t.Errorf("first=%+v", first)
	}
	if rec.success.Load() != 1 || rec.failure.Load() != 0 {
		t.Errorf("recorder success=%d failure=%d want 1/0", rec.success.Load(), rec.failure.Load())
	}
}

func TestCustomImport_Fetch_FallsBackToTvdbIdWhenTitleMissing(t *testing.T) {
	t.Parallel()
	body := `[{"title":"","tvdbId":42}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p := newImport(t, srv.URL, &fakeStatusRecorder{}, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Series) != 1 {
		t.Fatalf("Series=%d want 1", len(got.Series))
	}
	if got.Series[0].Title != "TvdbId: 42" {
		t.Errorf("Title=%q want 'TvdbId: 42'", got.Series[0].Title)
	}
}

func TestCustomImport_Fetch_ServerErrorReportsFailureButDoesNotPropagate(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	rec := &fakeStatusRecorder{}
	p := newImport(t, srv.URL, rec, srv.Client())
	got, err := p.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.AnyFailure {
		t.Errorf("AnyFailure=false on server error")
	}
	if rec.success.Load() != 0 || rec.failure.Load() != 1 {
		t.Errorf("recorder success=%d failure=%d want 0/1", rec.success.Load(), rec.failure.Load())
	}
}

func TestCustomImport_Test_401MapsToBaseUrl(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := newImport(t, srv.URL, &fakeStatusRecorder{}, srv.Client())
	failures, _ := p.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "BaseUrl" {
		t.Errorf("expected BaseUrl failure, got %+v", failures)
	}
}

func TestCustomImport_Test_SuccessReturnsZeroFailures(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	p := newImport(t, srv.URL, &fakeStatusRecorder{}, srv.Client())
	failures, err := p.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
}

func TestCustomImport_Test_InvalidSettingsReportsViaValidate(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{
		Name:           "Bad",
		Implementation: custom.Implementation,
		Settings:       settingsJSON(t, custom.Settings{BaseURL: ""}),
	}
	p, err := custom.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	failures, _ := p.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "BaseUrl" {
		t.Errorf("expected BaseUrl validation failure, got %+v", failures)
	}
}

func TestCustomImport_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !importlists.Default().Has(custom.Implementation, custom.ConfigContract) {
		t.Errorf("registry missing %s/%s", custom.Implementation, custom.ConfigContract)
	}
}

func TestCustomImport_RequestActionIsNoop(t *testing.T) {
	t.Parallel()
	row := &importlists.ImportList{Name: "X", Implementation: custom.Implementation}
	p, err := custom.New(row, importlists.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	type actionable interface {
		RequestAction(ctx context.Context, action string, query map[string]string) (any, error)
	}
	if ra, ok := p.(actionable); ok {
		out, err := ra.RequestAction(t.Context(), "anything", nil)
		if err != nil {
			t.Errorf("RequestAction: %v", err)
		}
		if out == nil {
			t.Errorf("RequestAction returned nil")
		}
	}
}
