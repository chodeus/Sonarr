package nyaa

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/torrentrss"
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

// FetchSearch returns empty for any criteria. The C# per-criteria
// search-URL routing at NyaaRequestGenerator.cs:21-111 is deferred
// to a follow-up slice — see the package doc-comment.
func (i *Indexer) FetchSearch(_ context.Context, _ any) ([]*parser.TorrentInfo, error) {
	return []*parser.TorrentInfo{}, nil
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
