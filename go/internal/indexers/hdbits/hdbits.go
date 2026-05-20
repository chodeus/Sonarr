package hdbits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the HDBits Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/HDBits/HDBits.cs:9-32`.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs an HDBits indexer.
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
// user must populate Username + ApiKey at Create time.
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

// FetchRecent issues the no-filter `/api/torrents` POST. C# cite:
// `HDBitsRequestGenerator.cs:15-22` (GetRecentRequests with empty
// TorrentQuery). The request carries the user's auth + the
// configured Categories/Codecs/Mediums + Limit=100; no search /
// filter params.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("hdbits: no HTTPClient configured")
	}
	q := newBaseQuery(settings)
	return i.fetchQuery(ctx, settings, q)
}

// FetchSearch dispatches the typed criteria through the per-type
// search-routing at C# `HDBitsRequestGenerator.cs:24-137`. Slice
// 5.J155 closes the J154 deferral.
//
// All search overloads gate on `Series.TvdbID != 0` — HDBits is
// a Tvdb-keyed search, no fallback to title/name. When TvdbID is
// missing the C# `TryAddSearchParameters` at `:139-150` returns
// false and the overload yields empty without HTTP work.
//
// Per criteria type:
//   - SingleEpisodeCriteria: per-episode fan-out — Clone the
//     base query, set TvdbInfo.Season+Episode, POST.
//   - SeasonCriteria: per-distinct-season fan-out.
//   - DailyEpisodeCriteria: single request with Search field set
//     to the airdate in `yyyy-MM-dd` form.
//   - DailySeasonCriteria: single request with Search = "<year>-".
//   - AnimeEpisodeCriteria: C# BUG preserved — the C# loop at
//     :31-37 clones+sets but NEVER calls `pageableRequests.Add(...)`
//     so the chain stays empty. The Go port mirrors the bug verbatim
//     (returns no URLs) rather than fixing it; user impact is
//     equivalent to the C# behaviour. Documented as a known
//     divergence so a future audit can decide between "preserve" vs
//     "fix" once upstream resolves it.
//   - AnimeSeasonCriteria: per-distinct-season fan-out (same shape
//     as SeasonCriteria but with AnimeCategories).
//   - SpecialEpisodeCriteria + unknown: empty.
//
// No tier-walking — all HDBits search requests live in a single
// implicit tier (the C# code never calls `pageableRequests.AddTier()`).
// Results are accumulated across all per-criteria requests and
// deduped by Guid.
func (i *Indexer) FetchSearch(ctx context.Context, criteria any) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	queries := buildSearchQueries(settings, criteria)
	if len(queries) == 0 {
		return []*parser.TorrentInfo{}, nil
	}
	if i.HTTPClient == nil {
		return nil, errors.New("hdbits: no HTTPClient configured")
	}

	dedup := make(map[string]struct{}, len(queries)*16)
	var all []*parser.TorrentInfo
	for _, q := range queries {
		rels, err := i.fetchQuery(ctx, settings, q)
		if err != nil {
			continue
		}
		for _, r := range rels {
			if _, dup := dedup[r.Guid]; dup {
				continue
			}
			dedup[r.Guid] = struct{}{}
			all = append(all, r)
		}
	}
	if all == nil {
		all = []*parser.TorrentInfo{}
	}
	return all, nil
}

// buildSearchQueries dispatches per criteria type and returns the
// list of fully-populated torrentQuery values to POST.
func buildSearchQueries(s *Settings, criteria any) []*torrentQuery {
	switch c := criteria.(type) {
	case *indexersearch.SingleEpisodeCriteria:
		tvdbID := seriesTvdbID(&c.CriteriaBase)
		if tvdbID == 0 {
			return nil
		}
		out := make([]*torrentQuery, 0, len(c.Episodes))
		for _, ep := range c.Episodes {
			q := newBaseQuery(s)
			q.TvdbInfo = &tvdbInfo{
				ID:      ptrInt(tvdbID),
				Season:  ptrInt(ep.SeasonNumber),
				Episode: ptrInt(ep.EpisodeNumber),
			}
			out = append(out, q)
		}
		return out
	case *indexersearch.SeasonCriteria:
		tvdbID := seriesTvdbID(&c.CriteriaBase)
		if tvdbID == 0 {
			return nil
		}
		seasons := distinctSeasonNumbers(c.Episodes)
		out := make([]*torrentQuery, 0, len(seasons))
		for _, season := range seasons {
			q := newBaseQuery(s)
			q.TvdbInfo = &tvdbInfo{
				ID:     ptrInt(tvdbID),
				Season: ptrInt(season),
			}
			out = append(out, q)
		}
		return out
	case *indexersearch.DailyEpisodeCriteria:
		tvdbID := seriesTvdbID(&c.CriteriaBase)
		if tvdbID == 0 {
			return nil
		}
		q := newBaseQuery(s)
		q.TvdbInfo = &tvdbInfo{ID: ptrInt(tvdbID)}
		q.Search = c.AirDate.UTC().Format(time.DateOnly)
		return []*torrentQuery{q}
	case *indexersearch.DailySeasonCriteria:
		tvdbID := seriesTvdbID(&c.CriteriaBase)
		if tvdbID == 0 {
			return nil
		}
		q := newBaseQuery(s)
		q.TvdbInfo = &tvdbInfo{ID: ptrInt(tvdbID)}
		q.Search = fmt.Sprintf("%d-", c.Year)
		return []*torrentQuery{q}
	case *indexersearch.AnimeEpisodeCriteria:
		// C# AnimeEpisode overload at :24-41 has a bug — the loop
		// clones + sets Season/Episode on the cloned query but
		// never calls pageableRequests.Add(GetRequest(query)). Net
		// effect: the chain stays empty. Preserved verbatim; see
		// FetchSearch doc-comment.
		return nil
	case *indexersearch.AnimeSeasonCriteria:
		tvdbID := seriesTvdbID(&c.CriteriaBase)
		if tvdbID == 0 {
			return nil
		}
		seasons := distinctSeasonNumbers(c.Episodes)
		out := make([]*torrentQuery, 0, len(seasons))
		for _, season := range seasons {
			q := newBaseQuery(s)
			q.TvdbInfo = &tvdbInfo{
				ID:     ptrInt(tvdbID),
				Season: ptrInt(season),
			}
			out = append(out, q)
		}
		return out
	default:
		// SpecialEpisodeCriteria + unknown → empty (matches C# :63-66
		// and the absent default branch).
		return nil
	}
}

// seriesTvdbID surfaces the Series.TvdbID with nil-tolerance. Empty
// when Series is nil or TvdbID is zero.
func seriesTvdbID(sc *decisionengine.SearchCriteria) int {
	if sc == nil || sc.Series == nil {
		return 0
	}
	return sc.Series.TvdbID
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

func ptrInt(v int) *int { return &v }

// newBaseQuery builds the default torrentQuery body with auth +
// filter parameters populated. Mirrors C#
// `HDBitsRequestGenerator.GetRequest` at `:152-176` (modulo the
// per-criteria Search / Tvdb / Page tail).
func newBaseQuery(s *Settings) *torrentQuery {
	limit := pageSize
	q := &torrentQuery{
		Username: strings.TrimSpace(s.Username),
		Passkey:  strings.TrimSpace(s.ApiKey),
		Limit:    &limit,
	}
	if len(s.Categories) > 0 {
		q.Category = append([]int(nil), s.Categories...)
	}
	if len(s.Codecs) > 0 {
		q.Codec = append([]int(nil), s.Codecs...)
	}
	if len(s.Mediums) > 0 {
		q.Medium = append([]int(nil), s.Mediums...)
	}
	return q
}

// fetchQuery POSTs the given torrentQuery body to /api/torrents
// and parses the response.
func (i *Indexer) fetchQuery(ctx context.Context, settings *Settings, q *torrentQuery) ([]*parser.TorrentInfo, error) {
	apiURL := strings.TrimRight(strings.TrimSpace(settings.BaseUrl), "/") + "/api/torrents"
	body, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("hdbits: marshal query: %w", err)
	}
	req, err := httpclient.NewRequest(apiURL, httpclient.AcceptJSON)
	if err != nil {
		return nil, fmt.Errorf("hdbits: build request: %w", err)
	}
	req.Method = "POST"
	req.Headers.SetContentType("application/json")
	req.SetContentBytes(body)
	resp, err := i.HTTPClient.Post(ctx, req)
	if err != nil {
		return nil, err
	}
	return parseResponse([]byte(resp.Content()), settings, i.bindingName(), i.bindingID())
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("hdbits: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("hdbits: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("hdbits: Definition.Settings is %T, want *hdbits.Settings", i.definition.Settings)
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
		panic(fmt.Errorf("hdbits: register constructor: %w", err))
	}
}
