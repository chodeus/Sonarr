package fanzub

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/newznab"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the Fanzub Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/Fanzub/Fanzub.cs:9-29`.
//
// Reuses [newznab.ParseRSS] for the response decode — Fanzub uses
// the standard RSS-2.0 parser on the C# side (`new RssParser{
// UseEnclosureUrl=true, UseEnclosureLength=true}`); the newznab Go
// parser inherits from the same base, processes `<title>` / `<guid>` /
// `<pubDate>` / `<enclosure>` against missing newznab-namespace
// attributes gracefully (the optional fields fall back to defaults).
// Treating Fanzub-feed responses as a Newznab-shaped feed is a
// pragmatic choice: it avoids a duplicate RSS-to-ReleaseInfo parser
// for a single consumer. The "did you mean Torznab?" check at
// `newznab.ParseRSS:200-202` doesn't fire because Fanzub enclosures
// are NZB-typed (`application/x-nzb`), not torrent-typed.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs a Fanzub indexer.
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
// sibling RSS-feed indexers.
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

// FetchRecent issues the no-query RSS pull at
// `<BaseUrl>?cat=anime&max=<PageSize>`. C# cite:
// `FanzubRequestGenerator.cs:23-30` (GetRecentRequests -> empty
// term -> GetPagedRequests at :94-105).
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.ReleaseInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("fanzub: no HTTPClient configured")
	}

	feedURL := buildFeedURL(settings, "")
	req, err := httpclient.NewRequest(feedURL, httpclient.AcceptRSS)
	if err != nil {
		return nil, fmt.Errorf("fanzub: build request: %w", err)
	}
	resp, err := i.HTTPClient.Get(ctx, req)
	if err != nil {
		return nil, err
	}
	releases, err := newznab.ParseRSS([]byte(resp.Content()), &newznab.ParseOptions{
		IndexerName: i.bindingName(),
		IndexerID:   i.bindingID(),
		FeedURL:     feedURL,
	})
	if err != nil {
		return nil, err
	}
	return releases, nil
}

// FetchSearch dispatches the typed criteria through the per-type
// search-URL routing at C# `FanzubRequestGenerator.cs:32-92`. Slice
// 5.J151 closes the J150 deferral.
//
// Per criteria type:
//   - SeasonCriteria: if AnimeStandardFormatSearch + season > 0,
//     emit per-clean-scene-title BOTH `<title>%20S<ss>` AND
//     `<title>%20-%20S<ss>` (matches C# GetSeasonSearchStrings at
//     :107-112).
//   - AnimeEpisodeCriteria: per-clean-scene-title, build absolute-
//     episode search strings (`<title>%20<aa>` + `<title>%20-%20<aa>`)
//     when AbsoluteEpisodeNumber > 0 — the GetTitleSearchStrings
//     2-format overload at :114-119. Then if AnimeStandardFormatSearch
//   - season + episode > 0, ALSO append the S/E 2-format overload
//     at :121-126 (`<title>%20S<ss>E<ee>` + `<title>%20-%20S<ss>E<ee>`).
//   - AnimeSeasonCriteria: if AnimeStandardFormatSearch + season > 0,
//     emit the per-clean-scene-title 2-format overload.
//   - SingleEpisode + Daily / DailySeason / SpecialEpisode / unknown:
//     empty (C# overloads at :32-34, :50-58, :89-92 all return empty
//     IndexerPageableRequestChain).
//
// All non-empty search strings get wrapped in literal quote
// characters (`"..."`) and joined with `|` into a SINGLE q=<...>
// query parameter. The C# tree does the join at the per-criteria
// callsite (`string.Join("|", searchTitles)` at :44, :71, :83) —
// unlike Nyaa which emits one URL per term, Fanzub emits ONE URL
// per criteria with all terms collapsed into the q= value.
//
// Title source is `CleanSceneTitles()` (the cleaned form), NOT raw
// SceneTitles — matches the C# `searchCriteria.CleanSceneTitles`
// references at :43, :64, :82.
//
// CleanTitle (`cleanFanzubTitle`) strips `[!?` and the backtick
// before format substitution — mirrors C# `:130-131`.
func (i *Indexer) FetchSearch(ctx context.Context, criteria any) ([]*parser.ReleaseInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	query := buildSearchQuery(settings, criteria)
	if query == "" {
		// Short-circuit BEFORE the HTTPClient check (same posture as
		// Nyaa). C# overloads that return empty don't touch HTTP.
		return []*parser.ReleaseInfo{}, nil
	}
	if i.HTTPClient == nil {
		return nil, errors.New("fanzub: no HTTPClient configured")
	}
	feedURL := buildFeedURL(settings, query)
	req, err := httpclient.NewRequest(feedURL, httpclient.AcceptRSS)
	if err != nil {
		return nil, fmt.Errorf("fanzub: build request: %w", err)
	}
	resp, err := i.HTTPClient.Get(ctx, req)
	if err != nil {
		return nil, err
	}
	releases, err := newznab.ParseRSS([]byte(resp.Content()), &newznab.ParseOptions{
		IndexerName: i.bindingName(),
		IndexerID:   i.bindingID(),
		FeedURL:     feedURL,
	})
	if err != nil {
		return nil, err
	}
	return releases, nil
}

// buildSearchQuery dispatches per criteria type and returns the
// pipe-joined query string. Empty string for the no-op criteria
// types.
func buildSearchQuery(s *Settings, criteria any) string {
	var terms []string
	switch c := criteria.(type) {
	case *indexersearch.SeasonCriteria:
		if !s.AnimeStandardFormatSearch || c.SeasonNumber <= 0 {
			return ""
		}
		for _, t := range c.CleanSceneTitles() {
			terms = append(terms, seasonSearchStrings(t, c.SeasonNumber)...)
		}
	case *indexersearch.AnimeEpisodeCriteria:
		for _, t := range c.CleanSceneTitles() {
			if c.AbsoluteEpisodeNumber > 0 {
				terms = append(terms, absoluteEpisodeSearchStrings(t, c.AbsoluteEpisodeNumber)...)
			}
		}
		if s.AnimeStandardFormatSearch && c.SeasonNumber > 0 && c.EpisodeNumber > 0 {
			for _, t := range c.CleanSceneTitles() {
				terms = append(terms, standardEpisodeSearchStrings(t, c.SeasonNumber, c.EpisodeNumber)...)
			}
		}
	case *indexersearch.AnimeSeasonCriteria:
		if !s.AnimeStandardFormatSearch || c.SeasonNumber <= 0 {
			return ""
		}
		for _, t := range c.CleanSceneTitles() {
			terms = append(terms, seasonSearchStrings(t, c.SeasonNumber)...)
		}
	default:
		// SingleEpisode + DailyEpisode + DailySeason + SpecialEpisode
		// + unknown → empty (matches C# overloads that return empty
		// IndexerPageableRequestChain at :32-34, :50-58, :89-92).
		return ""
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, "|")
}

// seasonSearchStrings mirrors C# `:107-112` —
// `"{0}%20S{1:00}"` + `"{0}%20-%20S{1:00}"` with the title cleaned
// and wrapped in literal quotes.
func seasonSearchStrings(title string, season int) []string {
	clean := cleanFanzubTitle(title)
	return []string{
		fmt.Sprintf("\"%s%%20S%02d\"", clean, season),
		fmt.Sprintf("\"%s%%20-%%20S%02d\"", clean, season),
	}
}

// absoluteEpisodeSearchStrings mirrors C# `:114-119` —
// `"{0}%20{1:00}"` + `"{0}%20-%20{1:00}"`.
func absoluteEpisodeSearchStrings(title string, abs int) []string {
	clean := cleanFanzubTitle(title)
	return []string{
		fmt.Sprintf("\"%s%%20%02d\"", clean, abs),
		fmt.Sprintf("\"%s%%20-%%20%02d\"", clean, abs),
	}
}

// standardEpisodeSearchStrings mirrors C# `:121-126` —
// `"{0}%20S{1:00}E{2:00}"` + `"{0}%20-%20S{1:00}E{2:00}"`.
func standardEpisodeSearchStrings(title string, season, episode int) []string {
	clean := cleanFanzubTitle(title)
	return []string{
		fmt.Sprintf("\"%s%%20S%02dE%02d\"", clean, season, episode),
		fmt.Sprintf("\"%s%%20-%%20S%02dE%02d\"", clean, season, episode),
	}
}

// removeCharactersRegex mirrors C# `:13`:
//
//	private static readonly Regex RemoveCharactersRegex =
//	    new Regex(@"[!?` + "`" + `]", RegexOptions.Compiled);
//
// Strips `!`, `?`, and the backtick character before format
// substitution.
var removeCharactersRegex = regexp.MustCompile("[!?`]")

// cleanFanzubTitle mirrors C# `:128-131`.
func cleanFanzubTitle(title string) string {
	return removeCharactersRegex.ReplaceAllString(title, "")
}

// buildFeedURL composes the request URL. Mirrors
// `FanzubRequestGenerator.cs:96-104`. Empty `query` produces the
// no-search RSS form; non-empty appends `&q=<query>`.
//
// C# uses `string.Format("{0}?cat=anime&max={1}", BaseUrl, PageSize)`
// — note no `?` separator handling, so a BaseUrl ending in `/` (the
// C# default `http://fanzub.com/rss/`) produces
// `http://fanzub.com/rss/?cat=anime&max=100`. The Go port preserves
// the verbatim concat to match the C# wire shape.
func buildFeedURL(s *Settings, query string) string {
	u := strings.TrimSpace(s.BaseUrl) + "?cat=anime&max=" + strconv.Itoa(defaultPageSize)
	if query != "" {
		u += "&q=" + query
	}
	return u
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("fanzub: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("fanzub: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("fanzub: Definition.Settings is %T, want *fanzub.Settings", i.definition.Settings)
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
		panic(fmt.Errorf("fanzub: register constructor: %w", err))
	}
}
