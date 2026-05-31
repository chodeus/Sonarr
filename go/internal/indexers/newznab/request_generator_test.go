package newznab

import (
	"context"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/series"
)

// Tests transcribe src/NzbDrone.Core.Test/IndexerTests/NewznabTests/
// NewznabRequestGeneratorFixture.cs case-by-case.

func newTestGenerator() *RequestGenerator {
	s := NewSettings()
	s.BaseUrl = "http://127.0.0.1:1234/"
	s.Categories = []int{1, 2}
	s.AnimeCategories = []int{3, 4}
	s.ApiKey = "abcd"
	g := NewRequestGenerator(s, NewCapabilities(), "Newznab")
	return g
}

func defaultSeries() *series.Series {
	return &series.Series{
		Title:    "Series",
		TvdbID:   20,
		TvRageID: 10,
		TvMazeID: 30,
		ImdbID:   "t40",
		TmdbID:   50,
	}
}

// should_use_all_categories_for_feed
func TestGen_RecentMergesCategories(t *testing.T) {
	g := newTestGenerator()
	chain, err := g.GetRecentRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	streams := chain.AllTiers()
	if len(streams) != 1 {
		t.Fatalf("streams = %d", len(streams))
	}
	url := streams[0][0].URL.String()
	if !strings.Contains(url, "&cat=1,2,3,4&") {
		t.Errorf("merged cats: %q", url)
	}
}

// should_not_have_duplicate_categories
func TestGen_RecentNoDuplicateCategories(t *testing.T) {
	g := newTestGenerator()
	g.Settings.Categories = []int{1, 2, 3}
	chain, err := g.GetRecentRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	streams := chain.AllTiers()
	if len(streams) != 1 {
		t.Fatal("streams != 1")
	}
	url := streams[0][0].URL.String()
	if !strings.Contains(url, "&cat=1,2,3,4&") {
		t.Errorf("dedup cats: %q", url)
	}
}

// should_use_only_anime_categories_for_anime_search
func TestGen_AnimeUsesOnlyAnimeCategories(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.AnimeEpisodeCriteria{
		CriteriaBase: indexersearch.CriteriaBase{
			Series:      defaultSeries(),
			SceneTitles: []string{"Monkey+Island"},
		},
		AbsoluteEpisodeNumber: 100,
		SeasonNumber:          5,
		EpisodeNumber:         4,
	}
	chain, err := g.GetSearchRequests(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	streams := chain.AllTiers()
	if len(streams) == 0 {
		t.Fatal("no streams")
	}
	for si, stream := range streams {
		if len(stream) == 0 {
			continue
		}
		url := stream[0].URL.String()
		if !strings.Contains(url, "&cat=3,4&") {
			t.Errorf("stream %d: %q", si, url)
		}
	}
}

// should_use_mode_search_for_anime — first stream uses tvsearch (rid path),
// last uses search. Mirrors C# fixture HaveCount(2) + first/last assertions.
func TestGen_AnimeModes(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.AnimeEpisodeCriteria{
		CriteriaBase: indexersearch.CriteriaBase{
			Series:      defaultSeries(),
			SceneTitles: []string{"Monkey+Island"},
		},
		AbsoluteEpisodeNumber: 100,
		SeasonNumber:          5,
		EpisodeNumber:         4,
	}
	chain, err := g.GetSearchRequests(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	streams := chain.AllTiers()
	if len(streams) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(streams))
	}
	first := streams[0][0].URL.String()
	last := streams[len(streams)-1][0].URL.String()
	if !strings.Contains(first, "?t=tvsearch&") {
		t.Errorf("first stream should be tvsearch: %q", first)
	}
	if !strings.Contains(last, "?t=search&") {
		t.Errorf("last stream should be search: %q", last)
	}
}

// should_return_subsequent_pages
func TestGen_PaginationOffsets(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.AnimeEpisodeCriteria{
		CriteriaBase:          indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Monkey+Island"}},
		AbsoluteEpisodeNumber: 100,
	}
	chain, err := g.GetSearchRequests(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	stream := chain.AllTiers()[0]
	if len(stream) < 3 {
		t.Fatalf("stream has %d pages", len(stream))
	}
	if !strings.Contains(stream[0].URL.String(), "&offset=0&") {
		t.Errorf("page 0: %q", stream[0].URL.String())
	}
	if !strings.Contains(stream[1].URL.String(), "&offset=100&") {
		t.Errorf("page 1: %q", stream[1].URL.String())
	}
	if !strings.Contains(stream[2].URL.String(), "&offset=200&") {
		t.Errorf("page 2: %q", stream[2].URL.String())
	}
}

// should_not_get_unlimited_pages
func TestGen_PaginationCappedByMaxPages(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.AnimeEpisodeCriteria{
		CriteriaBase:          indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Monkey+Island"}},
		AbsoluteEpisodeNumber: 100,
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	stream := chain.AllTiers()[0]
	if len(stream) > g.MaxPages {
		t.Errorf("stream length %d > MaxPages %d", len(stream), g.MaxPages)
	}
	if len(stream) >= 500 {
		t.Errorf("stream is unbounded (%d)", len(stream))
	}
}

// should_use_only_absolute_numbering_for_anime_search
func TestGen_AnimeAbsoluteNumberingOnly(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.AnimeEpisodeCriteria{
		CriteriaBase:          indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Monkey+Island"}},
		AbsoluteEpisodeNumber: 100,
		SeasonNumber:          5,
		EpisodeNumber:         4,
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	tier0 := chain.Tier(0)
	if len(tier0) < 2 {
		t.Fatalf("tier 0 streams = %d", len(tier0))
	}
	first := tier0[0][0].URL.String()
	second := tier0[1][0].URL.String()
	if !strings.Contains(first, "rid=10&q=100") {
		t.Errorf("first: %q", first)
	}
	if !strings.Contains(second, "q=Monkey%20Island+100") {
		t.Errorf("second: %q", second)
	}
}

// should_also_use_standard_numbering_for_anime_search
func TestGen_AnimeStandardFormatSearch(t *testing.T) {
	g := newTestGenerator()
	g.Settings.AnimeStandardFormatSearch = true
	c := &indexersearch.AnimeEpisodeCriteria{
		CriteriaBase:          indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Monkey+Island"}},
		AbsoluteEpisodeNumber: 100,
		SeasonNumber:          5,
		EpisodeNumber:         4,
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	tier0 := chain.Tier(0)
	if len(tier0) != 4 {
		t.Fatalf("tier 0 streams = %d, want 4", len(tier0))
	}
	urls := []string{
		tier0[0][0].URL.String(),
		tier0[1][0].URL.String(),
		tier0[2][0].URL.String(),
		tier0[3][0].URL.String(),
	}
	wants := []string{
		"rid=10&q=100",
		"rid=10&season=5&ep=4",
		"q=Monkey%20Island+100",
		"q=Monkey%20Island&season=5&ep=4",
	}
	for i, w := range wants {
		if !strings.Contains(urls[i], w) {
			t.Errorf("page %d: got %q want substring %q", i, urls[i], w)
		}
	}
}

// should_search_by_standard_season_number — anime season search with std format
func TestGen_AnimeSeasonStandardNumber(t *testing.T) {
	g := newTestGenerator()
	g.Settings.AnimeStandardFormatSearch = true
	c := &indexersearch.AnimeSeasonCriteria{
		CriteriaBase: indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Monkey Island"}},
		SeasonNumber: 3,
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	tier0 := chain.Tier(0)
	if len(tier0) != 2 {
		t.Fatalf("tier 0 streams = %d, want 2", len(tier0))
	}
	if !strings.Contains(tier0[0][0].URL.String(), "rid=10&season=3") {
		t.Errorf("first: %q", tier0[0][0].URL.String())
	}
	if !strings.Contains(tier0[1][0].URL.String(), "q=Monkey%20Island&season=3") {
		t.Errorf("second: %q", tier0[1][0].URL.String())
	}
}

// TestGen_AcceptsValueCriteria locks in the runtime fix: the production
// dispatcher (indexersearch.Service.dispatchSearch) passes criteria BY
// VALUE, not by pointer. Before criteriaToPointer, a value-typed criteria
// fell through to the default arm ("unsupported criteria type") and every
// real newznab/torznab search produced zero results — a bug only the
// integration path hit, since these unit tests passed pointers.
func TestGen_AcceptsValueCriteria(t *testing.T) {
	g := newTestGenerator()
	// NOTE: value, not &.
	c := indexersearch.SingleEpisodeCriteria{
		CriteriaBase:  indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Monkey Island"}},
		SeasonNumber:  1,
		EpisodeNumber: 2,
	}
	chain, err := g.GetSearchRequests(context.Background(), c)
	if err != nil {
		t.Fatalf("value criteria rejected: %v", err)
	}
	if len(chain.AllTiers()) == 0 {
		t.Fatal("value criteria produced no request streams")
	}
}

// should_not_search_by_rid_if_not_supported
func TestGen_NotByRidWhenUnsupported(t *testing.T) {
	g := newTestGenerator()
	g.Capabilities.SupportedTvSearchParameters = []string{"q", "season", "ep"}

	c := &indexersearch.SingleEpisodeCriteria{
		CriteriaBase:  indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Monkey Island"}},
		SeasonNumber:  1,
		EpisodeNumber: 2,
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	streams := chain.AllTiers()
	if len(streams) == 0 {
		t.Fatal("no streams")
	}
	for _, stream := range streams {
		for _, req := range stream {
			if strings.Contains(req.URL.String(), "rid=") {
				t.Errorf("rid leaked despite cap drop: %q", req.URL.String())
			}
		}
	}
}

func TestGen_SingleEpisodeURLFormat(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.SingleEpisodeCriteria{
		CriteriaBase:  indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Monkey Island"}},
		SeasonNumber:  1,
		EpisodeNumber: 2,
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	tier0 := chain.Tier(0)
	if len(tier0) == 0 {
		t.Fatal("no tier 0 streams")
	}
	first := tier0[0][0].URL.String()
	if !strings.Contains(first, "?t=tvsearch&") {
		t.Errorf("expected tvsearch: %q", first)
	}
	if !strings.Contains(first, "&season=1&ep=2") {
		t.Errorf("expected &season=1&ep=2: %q", first)
	}
	if !strings.Contains(first, "extended=1") {
		t.Errorf("expected extended=1: %q", first)
	}
	if !strings.Contains(first, "apikey=abcd") {
		t.Errorf("expected apikey=abcd: %q", first)
	}
}

// Season=0 should map to "00" per NewznabifySeasonNumber. C# :637-641.
func TestGen_SeasonZeroEncodesAs00(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.SingleEpisodeCriteria{
		CriteriaBase:  indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"x"}},
		SeasonNumber:  0,
		EpisodeNumber: 5,
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	if chain.Tiers() == 0 {
		t.Fatal("no tiers")
	}
	first := chain.Tier(0)[0][0].URL.String()
	if !strings.Contains(first, "&season=00&ep=5") {
		t.Errorf("season=0 should encode as 00: %q", first)
	}
}

// Daily episode search uses yyyy / MM/dd format. C# :333.
func TestGen_DailyEpisodeAirDate(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.DailyEpisodeCriteria{
		CriteriaBase: indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Daily Show"}},
	}
	c.AirDate = mustDate(t, "2026-05-10")
	chain, _ := g.GetSearchRequests(context.Background(), c)
	first := chain.Tier(0)[0][0].URL.String()
	if !strings.Contains(first, "&season=2026&ep=05/10") {
		t.Errorf("daily episode airdate: %q", first)
	}
}

// Daily season search uses Year as season. C# :380.
func TestGen_DailySeasonYearAsSeason(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.DailySeasonCriteria{
		CriteriaBase: indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Daily Show"}},
		Year:         2026,
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	first := chain.Tier(0)[0][0].URL.String()
	if !strings.Contains(first, "&season=2026") {
		t.Errorf("daily season year: %q", first)
	}
}

// Special episode search URL-encodes query strings using %20 (replaces +).
func TestGen_SpecialEpisodeURLEncoding(t *testing.T) {
	g := newTestGenerator()
	c := &indexersearch.SpecialEpisodeCriteria{
		CriteriaBase:       indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"Some Show"}},
		EpisodeQueryTitles: []string{"Christmas+Special"},
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	if chain.Tiers() == 0 {
		t.Fatal("no tiers")
	}
	first := chain.Tier(0)[0][0].URL.String()
	// Query.QueryEscape encodes spaces as +; url.QueryEscape("Christmas Special") = "Christmas+Special"
	if !strings.Contains(first, "&q=Christmas+Special") {
		t.Errorf("special encoding: %q", first)
	}
}

// AggregatedID search. C# :505-535. When supported AND we have at least
// one of the non-imdb IDs, all configured IDs go in one request.
func TestGen_AggregateIDSearch(t *testing.T) {
	g := newTestGenerator()
	g.Capabilities.SupportsAggregateIDSearch = true
	g.Capabilities.SupportedTvSearchParameters = []string{"q", "tvdbid", "imdbid", "rid", "tvmazeid", "tmdbid", "season", "ep"}
	c := &indexersearch.SingleEpisodeCriteria{
		CriteriaBase:  indexersearch.CriteriaBase{Series: defaultSeries(), SceneTitles: []string{"x"}},
		SeasonNumber:  1,
		EpisodeNumber: 2,
	}
	chain, _ := g.GetSearchRequests(context.Background(), c)
	first := chain.Tier(0)[0][0].URL.String()
	for _, want := range []string{"tvdbid=20", "imdbid=t40", "rid=10", "tvmazeid=30", "tmdbid=50"} {
		if !strings.Contains(first, want) {
			t.Errorf("aggregate URL missing %q: %s", want, first)
		}
	}
}

// Empty categories → empty stream. C# :603-606. Note: the chain still
// has its initial empty tier (matching C# constructor seed); the
// observable is `AllTiers()` returning zero streams.
func TestGen_EmptyCategoriesYieldsNoRequests(t *testing.T) {
	g := newTestGenerator()
	g.Settings.Categories = nil
	g.Settings.AnimeCategories = nil
	chain, err := g.GetRecentRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.AllTiers()) != 0 {
		t.Errorf("expected 0 streams, got %d", len(chain.AllTiers()))
	}
}

// PageSize=0 yields a single un-paged request. C# :620.
func TestGen_PageSizeZeroSinglePage(t *testing.T) {
	g := newTestGenerator()
	g.PageSize = 0
	chain, _ := g.GetRecentRequests(context.Background())
	stream := chain.AllTiers()[0]
	if len(stream) != 1 {
		t.Fatalf("PageSize=0 should yield 1 page, got %d", len(stream))
	}
	if strings.Contains(stream[0].URL.String(), "offset=") || strings.Contains(stream[0].URL.String(), "limit=") {
		t.Errorf("un-paged request must not carry offset/limit: %q", stream[0].URL.String())
	}
}

// SearchCategories anime branch: anime series → AnimeCategories.
func TestGen_AnimeSeriesUsesAnimeCategories(t *testing.T) {
	g := newTestGenerator()
	s := defaultSeries()
	s.SeriesType = series.SeriesTypeAnime
	cats := g.searchCategories(s)
	if len(cats) != 2 || cats[0] != 3 || cats[1] != 4 {
		t.Errorf("expected anime categories, got %v", cats)
	}
}

func TestGen_RegularSeriesUsesCategories(t *testing.T) {
	g := newTestGenerator()
	s := defaultSeries()
	s.SeriesType = series.SeriesTypeStandard
	cats := g.searchCategories(s)
	if len(cats) != 2 || cats[0] != 1 || cats[1] != 2 {
		t.Errorf("expected categories, got %v", cats)
	}
}

// CleanSceneTitle helper tests
func TestCleanSceneTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"The Office", "Office"},
		{"Hello & Goodbye", "Hello+and+Goodbye"},
		{"Don't Stop Me", "Dont+Stop+Me"},
		{"Star Trek: Voyager", "Star+Trek+Voyager"},
		{"Café Society", "Cafe+Society"},
	}
	for _, c := range cases {
		got := indexersearch.GetCleanSceneTitle(c.in)
		if got != c.want {
			t.Errorf("GetCleanSceneTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// SearchMode HasFlag tests
func TestSearchMode_HasFlag(t *testing.T) {
	combined := indexersearch.SearchModeSearchID | indexersearch.SearchModeSearchTitle
	if !combined.HasFlag(indexersearch.SearchModeSearchID) {
		t.Error("HasFlag SearchID failed")
	}
	if !combined.HasFlag(indexersearch.SearchModeSearchTitle) {
		t.Error("HasFlag SearchTitle failed")
	}
	if combined.HasFlag(indexersearch.SearchModeRSS) {
		t.Error("HasFlag RSS should be false")
	}
}

// mustDate parses YYYY-MM-DD into a UTC time.Time.
func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	out, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return out.UTC()
}
