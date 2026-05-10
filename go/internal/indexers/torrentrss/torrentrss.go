package torrentrss

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the generic Torrent RSS Feed Provider impl. C# cite:
// src/NzbDrone.Core/Indexers/TorrentRss/TorrentRssIndexer.cs:11-34.
//
// Search-disabled: TorrentRss feeds are RSS-only, no per-criteria queries.
// SupportsSearch=false in C# at :15. FetchSearch returns an empty slice
// for any criteria.
type Indexer struct {
	HTTPClient httpclient.Client

	definition *provider.Definition
}

// NewIndexer constructs a TorrentRss indexer.
func NewIndexer(client httpclient.Client) *Indexer {
	return &Indexer{HTTPClient: client}
}

// Name returns the user-visible class name. C# cite: TorrentRssIndexer.cs:13.
func (i *Indexer) Name() string { return "TorrentRssIndexer" }

// ConfigContract returns the Settings class name string.
func (i *Indexer) ConfigContract() string { return "TorrentRssIndexerSettings" }

// Message returns nil — no static banner.
func (i *Indexer) Message() *provider.Message { return nil }

// DefaultDefinitions returns a single placeholder Definition that the user
// edits to point at their feed URL. C# does not pre-populate any
// presets here.
func (i *Indexer) DefaultDefinitions() []*provider.Definition {
	return []*provider.Definition{
		{
			Name:           "Torrent RSS Feed",
			Implementation: i.Name(),
			ConfigContract: i.ConfigContract(),
			Enable:         false,
			Settings:       NewSettings(),
		},
	}
}

// Definition returns the bound Definition.
func (i *Indexer) Definition() *provider.Definition { return i.definition }

// SetDefinition binds the Definition.
func (i *Indexer) SetDefinition(d *provider.Definition) { i.definition = d }

// Test runs Settings.Validate. The C# tree does not perform any caps probe
// here — there's no caps endpoint for a generic RSS feed; the feed either
// parses or it doesn't.
func (i *Indexer) Test(_ context.Context) provider.ValidationResult {
	settings, err := i.settings()
	if err != nil {
		return provider.ValidationResult{Errors: []provider.ValidationFailure{
			provider.NewValidationFailure("", err.Error()),
		}}
	}
	return settings.Validate()
}

// RequestAction returns nil for everything — TorrentRss has no
// custom UI actions. C# cite: TorrentRssIndexer.cs has no override.
func (i *Indexer) RequestAction(_ string, _ map[string]string) (any, error) {
	return nil, nil
}

// FetchRecent issues the single RSS-feed pull. C# cite:
// TorrentRssIndexerRequestGenerator.GetRecentRequests + GetRssRequests at
// :11-69.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.HTTPClient == nil {
		return nil, errors.New("torrentrss: no HTTPClient configured")
	}

	url := strings.TrimRight(strings.TrimSpace(settings.BaseUrl), "/")
	req, err := httpclient.NewRequest(url, httpclient.AcceptRSS)
	if err != nil {
		return nil, fmt.Errorf("torrentrss: build request: %w", err)
	}
	if strings.TrimSpace(settings.Cookie) != "" {
		// Settings.Cookie is the raw `key=value; key2=value2` string the
		// user pasted from their browser. The httpclient's Request.Cookies
		// map round-trips through InitializeRequestCookies into a single
		// Cookie header — same shape as the C# pre-request handling.
		for _, p := range httpclient.ParseCookies(settings.Cookie) {
			req.Cookies[p.Key] = p.Value
		}
	}

	resp, err := i.HTTPClient.Get(ctx, req)
	if err != nil {
		return nil, err
	}
	releases, err := ParseRSS([]byte(resp.Content()), &ParseOptions{
		IndexerName:    i.bindingName(),
		IndexerID:      i.bindingID(),
		FeedURL:        settings.BaseUrl,
		ParserSettings: settings.Parser,
	})
	if err != nil {
		return nil, err
	}
	return releases, nil
}

// FetchSearch returns empty for any criteria. C# cite:
// TorrentRssIndexerRequestGenerator.GetSearchRequests at :22-58 — every
// overload returns a fresh empty IndexerPageableRequestChain.
func (i *Indexer) FetchSearch(_ context.Context, _ any) ([]*parser.TorrentInfo, error) {
	return []*parser.TorrentInfo{}, nil
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("torrentrss: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("torrentrss: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("torrentrss: Definition.Settings is %T, want *torrentrss.Settings", i.definition.Settings)
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

// Constructor returns a fresh Settings — registered with provider.Default()
// so Factory.LoadDefinition can hydrate Settings JSON for
// (Impl=TorrentRssIndexer, Contract=TorrentRssIndexerSettings) rows.
func Constructor() any { return NewSettings() }

func init() {
	if err := provider.Default().Replace(provider.FamilyIndexer,
		provider.Key{Impl: "TorrentRssIndexer", Contract: "TorrentRssIndexerSettings"},
		Constructor); err != nil {
		panic(fmt.Errorf("torrentrss: register constructor: %w", err))
	}
}
