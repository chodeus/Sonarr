package fanzub

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/newznab"
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

// FetchSearch returns empty for any criteria. The C# per-criteria
// search-URL routing at FanzubRequestGenerator.cs:32-92 is deferred
// to a follow-up slice — see the package doc-comment.
func (i *Indexer) FetchSearch(_ context.Context, _ any) ([]*parser.ReleaseInfo, error) {
	return []*parser.ReleaseInfo{}, nil
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
