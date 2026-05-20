package broadcasthenet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the BroadcastheNet Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/BroadcastheNet/BroadcastheNet.cs:10-43`.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition

	// lastRecentTorrentID mirrors the BTN-specific property at
	// `BroadcastheNetRequestGenerator.cs:14` — when > 0, FetchRecent's
	// tier 1 narrows the recent-fetch query to `id >= lastID-100`
	// (the -100 is C#'s overlap window guarding against deletions or
	// reordering since the last sync). The composition root is meant
	// to populate it from the indexerstatus persistence — see C#
	// `BroadcastheNet.cs:29-34` for the GetLastRssSyncReleaseInfo +
	// "BTN-<id>" parse. That wiring (capturing the max TorrentID
	// after each fetch + persisting it back as a Guid-shaped
	// ReleaseInfo) is still deferred; until it lands, this field
	// stays zero and FetchRecent falls through to the age-only path
	// — behaviour-equivalent to pre-J158.
	lastRecentTorrentID int
}

// NewIndexer constructs a BroadcastheNet indexer.
func NewIndexer(client httpclient.Client) *Indexer {
	return &Indexer{HTTPClient: client}
}

// Name returns the user-visible class name.
func (*Indexer) Name() string { return DisplayName }

// ConfigContract returns the Settings class identifier.
func (*Indexer) ConfigContract() string { return ConfigContract }

// Message returns nil.
func (*Indexer) Message() *provider.Message { return nil }

// DefaultDefinitions returns a single pre-canned definition.
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

// LastRecentTorrentID returns the most recently seen BTN TorrentID
// the indexer is using as a fast-path watermark for recent fetches.
// Zero means no watermark known and FetchRecent will only walk the
// 24-hour age tier.
func (i *Indexer) LastRecentTorrentID() int { return i.lastRecentTorrentID }

// SetLastRecentTorrentID seeds the fast-path watermark used by
// FetchRecent's tier 1. Mirrors the C# property setter at
// `BroadcastheNetRequestGenerator.cs:14` — the composition root or a
// follow-up indexerstatus-populator slice will call this before
// invoking FetchRecent. Negative or zero values are tolerated (treated
// as "no watermark").
func (i *Indexer) SetLastRecentTorrentID(id int) { i.lastRecentTorrentID = id }

// Test runs Settings.Validate. Same validate-only posture as the
// sibling indexers.
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

// FetchRecent issues paginated `getTorrents` JSON-RPC calls. C# cite:
// `BroadcastheNetRequestGenerator.cs:22-40`.
//
// Two tiers, walked in order with short-circuit on the first tier that
// produces any results — mirrors C# `IndexerPageableRequestChain.Add`
// (tier 1) + `.AddTier` (tier 2) semantics:
//
//   - TIER 1 (only when LastRecentTorrentID > 0): `{Id: ">=<lastID-100>"}`.
//     The -100 is C#'s overlap window — guards against deletions or
//     reordering since the last sync.
//   - TIER 2 (always present): `{Age: "<=86400"}` 24-hour fallback.
//
// Within a tier, fetchPaged walks up to [maxPages] pages, Guid-dedupes
// across pages, and short-circuits as soon as a page returns zero new
// torrents.
//
// The populate-from-indexerstatus side of the C# wiring at
// `BroadcastheNet.cs:29-34` (read GetLastRssSyncReleaseInfo, parse
// "BTN-<id>" out of its Guid) is still deferred — the field defaults
// to zero so callers that don't set it get the age-only behaviour
// identical to pre-J158.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("broadcasthenet: no HTTPClient configured")
	}
	for _, q := range buildRecentTiers(i.lastRecentTorrentID) {
		if rels := i.fetchPaged(ctx, settings, q); len(rels) > 0 {
			return rels, nil
		}
	}
	return []*parser.TorrentInfo{}, nil
}

// buildRecentTiers returns the per-tier query slice (each tier is a
// single query, paginated internally by fetchPaged). When lastID > 0
// the id-watermark tier prepends the age tier.
func buildRecentTiers(lastID int) []*torrentQuery {
	tiers := make([]*torrentQuery, 0, 2)
	if lastID > 0 {
		tiers = append(tiers, &torrentQuery{ID: fmt.Sprintf(">=%d", lastID-100)})
	}
	tiers = append(tiers, &torrentQuery{Age: "<=86400"})
	return tiers
}

// FetchSearch dispatches the typed criteria through the per-type
// search routing at C# `BroadcastheNetRequestGenerator.cs:42-225`.
// Slice 5.J157 closes the J156 deferral.
//
// All search overloads gate on `Series.TvdbID != 0 || Series.TvRageID
// != 0` (C# `AddSeriesSearchParameters` at `:227-243` returns false
// otherwise) — when both are zero the overload yields empty without
// HTTP work.
//
// Per criteria type (the BTN convention is %-wildcarded Name patterns
// per Category):
//
//   - SingleEpisodeCriteria: per-episode, single tier, Category=Episode
//     Name=`S<ss>%E<ee>%`.
//   - SeasonCriteria: per-distinct-season, single tier, dual request
//     (Category=Season Name=`Season N%` + Category=Episode
//     Name=`S<ss>E%`).
//   - DailyEpisodeCriteria: TIER 1 — Category=Episode
//     Name=`yyyy.MM.dd`. TIER 2 — per-episode Category=Episode
//     Name=`S<ss>E<ee>`.
//   - DailySeasonCriteria: TIER 1 — Category=Episode Name=`<year>%`.
//     TIER 2 — per-episode Category=Episode Name=`S<ss>E<ee>`.
//   - AnimeEpisodeCriteria: single tier, per-episode S/E + per-distinct-
//     season Season-N% (both shapes in one tier — C# `:152-170`).
//   - AnimeSeasonCriteria: per-distinct-season Season-N% + S<ss>E%
//     dual request (single tier).
//   - SpecialEpisodeCriteria: per non-empty-titled episode,
//     Category=Episode Name=`%<clean-scene-title>%`.
//
// Daily variants are the only criteria that AddTier(); all others
// pack into a single implicit tier. Each per-criteria URL paginates
// across MaxPages internally (via fetchPaged).
//
// Across the per-criteria fan-out, results are accumulated +
// Guid-deduped. For Daily / DailySeason, the tier-walk short-
// circuits once a tier produces any results.
func (i *Indexer) FetchSearch(ctx context.Context, criteria any) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	tiers := buildSearchTiers(criteria)
	if len(tiers) == 0 {
		return []*parser.TorrentInfo{}, nil
	}
	if i.HTTPClient == nil {
		return nil, errors.New("broadcasthenet: no HTTPClient configured")
	}

	for _, queries := range tiers {
		dedup := make(map[string]struct{}, len(queries)*pageSize)
		var acc []*parser.TorrentInfo
		for _, q := range queries {
			for _, r := range i.fetchPaged(ctx, settings, q) {
				if _, dup := dedup[r.Guid]; dup {
					continue
				}
				dedup[r.Guid] = struct{}{}
				acc = append(acc, r)
			}
		}
		if len(acc) > 0 {
			return acc, nil
		}
	}
	return []*parser.TorrentInfo{}, nil
}

// buildSearchTiers dispatches per criteria type and returns
// `[][]*torrentQuery` — outer slice = tiers walked in order,
// inner slice = queries within a tier (each paginated internally).
func buildSearchTiers(criteria any) [][]*torrentQuery {
	switch c := criteria.(type) {
	case *indexersearch.SingleEpisodeCriteria:
		base := newSeriesQuery(&c.CriteriaBase)
		if base == nil {
			return nil
		}
		var tier []*torrentQuery
		for _, ep := range c.Episodes {
			q := cloneQuery(base)
			q.Category = "Episode"
			q.Name = fmt.Sprintf("S%02d%%E%02d%%", ep.SeasonNumber, ep.EpisodeNumber)
			tier = append(tier, q)
		}
		if len(tier) == 0 {
			return nil
		}
		return [][]*torrentQuery{tier}
	case *indexersearch.SeasonCriteria:
		base := newSeriesQuery(&c.CriteriaBase)
		if base == nil {
			return nil
		}
		seasons := distinctSeasonNumbers(c.Episodes)
		var tier []*torrentQuery
		for _, n := range seasons {
			seasonQ := cloneQuery(base)
			seasonQ.Category = "Season"
			seasonQ.Name = fmt.Sprintf("Season %d%%", n)
			tier = append(tier, seasonQ)

			epQ := cloneQuery(base)
			epQ.Category = "Episode"
			epQ.Name = fmt.Sprintf("S%02dE%%", n)
			tier = append(tier, epQ)
		}
		if len(tier) == 0 {
			return nil
		}
		return [][]*torrentQuery{tier}
	case *indexersearch.DailyEpisodeCriteria:
		base := newSeriesQuery(&c.CriteriaBase)
		if base == nil {
			return nil
		}
		tier1 := []*torrentQuery{cloneQueryWith(base, "Episode", c.AirDate.UTC().Format("2006.01.02"))}
		var tier2 []*torrentQuery
		for _, ep := range c.Episodes {
			tier2 = append(tier2, cloneQueryWith(base, "Episode",
				fmt.Sprintf("S%02dE%02d", ep.SeasonNumber, ep.EpisodeNumber)))
		}
		tiers := [][]*torrentQuery{tier1}
		if len(tier2) > 0 {
			tiers = append(tiers, tier2)
		}
		return tiers
	case *indexersearch.DailySeasonCriteria:
		base := newSeriesQuery(&c.CriteriaBase)
		if base == nil {
			return nil
		}
		tier1 := []*torrentQuery{cloneQueryWith(base, "Episode", fmt.Sprintf("%d%%", c.Year))}
		var tier2 []*torrentQuery
		for _, ep := range c.Episodes {
			tier2 = append(tier2, cloneQueryWith(base, "Episode",
				fmt.Sprintf("S%02dE%02d", ep.SeasonNumber, ep.EpisodeNumber)))
		}
		tiers := [][]*torrentQuery{tier1}
		if len(tier2) > 0 {
			tiers = append(tiers, tier2)
		}
		return tiers
	case *indexersearch.AnimeEpisodeCriteria:
		base := newSeriesQuery(&c.CriteriaBase)
		if base == nil {
			return nil
		}
		var tier []*torrentQuery
		for _, ep := range c.Episodes {
			tier = append(tier, cloneQueryWith(base, "Episode",
				fmt.Sprintf("S%02dE%02d", ep.SeasonNumber, ep.EpisodeNumber)))
		}
		for _, n := range distinctSeasonNumbers(c.Episodes) {
			tier = append(tier, cloneQueryWith(base, "Season", fmt.Sprintf("Season %d%%", n)))
		}
		if len(tier) == 0 {
			return nil
		}
		return [][]*torrentQuery{tier}
	case *indexersearch.AnimeSeasonCriteria:
		base := newSeriesQuery(&c.CriteriaBase)
		if base == nil {
			return nil
		}
		var tier []*torrentQuery
		for _, n := range distinctSeasonNumbers(c.Episodes) {
			tier = append(tier, cloneQueryWith(base, "Season", fmt.Sprintf("Season %d%%", n)))
			tier = append(tier, cloneQueryWith(base, "Episode", fmt.Sprintf("S%02dE%%", n)))
		}
		if len(tier) == 0 {
			return nil
		}
		return [][]*torrentQuery{tier}
	case *indexersearch.SpecialEpisodeCriteria:
		base := newSeriesQuery(&c.CriteriaBase)
		if base == nil {
			return nil
		}
		var tier []*torrentQuery
		for _, ep := range c.Episodes {
			if strings.TrimSpace(ep.Title) == "" {
				continue
			}
			cleanTitle := decisionengine.GetCleanSceneTitle(ep.Title)
			tier = append(tier, cloneQueryWith(base, "Episode", "%"+cleanTitle+"%"))
		}
		if len(tier) == 0 {
			return nil
		}
		return [][]*torrentQuery{tier}
	default:
		return nil
	}
}

// newSeriesQuery returns a base torrentQuery with Tvdb (preferred)
// or Tvrage set from the search criteria's Series. Returns nil when
// neither id is set — matches C# `AddSeriesSearchParameters` at
// `:227-243`.
func newSeriesQuery(sc *decisionengine.SearchCriteria) *torrentQuery {
	if sc == nil || sc.Series == nil {
		return nil
	}
	if sc.Series.TvdbID != 0 {
		return &torrentQuery{Tvdb: strconv.Itoa(sc.Series.TvdbID)}
	}
	if sc.Series.TvRageID != 0 {
		return &torrentQuery{Tvrage: strconv.Itoa(sc.Series.TvRageID)}
	}
	return nil
}

// cloneQuery duplicates a torrentQuery — mirrors C# `Clone()` at
// `BroadcastheNetTorrentQuery.cs:35-38` (MemberwiseClone).
func cloneQuery(q *torrentQuery) *torrentQuery {
	clone := *q
	return &clone
}

// cloneQueryWith is a thin helper for the common shape "clone +
// Category + Name". Saves three lines per callsite.
func cloneQueryWith(q *torrentQuery, category, name string) *torrentQuery {
	c := cloneQuery(q)
	c.Category = category
	c.Name = name
	return c
}

// distinctSeasonNumbers walks the supplied episodes and returns a
// dedupe-preserved-order slice of season numbers. Mirrors C# LINQ
// `.Select(e => e.SeasonNumber).Distinct()`.
func distinctSeasonNumbers(eps []episodes.Episode) []int {
	seen := make(map[int]struct{}, len(eps))
	out := make([]int, 0, len(eps))
	for _, e := range eps {
		if _, ok := seen[e.SeasonNumber]; ok {
			continue
		}
		seen[e.SeasonNumber] = struct{}{}
		out = append(out, e.SeasonNumber)
	}
	return out
}

// fetchPaged walks the pagination loop. Mirrors C#
// `GetPagedRequests` at `:245-257` — increments `params[3]` (offset)
// by PageSize across MaxPages iterations.
//
// Per-page failures are swallowed (matches the C# tree's per-page
// try/catch). Accumulated results from prior pages still surface as
// the returned slice — there is no error path out, so the signature
// returns only the result slice (always non-nil).
func (i *Indexer) fetchPaged(ctx context.Context, settings *Settings, q *torrentQuery) []*parser.TorrentInfo {
	dedup := make(map[string]struct{}, maxPages*pageSize)
	var all []*parser.TorrentInfo
	for page := 0; page < maxPages; page++ {
		offset := page * pageSize
		rels, err := i.callRPC(ctx, settings, q, offset)
		if err != nil {
			// Per-page failures swallowed — matches the C# tree's
			// per-page try/catch. The accumulated results from prior
			// pages still surface.
			break
		}
		newCount := 0
		for _, r := range rels {
			if _, dup := dedup[r.Guid]; dup {
				continue
			}
			dedup[r.Guid] = struct{}{}
			all = append(all, r)
			newCount++
		}
		// Empty page (or all-duplicates) → no more pages to walk.
		if newCount == 0 {
			break
		}
	}
	if all == nil {
		all = []*parser.TorrentInfo{}
	}
	return all
}

// callRPC issues one JSON-RPC POST and parses the response.
func (i *Indexer) callRPC(ctx context.Context, settings *Settings, q *torrentQuery, offset int) ([]*parser.TorrentInfo, error) {
	apiURL := strings.TrimRight(strings.TrimSpace(settings.BaseUrl), "/")
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "getTorrents",
		Params:  []any{strings.TrimSpace(settings.ApiKey), q, pageSize, offset},
		ID:      1,
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("broadcasthenet: marshal RPC request: %w", err)
	}
	req, err := httpclient.NewRequest(apiURL, httpclient.AcceptJSON)
	if err != nil {
		return nil, fmt.Errorf("broadcasthenet: build request: %w", err)
	}
	req.Method = "POST"
	req.Headers.SetContentType("application/json")
	req.SetContentBytes(body)
	resp, err := i.HTTPClient.Post(ctx, req)
	if err != nil {
		return nil, err
	}
	contentType := ""
	if resp.Headers != nil {
		contentType = resp.Headers.ContentType()
	}
	scheme := "https"
	if strings.HasPrefix(apiURL, "http:") {
		scheme = "http"
	}
	return parseResponse(
		[]byte(resp.Content()),
		resp.StatusCode,
		contentType,
		scheme,
		settings,
		i.bindingName(),
		i.bindingID(),
	)
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("broadcasthenet: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("broadcasthenet: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("broadcasthenet: Definition.Settings is %T, want *broadcasthenet.Settings", i.definition.Settings)
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

var _ provider.Provider = (*Indexer)(nil)

// Constructor returns a fresh Settings.
func Constructor() any { return NewSettings() }

func init() {
	if err := provider.Default().Replace(provider.FamilyIndexer,
		provider.Key{Impl: Implementation, Contract: ConfigContract},
		Constructor); err != nil {
		panic(fmt.Errorf("broadcasthenet: register constructor: %w", err))
	}
}
