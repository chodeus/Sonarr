package newznab

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/series"
)

// RequestGenerator builds HTTP request URLs for a Newznab indexer.
// C# cite: src/NzbDrone.Core/Indexers/Newznab/NewznabRequestGenerator.cs:15-650.
//
// The generator is parameterised by:
//   - Settings — the row's typed Settings (BaseUrl/ApiPath/ApiKey/Categories/...)
//   - Capabilities — the indexer's caps response (search params support, page sizes)
//   - DefinitionName — used in trace logs only; passes through from the Definition
//
// Each Get*Requests entry point returns a PageableRequestChain which is
// a tiered set of paged-request streams. Tiers are tried in order; within
// a tier, all streams run in parallel. Mirrors C# IndexerPageableRequestChain.
//
// The generator accepts a CapabilitiesProvider so caps fetches lazily flow
// through the same TTL cache the Indexer struct uses, but it can also be
// constructed with a pre-fetched *Capabilities (the typical search-time
// path: caps were already pulled at boot or on first call).
type RequestGenerator struct {
	Settings       *Settings
	Capabilities   *Capabilities
	DefinitionName string
	MaxPages       int
	PageSize       int

	// CapabilitiesProvider, if non-nil, is consulted via GetCapabilities
	// when Capabilities is unset. Either of the two must be wired.
	CapabilitiesProvider *CapabilitiesProvider
}

// NewRequestGenerator returns a generator with the C# defaults at
// NewznabRequestGenerator.cs:30-31 (MaxPages=30, PageSize=100).
func NewRequestGenerator(settings *Settings, caps *Capabilities, defName string) *RequestGenerator {
	return &RequestGenerator{
		Settings:       settings,
		Capabilities:   caps,
		DefinitionName: defName,
		MaxPages:       30,
		PageSize:       100,
	}
}

// caps returns the bound Capabilities, falling back to the provider when
// nil. The provider is only consulted lazily so unit tests with a
// pre-built Capabilities value never need a stub HTTP client.
func (g *RequestGenerator) caps(ctx context.Context) (*Capabilities, error) {
	if g.Capabilities != nil {
		return g.Capabilities, nil
	}
	if g.CapabilitiesProvider == nil {
		return nil, fmt.Errorf("newznab: no Capabilities and no CapabilitiesProvider")
	}
	return g.CapabilitiesProvider.GetCapabilities(ctx, g.Settings)
}

// PageableRequest is one paged stream — an ordered list of HTTP requests
// each targeting the same searchType+params with successive offset/limit
// pagination. Mirrors the C# `IndexerPageableRequest` (an IEnumerable<IndexerRequest>).
type PageableRequest []*httpclient.Request

// PageableRequestChain is a tiered set of pageable streams. Mirrors
// `IndexerPageableRequestChain` at C# Indexers/IndexerPageableRequestChain.cs.
//
// Construction pattern in the C# generator:
//
//	chain.Add(stream)       // append to the *current* tier
//	chain.AddTier()         // open a new tier
//
// Consumption pattern: the search service iterates tier-by-tier, kicking
// each stream's pages out concurrently. The first tier that yields any
// release stops the cascade.
type PageableRequestChain struct {
	tiers [][]PageableRequest
}

// NewPageableRequestChain constructs an empty chain with one open tier.
func NewPageableRequestChain() *PageableRequestChain {
	return &PageableRequestChain{tiers: [][]PageableRequest{{}}}
}

// Add appends a stream to the current (last) tier. Empty streams are
// dropped (mirrors `categories.Empty()` short-circuit at C# :603-606).
func (c *PageableRequestChain) Add(stream PageableRequest) {
	if len(stream) == 0 {
		return
	}
	if len(c.tiers) == 0 {
		c.tiers = append(c.tiers, nil)
	}
	last := len(c.tiers) - 1
	c.tiers[last] = append(c.tiers[last], stream)
}

// AddTier opens a new (empty) tier. Mirrors C# AddTier.
func (c *PageableRequestChain) AddTier() {
	c.tiers = append(c.tiers, nil)
}

// Tiers returns the number of tiers. Mirrors C# `Tiers => _chains.Count`.
// Note: the constructor seeds one empty tier; AddTier short-circuits when
// the current tier is empty, so an empty tier 0 cannot grow into an empty
// tier N — only the initial state can have Tiers()==1 with no streams.
func (c *PageableRequestChain) Tiers() int { return len(c.tiers) }

// Tier returns the i'th tier. Mirrors C# `GetTier(int)` returning
// `_chains[index]`. Out-of-range indices return nil.
func (c *PageableRequestChain) Tier(i int) []PageableRequest {
	if i < 0 || i >= len(c.tiers) {
		return nil
	}
	return c.tiers[i]
}

// AllTiers returns every stream from every tier, flattened. Mirrors C#
// `GetAllTiers() => _chains.SelectMany(v => v)`.
//
// Note: the C# return type is `IEnumerable<IndexerPageableRequest>` which
// is flattened. Tests asserting `GetAllTiers().Should().HaveCount(N)`
// count streams across tiers, NOT tiers themselves.
func (c *PageableRequestChain) AllTiers() []PageableRequest {
	var out []PageableRequest
	for _, t := range c.tiers {
		out = append(out, t...)
	}
	return out
}

// GetRecentRequests builds the RSS-feed (no search-criteria) request set.
// C# cite: NewznabRequestGenerator.cs:196-212.
//
// When the indexer supports tv-search, both Categories + AnimeCategories
// are merged into a single category list; when only basic search is
// supported, AnimeCategories alone is used (matches the C# fallthrough).
func (g *RequestGenerator) GetRecentRequests(ctx context.Context) (*PageableRequestChain, error) {
	chain := NewPageableRequestChain()
	caps, err := g.caps(ctx)
	if err != nil {
		return chain, err
	}
	if caps.SupportedTvSearchParameters != nil {
		merged := mergeCategories(g.Settings.Categories, g.Settings.AnimeCategories)
		chain.Add(g.pagedRequests(g.MaxPages, merged, "tvsearch", ""))
	} else if caps.SupportedSearchParameters != nil {
		chain.Add(g.pagedRequests(g.MaxPages, g.Settings.AnimeCategories, "search", ""))
	}
	return chain, nil
}

// GetSearchRequests dispatches on the typed criteria value. Mirrors the
// seven `GetSearchRequests(<TCriteria>)` overloads at C# :214-495.
func (g *RequestGenerator) GetSearchRequests(ctx context.Context, criteria any) (*PageableRequestChain, error) {
	// The production dispatcher (indexersearch.Service.dispatchSearch)
	// passes criteria BY VALUE, but this type switch (and the per-builder
	// methods) operate on pointers. Normalize a by-value criteria to its
	// pointer form so both shapes match. Without this, every value-typed
	// search fell through to the default arm and produced zero results —
	// newznab/torznab search silently never worked at runtime (only the
	// pointer-passing unit tests ever exercised it).
	criteria = criteriaToPointer(criteria)
	switch c := criteria.(type) {
	case *indexersearch.SingleEpisodeCriteria:
		return g.getSingleEpisode(ctx, c)
	case *indexersearch.SeasonCriteria:
		return g.getSeason(ctx, c)
	case *indexersearch.DailyEpisodeCriteria:
		return g.getDailyEpisode(ctx, c)
	case *indexersearch.DailySeasonCriteria:
		return g.getDailySeason(ctx, c)
	case *indexersearch.AnimeEpisodeCriteria:
		return g.getAnimeEpisode(ctx, c)
	case *indexersearch.AnimeSeasonCriteria:
		return g.getAnimeSeason(ctx, c)
	case *indexersearch.SpecialEpisodeCriteria:
		return g.getSpecial(ctx, c)
	default:
		return NewPageableRequestChain(), fmt.Errorf("newznab: unsupported criteria type %T", criteria)
	}
}

// criteriaToPointer converts a by-value indexersearch criteria into its
// pointer form (the shape GetSearchRequests' type switch expects).
// Already-pointer and unknown types pass through unchanged.
func criteriaToPointer(criteria any) any {
	switch c := criteria.(type) {
	case indexersearch.SingleEpisodeCriteria:
		return &c
	case indexersearch.SeasonCriteria:
		return &c
	case indexersearch.DailyEpisodeCriteria:
		return &c
	case indexersearch.DailySeasonCriteria:
		return &c
	case indexersearch.AnimeEpisodeCriteria:
		return &c
	case indexersearch.AnimeSeasonCriteria:
		return &c
	case indexersearch.SpecialEpisodeCriteria:
		return &c
	default:
		return criteria
	}
}

// --- Per-criteria builders ---

func (g *RequestGenerator) getSingleEpisode(ctx context.Context, c *indexersearch.SingleEpisodeCriteria) (*PageableRequestChain, error) {
	chain := NewPageableRequestChain()
	caps, err := g.caps(ctx)
	if err != nil {
		return chain, err
	}
	if !supportsEpisodeSearch(caps) || (!supportsTvText(caps) && !supportsTvIDs(caps)) {
		return chain, nil
	}

	cats := g.searchCategories(c.Series)
	params := fmt.Sprintf("&season=%s&ep=%d", newznabifySeasonNumber(c.SeasonNumber), c.EpisodeNumber)

	if c.SearchMode.HasFlag(indexersearch.SearchModeSearchID) || c.SearchMode == indexersearch.SearchModeDefault {
		g.addTvIDPageable(chain, caps, cats, c.CriteriaBase, params)
	}
	if c.SearchMode.HasFlag(indexersearch.SearchModeSearchTitle) {
		g.addTitlePageable(chain, caps, cats, c.CriteriaBase, params)
	}
	chain.AddTier()
	if c.SearchMode == indexersearch.SearchModeDefault {
		g.addTitlePageable(chain, caps, cats, c.CriteriaBase, params)
	}
	return chain, nil
}

func (g *RequestGenerator) getSeason(ctx context.Context, c *indexersearch.SeasonCriteria) (*PageableRequestChain, error) {
	chain := NewPageableRequestChain()
	caps, err := g.caps(ctx)
	if err != nil {
		return chain, err
	}
	if !supportsSeasonSearch(caps) || (!supportsTvText(caps) && !supportsTvIDs(caps)) {
		return chain, nil
	}
	params := fmt.Sprintf("&season=%s", newznabifySeasonNumber(c.SeasonNumber))
	if c.SearchMode.HasFlag(indexersearch.SearchModeSearchID) || c.SearchMode == indexersearch.SearchModeDefault {
		g.addTvIDPageable(chain, caps, g.Settings.Categories, c.CriteriaBase, params)
	}
	if c.SearchMode.HasFlag(indexersearch.SearchModeSearchTitle) {
		g.addTitlePageable(chain, caps, g.Settings.Categories, c.CriteriaBase, params)
	}
	chain.AddTier()
	if c.SearchMode == indexersearch.SearchModeDefault {
		g.addTitlePageable(chain, caps, g.Settings.Categories, c.CriteriaBase, params)
	}
	return chain, nil
}

func (g *RequestGenerator) getDailyEpisode(ctx context.Context, c *indexersearch.DailyEpisodeCriteria) (*PageableRequestChain, error) {
	chain := NewPageableRequestChain()
	caps, err := g.caps(ctx)
	if err != nil {
		return chain, err
	}
	if !supportsEpisodeSearch(caps) || (!supportsTvText(caps) && !supportsTvIDs(caps)) {
		return chain, nil
	}
	params := fmt.Sprintf("&season=%s&ep=%s/%s",
		c.AirDate.Format("2006"),
		c.AirDate.Format("01"),
		c.AirDate.Format("02"),
	)
	if c.SearchMode.HasFlag(indexersearch.SearchModeSearchID) || c.SearchMode == indexersearch.SearchModeDefault {
		g.addTvIDPageable(chain, caps, g.Settings.Categories, c.CriteriaBase, params)
	}
	if c.SearchMode.HasFlag(indexersearch.SearchModeSearchTitle) {
		g.addTitlePageable(chain, caps, g.Settings.Categories, c.CriteriaBase, params)
	}
	chain.AddTier()
	if c.SearchMode == indexersearch.SearchModeDefault {
		g.addTitlePageable(chain, caps, g.Settings.Categories, c.CriteriaBase, params)
	}
	return chain, nil
}

func (g *RequestGenerator) getDailySeason(ctx context.Context, c *indexersearch.DailySeasonCriteria) (*PageableRequestChain, error) {
	chain := NewPageableRequestChain()
	caps, err := g.caps(ctx)
	if err != nil {
		return chain, err
	}
	if !supportsEpisodeSearch(caps) || (!supportsTvText(caps) && !supportsTvIDs(caps)) {
		return chain, nil
	}
	params := fmt.Sprintf("&season=%d", c.Year)
	if c.SearchMode.HasFlag(indexersearch.SearchModeSearchID) || c.SearchMode == indexersearch.SearchModeDefault {
		g.addTvIDPageable(chain, caps, g.Settings.Categories, c.CriteriaBase, params)
	}
	if c.SearchMode.HasFlag(indexersearch.SearchModeSearchTitle) {
		g.addTitlePageable(chain, caps, g.Settings.Categories, c.CriteriaBase, params)
	}
	chain.AddTier()
	if c.SearchMode == indexersearch.SearchModeDefault {
		g.addTitlePageable(chain, caps, g.Settings.Categories, c.CriteriaBase, params)
	}
	return chain, nil
}

func (g *RequestGenerator) getAnimeEpisode(ctx context.Context, c *indexersearch.AnimeEpisodeCriteria) (*PageableRequestChain, error) {
	chain := NewPageableRequestChain()
	caps, err := g.caps(ctx)
	if err != nil {
		return chain, err
	}
	if !supportsSearch(caps) {
		return chain, nil
	}

	g.addTvIDPageable(chain, caps, g.Settings.AnimeCategories, c.CriteriaBase,
		fmt.Sprintf("&q=%02d", c.AbsoluteEpisodeNumber))

	includeStandard := g.Settings.AnimeStandardFormatSearch && c.SeasonNumber > 0 && c.EpisodeNumber > 0
	if includeStandard && supportsEpisodeSearch(caps) {
		g.addTvIDPageable(chain, caps, g.Settings.AnimeCategories, c.CriteriaBase,
			fmt.Sprintf("&season=%s&ep=%d", newznabifySeasonNumber(c.SeasonNumber), c.EpisodeNumber))
	}

	queryTitles := c.CleanSceneTitles()
	if caps.TextSearchEngine == "raw" {
		queryTitles = c.AllSceneTitles()
	}

	for _, qt := range queryTitles {
		chain.Add(g.pagedRequests(g.MaxPages, g.Settings.AnimeCategories, "search",
			fmt.Sprintf("&q=%s+%02d", newznabifyTitle(qt), c.AbsoluteEpisodeNumber)))
		if includeStandard && supportsEpisodeSearch(caps) {
			chain.Add(g.pagedRequests(g.MaxPages, g.Settings.AnimeCategories, "tvsearch",
				fmt.Sprintf("&q=%s&season=%s&ep=%d", newznabifyTitle(qt), newznabifySeasonNumber(c.SeasonNumber), c.EpisodeNumber)))
		}
	}
	return chain, nil
}

func (g *RequestGenerator) getAnimeSeason(ctx context.Context, c *indexersearch.AnimeSeasonCriteria) (*PageableRequestChain, error) {
	chain := NewPageableRequestChain()
	caps, err := g.caps(ctx)
	if err != nil {
		return chain, err
	}
	if !supportsSearch(caps) || !g.Settings.AnimeStandardFormatSearch || c.SeasonNumber <= 0 {
		return chain, nil
	}

	g.addTvIDPageable(chain, caps, g.Settings.AnimeCategories, c.CriteriaBase,
		fmt.Sprintf("&season=%s", newznabifySeasonNumber(c.SeasonNumber)))

	queryTitles := c.CleanSceneTitles()
	if caps.TextSearchEngine == "raw" {
		queryTitles = c.AllSceneTitles()
	}
	for _, qt := range queryTitles {
		chain.Add(g.pagedRequests(g.MaxPages, g.Settings.AnimeCategories, "tvsearch",
			fmt.Sprintf("&q=%s&season=%s", newznabifyTitle(qt), newznabifySeasonNumber(c.SeasonNumber))))
	}
	return chain, nil
}

func (g *RequestGenerator) getSpecial(ctx context.Context, c *indexersearch.SpecialEpisodeCriteria) (*PageableRequestChain, error) {
	chain := NewPageableRequestChain()
	caps, err := g.caps(ctx)
	if err != nil {
		return chain, err
	}
	if !supportsSearch(caps) {
		return chain, nil
	}
	cats := g.searchCategories(c.Series)
	for _, qt := range c.EpisodeQueryTitles {
		// C# replaces '+' with ' ' and then UrlEncodes; mirror that exactly.
		query := strings.ReplaceAll(qt, "+", " ")
		query = url.QueryEscape(query)
		chain.Add(g.pagedRequests(g.MaxPages, cats, "search", "&q="+query))
	}
	return chain, nil
}

// --- Helpers ---

// addTvIDPageable ports AddTvIdPageableRequests at C# :497-574.
func (g *RequestGenerator) addTvIDPageable(chain *PageableRequestChain, caps *Capabilities, cats []int, base indexersearch.CriteriaBase, params string) {
	s := base.Series
	if s == nil {
		return
	}
	includeTvdb := has(caps.SupportedTvSearchParameters, "tvdbid") && s.TvdbID > 0
	includeImdb := has(caps.SupportedTvSearchParameters, "imdbid") && strings.TrimSpace(s.ImdbID) != ""
	includeTvRage := has(caps.SupportedTvSearchParameters, "rid") && s.TvRageID > 0
	includeTvMaze := has(caps.SupportedTvSearchParameters, "tvmazeid") && s.TvMazeID > 0
	includeTmdb := has(caps.SupportedTvSearchParameters, "tmdbid") && s.TmdbID > 0

	if caps.SupportsAggregateIDSearch && (includeTvdb || includeTvRage || includeTvMaze || includeTmdb) {
		// "ImDb" intentionally NOT in the aggregate trigger — mirrors C# :505.
		ids := ""
		if includeTvdb {
			ids += fmt.Sprintf("&tvdbid=%d", s.TvdbID)
		}
		if includeImdb {
			ids += "&imdbid=" + s.ImdbID
		}
		if includeTvRage {
			ids += fmt.Sprintf("&rid=%d", s.TvRageID)
		}
		if includeTvMaze {
			ids += fmt.Sprintf("&tvmazeid=%d", s.TvMazeID)
		}
		if includeTmdb {
			ids += fmt.Sprintf("&tmdbid=%d", s.TmdbID)
		}
		chain.Add(g.pagedRequests(g.MaxPages, cats, "tvsearch", ids+params))
		return
	}

	switch {
	case includeTvdb:
		chain.Add(g.pagedRequests(g.MaxPages, cats, "tvsearch",
			fmt.Sprintf("&tvdbid=%d%s", s.TvdbID, params)))
	case includeImdb:
		chain.Add(g.pagedRequests(g.MaxPages, cats, "tvsearch",
			fmt.Sprintf("&imdbid=%s%s", s.ImdbID, params)))
	case includeTvRage:
		chain.Add(g.pagedRequests(g.MaxPages, cats, "tvsearch",
			fmt.Sprintf("&rid=%d%s", s.TvRageID, params)))
	case includeTvMaze:
		chain.Add(g.pagedRequests(g.MaxPages, cats, "tvsearch",
			fmt.Sprintf("&tvmazeid=%d%s", s.TvMazeID, params)))
	case includeTmdb:
		chain.Add(g.pagedRequests(g.MaxPages, cats, "tvsearch",
			fmt.Sprintf("&tmdbid=%d%s", s.TmdbID, params)))
	}
}

// addTitlePageable ports AddTitlePageableRequests at C# :576-599. Note:
// the C# code reads `Settings.Categories` here directly even though the
// caller passes a `categories` argument — that's a C# quirk we mirror
// faithfully (the cats arg is unused for title searches; AnimeCategories
// callers don't reach this path because anime uses the `q=` query directly).
func (g *RequestGenerator) addTitlePageable(chain *PageableRequestChain, caps *Capabilities, _ []int, base indexersearch.CriteriaBase, params string) {
	if has(caps.SupportedTvSearchParameters, "title") {
		for _, term := range base.SceneTitles {
			chain.Add(g.pagedRequests(g.MaxPages, g.Settings.Categories, "tvsearch",
				fmt.Sprintf("&title=%s%s", url.QueryEscape(term), params)))
		}
		return
	}
	if !has(caps.SupportedTvSearchParameters, "q") {
		return
	}
	queryTitles := base.CleanSceneTitles()
	if caps.TvTextSearchEngine == "raw" {
		queryTitles = base.AllSceneTitles()
	}
	for _, qt := range queryTitles {
		chain.Add(g.pagedRequests(g.MaxPages, g.Settings.Categories, "tvsearch",
			fmt.Sprintf("&q=%s%s", newznabifyTitle(qt), params)))
	}
}

// pagedRequests builds the offset-paged request stream for one
// (searchType, params) pair. Mirrors C# GetPagedRequests at :601-629.
//
// Empty categories short-circuits to nil (C# `yield break`).
// PageSize=0 yields a single un-paged request.
func (g *RequestGenerator) pagedRequests(maxPages int, categories []int, searchType, params string) PageableRequest {
	if len(categories) == 0 {
		return nil
	}
	categoriesQuery := categoriesParam(categories)

	baseURL := fmt.Sprintf("%s%s?t=%s&cat=%s&extended=1%s",
		strings.TrimRight(g.Settings.BaseUrl, "/"),
		strings.TrimRight(g.Settings.ApiPath, "/"),
		searchType,
		categoriesQuery,
		g.Settings.AdditionalParameters,
	)
	if strings.TrimSpace(g.Settings.ApiKey) != "" {
		baseURL += "&apikey=" + g.Settings.ApiKey
	}

	if g.PageSize == 0 {
		req, err := newznabRequest(baseURL + params)
		if err != nil {
			return nil
		}
		return PageableRequest{req}
	}

	out := make(PageableRequest, 0, maxPages)
	for page := 0; page < maxPages; page++ {
		u := fmt.Sprintf("%s&offset=%d&limit=%d%s", baseURL, page*g.PageSize, g.PageSize, params)
		req, err := newznabRequest(u)
		if err != nil {
			continue
		}
		out = append(out, req)
	}
	return out
}

// newznabRequest builds an httpclient.Request with Accept=RSS.
func newznabRequest(rawURL string) (*httpclient.Request, error) {
	return httpclient.NewRequest(rawURL, httpclient.AcceptRSS)
}

// categoriesParam joins ints with commas and de-dupes. Mirrors the
// `categories.Distinct()` step at C# :608.
func categoriesParam(categories []int) string {
	seen := make(map[int]struct{}, len(categories))
	uniq := make([]int, 0, len(categories))
	for _, c := range categories {
		if _, ok := seen[c]; !ok {
			seen[c] = struct{}{}
			uniq = append(uniq, c)
		}
	}
	parts := make([]string, len(uniq))
	for i, c := range uniq {
		parts[i] = fmt.Sprint(c)
	}
	return strings.Join(parts, ",")
}

// mergeCategories returns Categories ∪ AnimeCategories deduped. Used by
// GetRecentRequests when both lists feed the same RSS pull.
func mergeCategories(a, b []int) []int {
	seen := make(map[int]struct{}, len(a)+len(b))
	out := make([]int, 0, len(a)+len(b))
	for _, c := range a {
		if _, ok := seen[c]; !ok {
			seen[c] = struct{}{}
			out = append(out, c)
		}
	}
	for _, c := range b {
		if _, ok := seen[c]; !ok {
			seen[c] = struct{}{}
			out = append(out, c)
		}
	}
	sort.Ints(out)
	return out
}

// has reports whether s contains v.
func has(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

// searchCategories ports GetSearchCategories at C# :643-648 — anime series
// use AnimeCategories; everything else uses Categories.
func (g *RequestGenerator) searchCategories(s *series.Series) []int {
	if s != nil && s.SeriesType == series.SeriesTypeAnime {
		return g.Settings.AnimeCategories
	}
	return g.Settings.Categories
}

// supportsSearch reports whether the indexer advertises basic `?t=search&q=`.
// C# cite: :35-44.
func supportsSearch(caps *Capabilities) bool {
	return caps.SupportedSearchParameters != nil && has(caps.SupportedSearchParameters, "q")
}

// supportsTvText reports whether the indexer advertises q OR title in
// tv-search. C# cite: :70-77.
func supportsTvText(caps *Capabilities) bool {
	if caps.SupportedTvSearchParameters == nil {
		return false
	}
	return has(caps.SupportedTvSearchParameters, "q") || has(caps.SupportedTvSearchParameters, "title")
}

// supportsTvIDs reports whether ANY ID search param is advertised.
// C# cite: :134-141.
func supportsTvIDs(caps *Capabilities) bool {
	if caps.SupportedTvSearchParameters == nil {
		return false
	}
	for _, p := range []string{"tvdbid", "imdbid", "rid", "tvmazeid", "tmdbid"} {
		if has(caps.SupportedTvSearchParameters, p) {
			return true
		}
	}
	return false
}

// supportsSeasonSearch reports whether tv-search advertises `season`.
func supportsSeasonSearch(caps *Capabilities) bool {
	return caps.SupportedTvSearchParameters != nil && has(caps.SupportedTvSearchParameters, "season")
}

// supportsEpisodeSearch reports whether tv-search advertises both `season`
// and `ep`. C# cite: :154-164.
func supportsEpisodeSearch(caps *Capabilities) bool {
	if caps.SupportedTvSearchParameters == nil {
		return false
	}
	return has(caps.SupportedTvSearchParameters, "season") && has(caps.SupportedTvSearchParameters, "ep")
}

// newznabifySeasonNumber ports the C# helper at :637-641. Workaround for
// NNTMux treating season=0 as null.
func newznabifySeasonNumber(seasonNumber int) string {
	if seasonNumber == 0 {
		return "00"
	}
	return fmt.Sprint(seasonNumber)
}

// newznabifyTitle replaces '+' with ' ' then URL-encodes. Mirrors
// NewsnabifyTitle at C# :631-635 (yes, the C# function name has a typo).
//
// Encoding choice: the C# code uses Uri.EscapeDataString which produces
// %20 for spaces. Go's url.QueryEscape produces +. We post-process so
// the wire bytes match the C# output (the test corpus asserts %20).
func newznabifyTitle(title string) string {
	title = strings.ReplaceAll(title, "+", " ")
	encoded := url.QueryEscape(title)
	return strings.ReplaceAll(encoded, "+", "%20")
}
