package iptorrents

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/torrentrss"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the IPTorrents Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/IPTorrents/IPTorrents.cs:9-31`.
//
// Compared to torrentleech (J146): the user pastes the FULL feed URL
// as BaseUrl (no separate ApiKey field), so FetchRecent uses
// Settings.BaseUrl verbatim. The parser knob differs too — C# uses
// `TorrentRssParser{ParseSizeInDescription=true}` instead of
// Torrentleech's `ParseSeedersInDescription=true`.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs an IPTorrents indexer.
func NewIndexer(client httpclient.Client) *Indexer {
	return &Indexer{HTTPClient: client}
}

// Name returns the user-visible class name. C# cite: `:11`.
func (*Indexer) Name() string { return DisplayName }

// ConfigContract returns the Settings class identifier.
func (*Indexer) ConfigContract() string { return ConfigContract }

// Message returns nil — IPTorrents has no static banner.
func (*Indexer) Message() *provider.Message { return nil }

// DefaultDefinitions returns a single pre-canned definition. The
// user must paste their per-account feed URL into BaseUrl.
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

// Test runs Settings.Validate. No live-fetch probe — same posture
// as torrentleech / torrentrss; live-fetch lands as a follow-up if
// the validate-only check turns out to miss a failure mode users
// actually hit.
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

// FetchRecent issues the single RSS-feed pull at Settings.BaseUrl
// verbatim. C# cite: `IPTorrentsRequestGenerator.cs:55-58`.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("iptorrents: no HTTPClient configured")
	}

	req, err := httpclient.NewRequest(settings.BaseUrl, httpclient.AcceptRSS)
	if err != nil {
		return nil, fmt.Errorf("iptorrents: build request: %w", err)
	}

	resp, err := i.HTTPClient.Get(ctx, req)
	if err != nil {
		return nil, err
	}
	releases, err := torrentrss.ParseRSS([]byte(resp.Content()), &torrentrss.ParseOptions{
		IndexerName: i.bindingName(),
		IndexerID:   i.bindingID(),
		FeedURL:     settings.BaseUrl,
		ParserSettings: torrentrss.ParserSettings{
			ParseSizeInDescription: true,
		},
	})
	if err != nil {
		return nil, err
	}
	return releases, nil
}

// FetchSearch returns empty for any criteria. C# cite:
// `IPTorrentsRequestGenerator.cs:20-53` — every overload returns a
// fresh empty IndexerPageableRequestChain.
func (i *Indexer) FetchSearch(_ context.Context, _ any) ([]*parser.TorrentInfo, error) {
	return []*parser.TorrentInfo{}, nil
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("iptorrents: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("iptorrents: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("iptorrents: Definition.Settings is %T, want *iptorrents.Settings", i.definition.Settings)
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

// Constructor returns a fresh Settings — registered with
// provider.Default() so Factory.LoadDefinition can hydrate Settings
// JSON for (Impl=IPTorrents, Contract=IPTorrentsSettings) rows.
func Constructor() any { return NewSettings() }

func init() {
	if err := provider.Default().Replace(provider.FamilyIndexer,
		provider.Key{Impl: Implementation, Contract: ConfigContract},
		Constructor); err != nil {
		panic(fmt.Errorf("iptorrents: register constructor: %w", err))
	}
}
