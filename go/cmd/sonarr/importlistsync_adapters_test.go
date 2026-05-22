package main

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/metadatasource"
	"sonarr.tv/sonarr/internal/series"
)

func TestHasSeasons_EmptyVariants(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", " ", "null", "[]", "  [] "} {
		if hasSeasons(in) {
			t.Errorf("hasSeasons(%q): want false", in)
		}
	}
}

func TestHasSeasons_NonEmptyArray(t *testing.T) {
	t.Parallel()
	in := `[{"seasonNumber":1,"monitored":true}]`
	if !hasSeasons(in) {
		t.Errorf("hasSeasons(%q): want true", in)
	}
}

func TestHasSeasons_Malformed_TreatedAsEmpty(t *testing.T) {
	t.Parallel()
	// Malformed JSON falls back to the SkyHook payload — see
	// addSkyHookData doc-comment.
	if hasSeasons("{not json") {
		t.Errorf("hasSeasons(malformed): want false")
	}
}

// stubSeriesInfoProvider is a [metadatasource.SeriesInfoProvider] test double.
type stubSeriesInfoProvider struct {
	got    int
	merged *series.Series
	eps    []episodes.Episode
	err    error
}

func (s *stubSeriesInfoProvider) GetSeriesInfo(_ context.Context, tvdbID int) (*series.Series, []episodes.Episode, error) {
	s.got = tvdbID
	return s.merged, s.eps, s.err
}

func TestAddSkyHookData_MergesSkyhookOverUserShape(t *testing.T) {
	t.Parallel()
	// SkyHook payload — authoritative for Title, Network, Images,
	// ImdbID, CleanTitle, etc.
	provider := &stubSeriesInfoProvider{
		merged: &series.Series{
			TvdbID:     200,
			Title:      "skyhook-title",
			CleanTitle: "skyhookclean",
			Network:    "skyhook-network",
			ImdbID:     "tt-skyhook",
			Seasons:    `[{"seasonNumber":1,"monitored":false}]`,
			Images:     `[{"coverType":"poster"}]`,
		},
	}
	// User-supplied add-shape — wins for the 11 transferable fields.
	user := series.Series{
		TvdbID:           200,
		Title:            "user-title-IGNORED",
		Path:             "/tv/Show",
		QualityProfileID: 3,
		Monitored:        true,
		SeasonFolder:     true,
		MonitorNewItems:  1,
		RootFolderPath:   "/tv",
		Tags:             []int{7},
		Seasons:          "", // empty → SkyHook seasons win
	}

	got, _, err := addSkyHookData(t.Context(), provider, user)
	if err != nil {
		t.Fatalf("addSkyHookData: %v", err)
	}
	if provider.got != 200 {
		t.Errorf("provider called with tvdbId=%d, want 200", provider.got)
	}

	// SkyHook-authoritative fields must come from the merged payload.
	if got.Title != "skyhook-title" {
		t.Errorf("Title: got %q, want skyhook-title", got.Title)
	}
	if got.Network != "skyhook-network" {
		t.Errorf("Network: got %q", got.Network)
	}
	if got.ImdbID != "tt-skyhook" {
		t.Errorf("ImdbID: got %q", got.ImdbID)
	}
	if got.Images != `[{"coverType":"poster"}]` {
		t.Errorf("Images: got %q", got.Images)
	}

	// User-supplied fields must override.
	if got.Path != "/tv/Show" {
		t.Errorf("Path: got %q", got.Path)
	}
	if got.QualityProfileID != 3 {
		t.Errorf("QualityProfileID: got %d", got.QualityProfileID)
	}
	if !got.Monitored {
		t.Errorf("Monitored: want true")
	}
	if !got.SeasonFolder {
		t.Errorf("SeasonFolder: want true")
	}
	if got.MonitorNewItems != 1 {
		t.Errorf("MonitorNewItems: got %d", got.MonitorNewItems)
	}
	if got.RootFolderPath != "/tv" {
		t.Errorf("RootFolderPath: got %q", got.RootFolderPath)
	}
	if len(got.Tags) != 1 || got.Tags[0] != 7 {
		t.Errorf("Tags: got %v", got.Tags)
	}

	// Empty user Seasons → SkyHook seeds.
	if got.Seasons != `[{"seasonNumber":1,"monitored":false}]` {
		t.Errorf("Seasons (empty-user): got %q", got.Seasons)
	}
}

func TestAddSkyHookData_PreservesUserSeasonsWhenNonEmpty(t *testing.T) {
	t.Parallel()
	provider := &stubSeriesInfoProvider{
		merged: &series.Series{
			TvdbID:  200,
			Seasons: `[{"seasonNumber":1,"monitored":false}]`, // SkyHook says S1
		},
	}
	user := series.Series{
		TvdbID:  200,
		Seasons: `[{"seasonNumber":2,"monitored":true}]`, // user pre-picked S2
	}
	got, _, err := addSkyHookData(t.Context(), provider, user)
	if err != nil {
		t.Fatalf("addSkyHookData: %v", err)
	}
	if got.Seasons != `[{"seasonNumber":2,"monitored":true}]` {
		t.Errorf("Seasons: want user's, got %q", got.Seasons)
	}
}

func TestAddSkyHookData_ProviderErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("skyhook unreachable")
	provider := &stubSeriesInfoProvider{err: want}
	_, _, err := addSkyHookData(t.Context(), provider, series.Series{TvdbID: 200})
	if !errors.Is(err, want) {
		t.Fatalf("err: got %v, want %v", err, want)
	}
}

func TestAddSkyHookData_NilMergedReturnsError(t *testing.T) {
	t.Parallel()
	// Defensive — buggy provider returning (nil, nil, nil) should error
	// rather than panic.
	provider := &stubSeriesInfoProvider{merged: nil, err: nil}
	_, _, err := addSkyHookData(t.Context(), provider, series.Series{TvdbID: 200})
	if err == nil {
		t.Fatal("addSkyHookData: want error on nil merged")
	}
}

// Compile-time assertion that [stubSeriesInfoProvider] satisfies the metadatasource
// interface — keeps the test honest if the interface shape drifts.
var _ metadatasource.SeriesInfoProvider = (*stubSeriesInfoProvider)(nil)

// TestAddSkyHookData_ReturnsEpisodes — J190 verifies the previously-
// dropped episode slice is now passed through. Caller seeds these
// into episodes.Repo.InsertMany via the AddSeries loop's seeder.
func TestAddSkyHookData_ReturnsEpisodes(t *testing.T) {
	t.Parallel()
	provider := &stubSeriesInfoProvider{
		merged: &series.Series{TvdbID: 200, Title: "Show"},
		eps: []episodes.Episode{
			{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"},
			{SeasonNumber: 1, EpisodeNumber: 2, Title: "Two"},
		},
	}
	_, eps, err := addSkyHookData(t.Context(), provider, series.Series{TvdbID: 200})
	if err != nil {
		t.Fatalf("addSkyHookData: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("episodes count: got %d, want 2", len(eps))
	}
	if eps[0].EpisodeNumber != 1 || eps[1].EpisodeNumber != 2 {
		t.Errorf("episode order/values not preserved: %+v", eps)
	}
}

// stubEpisodeSeeder is a test double for the [episodeSeeder] narrow
// interface — captures the seed-list so tests can assert SeriesID
// stamping + InsertMany invocation.
type stubEpisodeSeeder struct {
	got       []*episodes.Episode
	callCount int
	err       error
}

func (s *stubEpisodeSeeder) InsertMany(_ context.Context, list []*episodes.Episode) error {
	s.callCount++
	if s.err != nil {
		return s.err
	}
	s.got = append(s.got, list...)
	return nil
}

// Compile-time assertion stub satisfies the interface.
var _ episodeSeeder = (*stubEpisodeSeeder)(nil)
