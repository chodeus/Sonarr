package nyaa

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/torrentrss"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the Nyaa Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/Nyaa/Nyaa.cs:9-29`.
//
// Parser knob set mirrors C# `:27`:
//
//	new TorrentRssParser {
//	    UseGuidInfoUrl = true,
//	    SizeElementName = "size",
//	    InfoHashElementName = "infoHash",
//	    PeersElementName = "leechers",
//	    CalculatePeersAsSum = true,
//	    SeedsElementName = "seeders",
//	}
//
// The Go-port torrentrss.ParserSettings has all five fields; the
// Go default for UseGuidInfoUrl is the same behaviour the C# flag
// enables, so the explicit flip isn't needed (matches the J146
// torrentleech wiring posture).
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs a Nyaa indexer.
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

// Test runs Settings.Validate. Same validate-only posture as the
// other RSS-feed indexers.
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

// FetchRecent issues the no-term RSS pull at
// `<BaseUrl>/?page=rss<AdditionalParameters>`. C# cite:
// `NyaaRequestGenerator.cs:113-123` (GetPagedRequests with term=nil).
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("nyaa: no HTTPClient configured")
	}

	feedURL := buildFeedURL(settings, "")
	req, err := httpclient.NewRequest(feedURL, httpclient.AcceptRSS)
	if err != nil {
		return nil, fmt.Errorf("nyaa: build request: %w", err)
	}
	resp, err := i.HTTPClient.Get(ctx, req)
	if err != nil {
		return nil, err
	}
	releases, err := torrentrss.ParseRSS([]byte(resp.Content()), &torrentrss.ParseOptions{
		IndexerName: i.bindingName(),
		IndexerID:   i.bindingID(),
		FeedURL:     feedURL,
		ParserSettings: torrentrss.ParserSettings{
			SizeElementName:     "size",
			InfoHashElementName: "infoHash",
			PeersElementName:    "leechers",
			SeedsElementName:    "seeders",
			CalculatePeersAsSum: true,
		},
	})
	if err != nil {
		return nil, err
	}
	return releases, nil
}

// FetchSearch dispatches the typed criteria through the per-type
// search-URL routing at C# `NyaaRequestGenerator.cs:21-111`. Slice
// 5.J149 closes the J148 deferral.
//
// Per criteria type:
//   - SingleEpisodeCriteria: if AnimeStandardFormatSearch + season >
//     0 + episode > 0, emit one URL per scene-title with
//     `<title>+s<ss>e<ee>`.
//   - SeasonCriteria: empty (C# `:36-49` returns an empty chain).
//   - AnimeEpisodeCriteria: per-title, if absolute > 0 emit
//     `<title>+<abs>` (+ `<title>+<0abs>` when abs < 10); if
//     AnimeStandardFormatSearch + season + episode > 0, ALSO emit
//     `<title>+s<ss>e<ee>`.
//   - AnimeSeasonCriteria: if AnimeStandardFormatSearch + season >
//     0, emit `<title>+s<ss>`.
//   - SpecialEpisodeCriteria: per query title, emit the bare
//     PrepareQuery(title).
//   - Daily / DailySeason / unknown: empty.
//
// Results are deduped by parser.TorrentInfo.Guid to handle the
// overlap when multiple search terms hit the same release.
func (i *Indexer) FetchSearch(ctx context.Context, criteria any) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	terms := buildSearchTerms(settings, criteria)
	if len(terms) == 0 {
		// Short-circuit BEFORE the HTTPClient check so criteria types
		// the C# tree treats as no-op (DailyEpisode, DailySeason,
		// non-AnimeStandard SingleEpisode/Season) don't surface as
		// nil-client errors. Matches the C# overloads that return
		// `new IndexerPageableRequestChain()` (empty) without
		// touching the HTTP layer.
		return []*parser.TorrentInfo{}, nil
	}
	if i.HTTPClient == nil {
		return nil, errors.New("nyaa: no HTTPClient configured")
	}

	dedup := make(map[string]struct{}, len(terms)*16)
	var all []*parser.TorrentInfo
	for _, term := range terms {
		feedURL := buildFeedURL(settings, term)
		req, err := httpclient.NewRequest(feedURL, httpclient.AcceptRSS)
		if err != nil {
			return nil, fmt.Errorf("nyaa: build request: %w", err)
		}
		resp, err := i.HTTPClient.Get(ctx, req)
		if err != nil {
			continue
		}
		releases, err := torrentrss.ParseRSS([]byte(resp.Content()), &torrentrss.ParseOptions{
			IndexerName: i.bindingName(),
			IndexerID:   i.bindingID(),
			FeedURL:     feedURL,
			ParserSettings: torrentrss.ParserSettings{
				SizeElementName:     "size",
				InfoHashElementName: "infoHash",
				PeersElementName:    "leechers",
				SeedsElementName:    "seeders",
				CalculatePeersAsSum: true,
			},
		})
		if err != nil {
			continue
		}
		for _, r := range releases {
			key := r.Guid
			if key == "" {
				// Fallback for feeds that omit <guid>: use Title +
				// DownloadURL. Prevents dedup-collapse-everything
				// when Guid is uniformly empty.
				key = r.Title + "|" + r.DownloadURL
			}
			if _, ok := dedup[key]; ok {
				continue
			}
			dedup[key] = struct{}{}
			all = append(all, r)
		}
	}
	if all == nil {
		all = []*parser.TorrentInfo{}
	}
	return all, nil
}

// buildSearchTerms type-asserts the supplied criteria and emits the
// per-term list mirroring the C# NyaaRequestGenerator per-overload
// dispatch.
func buildSearchTerms(s *Settings, criteria any) []string {
	switch c := criteria.(type) {
	case *indexersearch.SingleEpisodeCriteria:
		if !s.AnimeStandardFormatSearch || c.SeasonNumber <= 0 || c.EpisodeNumber <= 0 {
			return nil
		}
		titles := c.SceneTitles
		out := make([]string, 0, len(titles))
		for _, t := range titles {
			out = append(out, fmt.Sprintf("%s+s%02de%02d", prepareQuery(t), c.SeasonNumber, c.EpisodeNumber))
		}
		return out
	case *indexersearch.AnimeEpisodeCriteria:
		titles := c.SceneTitles
		var out []string
		for _, t := range titles {
			pq := prepareQuery(t)
			if c.AbsoluteEpisodeNumber > 0 {
				out = append(out, fmt.Sprintf("%s+%d", pq, c.AbsoluteEpisodeNumber))
				if c.AbsoluteEpisodeNumber < 10 {
					out = append(out, fmt.Sprintf("%s+%02d", pq, c.AbsoluteEpisodeNumber))
				}
			}
			if s.AnimeStandardFormatSearch && c.SeasonNumber > 0 && c.EpisodeNumber > 0 {
				out = append(out, fmt.Sprintf("%s+s%02de%02d", pq, c.SeasonNumber, c.EpisodeNumber))
			}
		}
		return out
	case *indexersearch.AnimeSeasonCriteria:
		if !s.AnimeStandardFormatSearch || c.SeasonNumber <= 0 {
			return nil
		}
		titles := c.SceneTitles
		out := make([]string, 0, len(titles))
		for _, t := range titles {
			out = append(out, fmt.Sprintf("%s+s%02d", prepareQuery(t), c.SeasonNumber))
		}
		return out
	case *indexersearch.SpecialEpisodeCriteria:
		out := make([]string, 0, len(c.EpisodeQueryTitles))
		for _, q := range c.EpisodeQueryTitles {
			out = append(out, prepareQuery(q))
		}
		return out
	default:
		// SeasonCriteria + DailyEpisodeCriteria + DailySeasonCriteria
		// + anything else → empty (matches C# overloads at :36-58 +
		// :86 which all return empty IndexerPageableRequestChain).
		return nil
	}
}

// prepareQuery mirrors C# `NyaaRequestGenerator.PrepareQuery` at
// `:125-128` — replaces spaces with `+` for the URL query form.
func prepareQuery(q string) string {
	return strings.ReplaceAll(q, " ", "+")
}

// buildFeedURL composes the request URL. Mirrors
// `NyaaRequestGenerator.cs:115-122`. Empty `term` produces the
// no-search RSS form; non-empty appends `&term=<term>`.
func buildFeedURL(s *Settings, term string) string {
	base := strings.TrimRight(strings.TrimSpace(s.BaseUrl), "/")
	u := base + "/?page=rss" + s.AdditionalParameters
	if term != "" {
		u += "&term=" + term
	}
	return u
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("nyaa: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("nyaa: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("nyaa: Definition.Settings is %T, want *nyaa.Settings", i.definition.Settings)
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
		panic(fmt.Errorf("nyaa: register constructor: %w", err))
	}
}
