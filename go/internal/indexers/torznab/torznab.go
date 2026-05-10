package torznab

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/newznab"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// Indexer is the Torznab Provider impl. C# cite:
// src/NzbDrone.Core/Indexers/Torznab/Torznab.cs:17-179.
//
// Reuses the Newznab CapabilitiesProvider + RequestGenerator (the
// protocol is identical apart from response namespace), pairs them with
// the torrent-flavoured RSS parser from rss_parser.go in this package.
//
// Sub-arc scope: ship the Provider surface (Name/ConfigContract/
// Definition/Test/RequestAction) plus FetchRecent / FetchSearch entry
// points that surface *parser.TorrentInfo. Status-recording integration
// matches Newznab — handled by the Service slice that wraps Fetch*.
type Indexer struct {
	HTTPClient   httpclient.Client
	CapsProvider *newznab.CapabilitiesProvider

	definition *provider.Definition
}

// NewIndexer constructs a Torznab indexer. Reuses Newznab's typed
// CapabilitiesProvider since the `?t=caps` shape is identical.
func NewIndexer(client httpclient.Client, caps *newznab.CapabilitiesProvider) *Indexer {
	return &Indexer{HTTPClient: client, CapsProvider: caps}
}

// Name returns the user-visible class name. Mirrors Torznab.cs:21.
func (i *Indexer) Name() string { return "Torznab" }

// ConfigContract returns the Settings class name string.
func (i *Indexer) ConfigContract() string { return "TorznabSettings" }

// Message returns nil — no static banner.
func (i *Indexer) Message() *provider.Message { return nil }

// DefaultDefinitions returns nil — the C# Torznab class does not
// pre-populate any presets (Torznab.cs has GetDefinition + GetSettings
// helpers but the DefaultDefinitions enumerator is not declared, so the
// base IndexerBase.DefaultDefinitions returns just the bare class).
//
// The Go port returns a single anonymous Default-style entry pointing at
// `http://localhost` so the UI's "add new" form has a placeholder to
// edit; the user customises BaseUrl + ApiKey before save.
func (i *Indexer) DefaultDefinitions() []*provider.Definition {
	s := NewSettings()
	return []*provider.Definition{
		{
			Name:           "Torznab",
			Implementation: i.Name(),
			ConfigContract: i.ConfigContract(),
			Enable:         false,
			Settings:       s,
		},
	}
}

// Definition returns the bound Definition.
func (i *Indexer) Definition() *provider.Definition { return i.definition }

// SetDefinition binds the Definition.
func (i *Indexer) SetDefinition(d *provider.Definition) { i.definition = d }

// Test ports the two-phase validation at Torznab.cs:85-141.
//
// Order:
//  1. Settings.Validate() (incl. inherited Newznab rules).
//  2. JackettAll() warning (when ApiPath/BaseUrl points at the all-indexers
//     Jackett endpoint — that endpoint isn't supported and the user must
//     reconfigure).
//  3. TestCapabilities() — same gate as Newznab: basic search has 'q' OR
//     tv-search has any of q/tvdbid/rid AND has both 'season' AND 'ep'.
func (i *Indexer) Test(ctx context.Context) provider.ValidationResult {
	settings, err := i.settings()
	if err != nil {
		return provider.ValidationResult{Errors: []provider.ValidationFailure{
			provider.NewValidationFailure("", err.Error()),
		}}
	}

	r := settings.Validate()
	if !r.IsValid() {
		return r
	}

	if jackettWarning := jackettAll(settings); jackettWarning != nil {
		r.Errors = append(r.Errors, *jackettWarning)
	}

	if i.CapsProvider == nil {
		r.Errors = append(r.Errors, provider.NewValidationWarning("", "capabilities provider not configured"))
		return r
	}

	caps, err := i.CapsProvider.GetCapabilities(ctx, &settings.Settings)
	if err != nil {
		r.Errors = append(r.Errors, provider.NewValidationFailure("",
			fmt.Sprintf("Unable to connect to indexer: %v", err)))
		return r
	}
	if !validateSearchModeSupported(caps) {
		r.Errors = append(r.Errors, provider.NewValidationFailure("",
			"Indexer does not advertise any compatible search parameters"))
	}
	return r
}

// jackettAll ports JackettAll at Torznab.cs:126-141. The "all" endpoint
// hits every configured Jackett indexer at once and the response stream
// can't be classified per-indexer — Sonarr surfaces a warning.
func jackettAll(s *Settings) *provider.ValidationFailure {
	const jackettAllOldPath = "/torznab/all"
	const jackettAllV2Path = "/api/v2.0/indexers/all/results/torznab"
	if strings.Contains(s.ApiPath, jackettAllOldPath) ||
		strings.Contains(s.ApiPath, jackettAllV2Path) ||
		strings.Contains(s.BaseUrl, jackettAllOldPath) ||
		strings.Contains(s.BaseUrl, jackettAllV2Path) {
		f := provider.NewValidationWarning("ApiPath",
			"Jackett's all-indexers endpoint is not supported, use a per-indexer endpoint instead")
		return &f
	}
	return nil
}

// validateSearchModeSupported ports the boolean shape of TestCapabilities
// at Torznab.cs:98-124 (identical to Newznab's TestCapabilities at :113-129).
func validateSearchModeSupported(caps *newznab.Capabilities) bool {
	if has(caps.SupportedSearchParameters, "q") {
		return true
	}
	if caps.SupportedTvSearchParameters == nil {
		return false
	}
	hasAny := has(caps.SupportedTvSearchParameters, "q") ||
		has(caps.SupportedTvSearchParameters, "tvdbid") ||
		has(caps.SupportedTvSearchParameters, "rid")
	hasSeasonEp := has(caps.SupportedTvSearchParameters, "season") &&
		has(caps.SupportedTvSearchParameters, "ep")
	return hasAny && hasSeasonEp
}

// RequestAction handles "newznabCategories". C# cite: Torznab.cs:143-164
// (identical to Newznab.cs:141-165).
func (i *Indexer) RequestAction(stage string, _ map[string]string) (any, error) {
	if stage != "newznabCategories" {
		return nil, nil
	}
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.CapsProvider == nil {
		return map[string]any{"options": []any{}}, nil
	}
	caps, err := i.CapsProvider.GetCapabilities(context.Background(), &settings.Settings)
	if err != nil {
		return map[string]any{"options": []any{}}, nil
	}
	return map[string]any{"options": flattenCategories(caps.Categories)}, nil
}

func flattenCategories(cats []newznab.Category) []map[string]any {
	out := make([]map[string]any, 0, len(cats)*4)
	for _, c := range cats {
		out = append(out, map[string]any{"value": c.ID, "name": c.Name})
		for _, sc := range c.Subcategories {
			out = append(out, map[string]any{"value": sc.ID, "name": c.Name + " - " + sc.Name})
		}
	}
	return out
}

// FetchRecent issues the RSS-feed pull. C# cite: HttpIndexerBase.FetchRecent
// + Torznab.cs reusing NewznabRequestGenerator.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.TorrentInfo, error) {
	gen, err := i.requestGenerator()
	if err != nil {
		return nil, err
	}
	chain, err := gen.GetRecentRequests(ctx)
	if err != nil {
		return nil, err
	}
	return i.runChain(ctx, chain), nil
}

// FetchSearch dispatches a typed search criteria value through the
// generator and runs the resulting chain.
func (i *Indexer) FetchSearch(ctx context.Context, criteria any) ([]*parser.TorrentInfo, error) {
	gen, err := i.requestGenerator()
	if err != nil {
		return nil, err
	}
	chain, err := gen.GetSearchRequests(ctx, criteria)
	if err != nil {
		return nil, err
	}
	return i.runChain(ctx, chain), nil
}

// runChain executes a PageableRequestChain. Same shape as the Newznab
// equivalent but produces *parser.TorrentInfo via ParseRSS in this
// package (which uses the torznab namespace).
func (i *Indexer) runChain(ctx context.Context, chain *newznab.PageableRequestChain) []*parser.TorrentInfo {
	settings, _ := i.settings()
	feedURL := ""
	hasAPIKey := false
	if settings != nil {
		feedURL = settings.BaseUrl
		hasAPIKey = strings.TrimSpace(settings.ApiKey) != ""
	}

	dedup := make(map[string]struct{})
	var all []*parser.TorrentInfo

	for tierIdx := 0; tierIdx < chain.Tiers(); tierIdx++ {
		tier := chain.Tier(tierIdx)
		var tierReleases []*parser.TorrentInfo

		for _, stream := range tier {
			for _, req := range stream {
				resp, err := i.HTTPClient.Get(ctx, req)
				if err != nil {
					continue
				}
				releases, err := ParseRSS([]byte(resp.Content()), &ParseOptions{
					IndexerName: i.bindingName(),
					IndexerID:   i.bindingID(),
					FeedURL:     feedURL,
					HasAPIKey:   hasAPIKey,
				})
				if err != nil {
					return all
				}
				if len(releases) == 0 {
					break
				}
				tierReleases = append(tierReleases, releases...)
			}
		}

		for _, r := range tierReleases {
			if r.Guid == "" {
				continue
			}
			if _, ok := dedup[r.Guid]; ok {
				continue
			}
			dedup[r.Guid] = struct{}{}
			all = append(all, r)
		}

		if len(all) > 0 {
			break
		}
	}
	return all
}

// requestGenerator builds a fresh newznab.RequestGenerator bound to this
// indexer's Settings. The generator only reads the embedded
// newznab.Settings — Torznab-specific fields (MinimumSeeders, SeedCriteria)
// are not query-string params, they're consumed downstream in the
// decisionengine.
func (i *Indexer) requestGenerator() (*newznab.RequestGenerator, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	g := newznab.NewRequestGenerator(&settings.Settings, nil, i.bindingName())
	g.CapabilitiesProvider = i.CapsProvider
	return g, nil
}

func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("torznab: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("torznab: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("torznab: Definition.Settings is %T, want *torznab.Settings", i.definition.Settings)
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

// has reports whether s contains v (small helper duplicated from newznab
// to avoid exporting the helper from there).
func has(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

// Compile-time interface check.
var _ provider.Provider = (*Indexer)(nil)

// Constructor returns a fresh Settings — registered with provider.Default()
// so Factory.LoadDefinition can hydrate Settings JSON for
// (Impl=Torznab, Contract=TorznabSettings) rows.
func Constructor() any { return NewSettings() }

func init() {
	if err := provider.Default().Replace(provider.FamilyIndexer,
		provider.Key{Impl: "Torznab", Contract: "TorznabSettings"},
		Constructor); err != nil {
		panic(fmt.Errorf("torznab: register constructor: %w", err))
	}
}
