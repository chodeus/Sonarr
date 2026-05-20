package torrentleech

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

// Indexer is the Torrentleech Provider impl. C# cite:
// `src/NzbDrone.Core/Indexers/Torrentleech/Torrentleech.cs:9-31`.
//
// SupportsSearch=false (C# `:14`) — search overloads return empty.
// The C# tree uses `TorrentRssParser{UseGuidInfoUrl=true,
// ParseSeedersInDescription=true}`. The Go port wires the same two
// knobs into a fresh [torrentrss.ParserSettings] passed to ParseRSS;
// the surface lives in this package rather than torrentrss because
// torrentrss owns the generic-RSS path and forking would be more
// fragile than parameterising.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs a Torrentleech indexer bound to the given
// HTTPClient. Definition is set later via [Indexer.SetDefinition].
func NewIndexer(client httpclient.Client) *Indexer {
	return &Indexer{HTTPClient: client}
}

// Name returns the user-visible class name. C# cite: `:11`. The
// returned string differs from [Implementation] — see settings.go
// for the why.
func (*Indexer) Name() string { return DisplayName }

// ConfigContract returns the Settings class identifier.
func (*Indexer) ConfigContract() string { return ConfigContract }

// Message returns nil — Torrentleech has no static banner. C# tree
// likewise returns null.
func (*Indexer) Message() *provider.Message { return nil }

// DefaultDefinitions returns a single pre-canned definition with
// the C# default BaseUrl. The user fills in their ApiKey at
// Definition-create time.
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

// Test runs Settings.Validate. Torrentleech has no live caps probe —
// the C# tree's TestConnection just runs the request-generator + a
// content fetch, which would 4xx on an empty/invalid ApiKey. The Go
// port keeps the validate-only Test posture for parity with
// torrentrss; live-fetch probe lands as a follow-up if needed.
func (i *Indexer) Test(_ context.Context) provider.ValidationResult {
	settings, err := i.settings()
	if err != nil {
		return provider.ValidationResult{Errors: []provider.ValidationFailure{
			provider.NewValidationFailure("", err.Error()),
		}}
	}
	return settings.Validate()
}

// RequestAction returns nil for everything — TorrentLeech has no
// custom UI actions.
func (i *Indexer) RequestAction(_ string, _ map[string]string) (any, error) {
	return nil, nil
}

// FetchRecent issues the single RSS-feed pull at
// `<BaseUrl>/<ApiKey>`. C# cite:
// `TorrentleechRequestGenerator.cs:55-58`.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("torrentleech: no HTTPClient configured")
	}

	feedURL := strings.TrimRight(strings.TrimSpace(settings.BaseUrl), "/") +
		"/" + strings.TrimSpace(settings.ApiKey)
	req, err := httpclient.NewRequest(feedURL, httpclient.AcceptRSS)
	if err != nil {
		return nil, fmt.Errorf("torrentleech: build request: %w", err)
	}

	resp, err := i.HTTPClient.Get(ctx, req)
	if err != nil {
		return nil, err
	}
	releases, err := torrentrss.ParseRSS([]byte(resp.Content()), &torrentrss.ParseOptions{
		IndexerName: i.bindingName(),
		IndexerID:   i.bindingID(),
		FeedURL:     feedURL,
		// C# Torrentleech.cs:29 uses
		// `TorrentRssParser{UseGuidInfoUrl=true, ParseSeedersInDescription=true}`.
		// ParseSeedersInDescription maps cleanly; UseGuidInfoUrl is
		// the parser's default behaviour (treats <guid> as the info
		// URL when no explicit <link> exists), so no flag flip needed
		// — the torrentrss ParserSettings default matches.
		ParserSettings: torrentrss.ParserSettings{
			ParseSeedersInDescription: true,
		},
	})
	if err != nil {
		return nil, err
	}
	return releases, nil
}

// FetchSearch returns empty for any criteria. C# cite:
// `TorrentleechRequestGenerator.cs:20-53` — every overload returns
// a fresh empty IndexerPageableRequestChain.
func (i *Indexer) FetchSearch(_ context.Context, _ any) ([]*parser.TorrentInfo, error) {
	return []*parser.TorrentInfo{}, nil
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("torrentleech: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("torrentleech: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("torrentleech: Definition.Settings is %T, want *torrentleech.Settings", i.definition.Settings)
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
// JSON for (Impl=TorrentLeech, Contract=TorrentleechSettings) rows.
func Constructor() any { return NewSettings() }

func init() {
	if err := provider.Default().Replace(provider.FamilyIndexer,
		provider.Key{Impl: Implementation, Contract: ConfigContract},
		Constructor); err != nil {
		panic(fmt.Errorf("torrentleech: register constructor: %w", err))
	}
}
