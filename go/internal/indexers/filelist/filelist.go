package filelist

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the FileList Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/FileList/FileList.cs:9-30`.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs a FileList indexer.
func NewIndexer(client httpclient.Client) *Indexer {
	return &Indexer{HTTPClient: client}
}

// Name returns the user-visible class name.
func (*Indexer) Name() string { return DisplayName }

// ConfigContract returns the Settings class identifier.
func (*Indexer) ConfigContract() string { return ConfigContract }

// Message returns nil.
func (*Indexer) Message() *provider.Message { return nil }

// DefaultDefinitions returns a single pre-canned definition. The
// user must populate Username + Passkey at Create time.
func (i *Indexer) DefaultDefinitions() []*provider.Definition {
	return []*provider.Definition{
		{
			Name:           DisplayName,
			Implementation: Implementation,
			ConfigContract: ConfigContract,
			Enable:         false,
			Settings:       NewSettings(),
		},
	}
}

// Definition returns the bound Definition.
func (i *Indexer) Definition() *provider.Definition { return i.definition }

// SetDefinition binds the Definition.
func (i *Indexer) SetDefinition(d *provider.Definition) { i.definition = d }

// Test runs Settings.Validate. Same validate-only posture as the
// sibling indexers — no live caps probe.
func (i *Indexer) Test(_ context.Context) provider.ValidationResult {
	settings, err := i.settings()
	if err != nil {
		return provider.ValidationResult{Errors: []provider.ValidationFailure{
			provider.NewValidationFailure("", err.Error()),
		}}
	}
	return settings.Validate()
}

// RequestAction returns nil for everything.
func (i *Indexer) RequestAction(_ string, _ map[string]string) (any, error) {
	return nil, nil
}

// FetchRecent issues the no-search `latest-torrents` API call.
// C# cite: `FileListRequestGenerator.cs:15-22` — single request
// with the Categories+AnimeCategories union, no per-criteria
// parameters.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("filelist: no HTTPClient configured")
	}
	cats := unionInts(settings.Categories, settings.AnimeCategories)
	if len(cats) == 0 {
		// C# `GetRequest` at :140-143 yield-breaks when categories
		// empty; mirror by returning empty without HTTP.
		return []*parser.TorrentInfo{}, nil
	}
	feedURL := buildAPIURL(settings, "latest-torrents", cats, "")
	return i.fetch(ctx, settings, feedURL)
}

// FetchSearch dispatches the typed criteria through the per-type
// search-URL routing at C# `FileListRequestGenerator.cs:24-120`.
// Slice 5.J153 closes the J152 deferral.
//
// Per criteria type (`searchType = "search-torrents"`):
//   - SingleEpisodeCriteria + SeasonCriteria: dual-flow IMDB +
//     Name search, gated on the C# SearchMode flag:
//   - SearchID || Default → AddImdbRequests (tier 0)
//   - SearchTitle → AddNameRequests (tier 0)
//   - then `pageableRequests.AddTier()` to bump tier
//   - Default → AddNameRequests (tier 1, fallback)
//   - AnimeEpisodeCriteria: 4 tiers — IMDB at abs / IMDB at per-
//     episode / Name at abs / Name at per-episode. Uses
//     AnimeCategories (not Categories).
//   - AnimeSeasonCriteria: 2 tiers — IMDB / Name. AnimeCategories.
//   - Daily / DailySeason / Special / unknown: empty.
//
// # Tier semantics
//
// The C# tree's IndexerPageableRequestChain walks tiers in order,
// short-circuiting once a tier produces any results. The Go port
// mirrors via [filelist.runTiers] which fetches each URL in a tier,
// dedupes by Guid, and returns the accumulated set on the first
// non-empty tier. Mirrors the existing torznab.runChain shape
// (the J147-era IPTorrents arc settled on a simpler one-tier
// model — FileList is the first indexer in the port to actually
// exercise the tier-short-circuit machinery).
//
// AddImdbRequests at C# `:122-128` only emits a request when the
// series ImdbId is non-empty. AddNameRequests at C# `:130-136`
// iterates `searchCriteria.SceneTitles` (NOT CleanSceneTitles —
// FileList passes raw scene titles through Uri.EscapeDataString).
func (i *Indexer) FetchSearch(ctx context.Context, criteria any) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	tiers := buildSearchTiers(settings, criteria)
	if len(tiers) == 0 {
		// Short-circuit BEFORE the HTTPClient check (same posture as
		// the Nyaa / Fanzub close-outs).
		return []*parser.TorrentInfo{}, nil
	}
	if i.HTTPClient == nil {
		return nil, errors.New("filelist: no HTTPClient configured")
	}
	return i.runTiers(ctx, settings, tiers), nil
}

// runTiers walks tier-list-of-URLs in order. Each tier accumulates
// dedupe-by-Guid; the first non-empty tier's results are returned.
// Per-URL fetch / parse failures are swallowed within a tier
// (matches the C# tree's per-request try/catch posture).
func (i *Indexer) runTiers(ctx context.Context, settings *Settings, tiers [][]string) []*parser.TorrentInfo {
	for _, urls := range tiers {
		dedup := make(map[string]struct{}, len(urls)*16)
		var acc []*parser.TorrentInfo
		for _, u := range urls {
			rels, err := i.fetch(ctx, settings, u)
			if err != nil {
				continue
			}
			for _, r := range rels {
				if _, dup := dedup[r.Guid]; dup {
					continue
				}
				dedup[r.Guid] = struct{}{}
				acc = append(acc, r)
			}
		}
		if len(acc) > 0 {
			return acc
		}
	}
	return []*parser.TorrentInfo{}
}

// buildSearchTiers dispatches per criteria type and returns the
// tier-of-URLs structure that runTiers walks.
//
// Categories source for each criteria type:
//   - SingleEpisode / Season: Settings.Categories
//   - AnimeEpisode / AnimeSeason: Settings.AnimeCategories
//
// Category sets that are empty produce zero URLs (C# yield-break
// at `:140-143`).
func buildSearchTiers(s *Settings, criteria any) [][]string {
	switch c := criteria.(type) {
	case *indexersearch.SingleEpisodeCriteria:
		params := fmt.Sprintf("&season=%d&episode=%d", c.SeasonNumber, c.EpisodeNumber)
		return standardSearchTiers(s, &c.CriteriaBase, s.Categories, params)
	case *indexersearch.SeasonCriteria:
		params := fmt.Sprintf("&season=%d", c.SeasonNumber)
		return standardSearchTiers(s, &c.CriteriaBase, s.Categories, params)
	case *indexersearch.AnimeEpisodeCriteria:
		return animeEpisodeSearchTiers(s, c)
	case *indexersearch.AnimeSeasonCriteria:
		return animeSeasonSearchTiers(s, c)
	default:
		// DailyEpisode + DailySeason + SpecialEpisode + unknown → empty
		// (matches C# overloads at :72-80, :117-120 that all return
		// an empty IndexerPageableRequestChain).
		return nil
	}
}

// standardSearchTiers ports the C# SingleEpisode + Season dispatch.
// Both follow the same shape: optional tier 0 (IMDB and/or Name
// depending on SearchMode flag) + AddTier + optional tier 1 (Name
// fallback when SearchMode is Default).
func standardSearchTiers(s *Settings, sc *decisionengine.SearchCriteria, categories []int, params string) [][]string {
	mode := sc.SearchMode
	isDefault := mode == decisionengine.SearchModeDefault

	var tier0 []string
	if mode.HasFlag(decisionengine.SearchModeSearchID) || isDefault {
		tier0 = append(tier0, imdbRequests(s, sc, categories, params)...)
	}
	if mode.HasFlag(decisionengine.SearchModeSearchTitle) {
		tier0 = append(tier0, nameRequests(s, sc, categories, params)...)
	}

	var tiers [][]string
	if len(tier0) > 0 {
		tiers = append(tiers, tier0)
	}

	if isDefault {
		if tier1 := nameRequests(s, sc, categories, params); len(tier1) > 0 {
			tiers = append(tiers, tier1)
		}
	}
	return tiers
}

// animeEpisodeSearchTiers ports the C# AnimeEpisode 4-tier dispatch
// at `:82-104`.
func animeEpisodeSearchTiers(s *Settings, c *indexersearch.AnimeEpisodeCriteria) [][]string {
	cats := s.AnimeCategories
	absParams := fmt.Sprintf("&season=0&episode=%d", c.AbsoluteEpisodeNumber)

	var tiers [][]string
	// Tier 0: IMDB at abs.
	if t := imdbRequests(s, &c.CriteriaBase, cats, absParams); len(t) > 0 {
		tiers = append(tiers, t)
	}
	// Tier 1: IMDB per-episode.
	var tier1 []string
	for _, ep := range c.Episodes {
		params := fmt.Sprintf("&season=%d&episode=%d", ep.SeasonNumber, ep.EpisodeNumber)
		tier1 = append(tier1, imdbRequests(s, &c.CriteriaBase, cats, params)...)
	}
	if len(tier1) > 0 {
		tiers = append(tiers, tier1)
	}
	// Tier 2: Name at abs.
	if t := nameRequests(s, &c.CriteriaBase, cats, absParams); len(t) > 0 {
		tiers = append(tiers, t)
	}
	// Tier 3: Name per-episode.
	var tier3 []string
	for _, ep := range c.Episodes {
		params := fmt.Sprintf("&season=%d&episode=%d", ep.SeasonNumber, ep.EpisodeNumber)
		tier3 = append(tier3, nameRequests(s, &c.CriteriaBase, cats, params)...)
	}
	if len(tier3) > 0 {
		tiers = append(tiers, tier3)
	}
	return tiers
}

// animeSeasonSearchTiers ports the C# AnimeSeason 2-tier dispatch
// at `:106-115`.
func animeSeasonSearchTiers(s *Settings, c *indexersearch.AnimeSeasonCriteria) [][]string {
	cats := s.AnimeCategories
	params := fmt.Sprintf("&season=%d", c.SeasonNumber)

	var tiers [][]string
	if t := imdbRequests(s, &c.CriteriaBase, cats, params); len(t) > 0 {
		tiers = append(tiers, t)
	}
	if t := nameRequests(s, &c.CriteriaBase, cats, params); len(t) > 0 {
		tiers = append(tiers, t)
	}
	return tiers
}

// imdbRequests mirrors C# AddImdbRequests at `:122-128`. Returns a
// single-URL list when the series has an ImdbId; empty otherwise.
func imdbRequests(s *Settings, sc *decisionengine.SearchCriteria, categories []int, params string) []string {
	if sc.Series == nil || strings.TrimSpace(sc.Series.ImdbID) == "" {
		return nil
	}
	if len(categories) == 0 {
		return nil
	}
	q := "&type=imdb&query=" + url.QueryEscape(strings.TrimSpace(sc.Series.ImdbID)) + params
	return []string{buildAPIURL(s, "search-torrents", categories, q)}
}

// nameRequests mirrors C# AddNameRequests at `:130-136`. One URL
// per SceneTitle (raw, NOT CleanSceneTitles). Title is
// Uri.EscapeDataString'd via url.QueryEscape (which uses the same
// reserved-char set).
func nameRequests(s *Settings, sc *decisionengine.SearchCriteria, categories []int, params string) []string {
	if len(categories) == 0 {
		return nil
	}
	out := make([]string, 0, len(sc.SceneTitles))
	for _, title := range sc.SceneTitles {
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		q := "&type=name&query=" + url.QueryEscape(title) + params
		out = append(out, buildAPIURL(s, "search-torrents", categories, q))
	}
	return out
}

// fetch performs the HTTP request, parses the response, and stamps
// indexer provenance onto each TorrentInfo. Used by FetchRecent
// (single URL) and -- in a follow-up slice -- FetchSearch (per-
// criteria URL list).
func (i *Indexer) fetch(ctx context.Context, settings *Settings, feedURL string) ([]*parser.TorrentInfo, error) {
	req, err := httpclient.NewRequest(feedURL, httpclient.AcceptJSON)
	if err != nil {
		return nil, fmt.Errorf("filelist: build request: %w", err)
	}
	req.BasicAuthUser = strings.TrimSpace(settings.Username)
	req.BasicAuthPass = strings.TrimSpace(settings.Passkey)
	resp, err := i.HTTPClient.Get(ctx, req)
	if err != nil {
		return nil, err
	}
	return parseResponse([]byte(resp.Content()), settings, i.bindingName(), i.bindingID())
}

// buildAPIURL composes the FileList API URL. Mirrors C#
// `FileListRequestGenerator.cs:138-153` — `<BaseUrl>/api.php?action=<...>&category=<csv>[<parameters>]`.
//
// `parameters` is the per-criteria extra query string (already
// `&`-prefixed when non-empty — mirrors the C# format string).
func buildAPIURL(s *Settings, action string, categories []int, parameters string) string {
	base := strings.TrimRight(strings.TrimSpace(s.BaseUrl), "/")
	catCSV := joinInts(categories, ",")
	u := base + "/api.php?action=" + action + "&category=" + catCSV + parameters
	return u
}

// unionInts concatenates two []int slices and dedupes, preserving
// the order of first appearance. Mirrors C#
// `Categories.Concat(AnimeCategories)` followed by the
// `GetRequest`-time `Distinct()` at :145.
func unionInts(a, b []int) []int {
	seen := make(map[int]struct{}, len(a)+len(b))
	out := make([]int, 0, len(a)+len(b))
	for _, v := range a {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range b {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// joinInts builds a comma-separated string from a []int. Used for
// the `category=<csv>` query param.
func joinInts(xs []int, sep string) string {
	if len(xs) == 0 {
		return ""
	}
	parts := make([]string, len(xs))
	for i, v := range xs {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, sep)
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("filelist: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("filelist: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("filelist: Definition.Settings is %T, want *filelist.Settings", i.definition.Settings)
	}
	return s, nil
}

func (i *Indexer) bindingName() string {
	if i.definition != nil && i.definition.Name != "" {
		return i.definition.Name
	}
	return i.Name()
}

func (i *Indexer) bindingID() int {
	if i.definition == nil {
		return 0
	}
	return i.definition.ID
}

// Compile-time interface check.
var _ provider.Provider = (*Indexer)(nil)

// Constructor returns a fresh Settings.
func Constructor() any { return NewSettings() }

func init() {
	if err := provider.Default().Replace(provider.FamilyIndexer,
		provider.Key{Impl: Implementation, Contract: ConfigContract},
		Constructor); err != nil {
		panic(fmt.Errorf("filelist: register constructor: %w", err))
	}
}
