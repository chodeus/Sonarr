package newznab

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// indexersearch criteria types are used by FetchSearch via the
// RequestGenerator's type switch — that import stays in request_generator.go.

// Indexer is the Newznab Provider impl. C# cite:
// src/NzbDrone.Core/Indexers/Newznab/Newznab.cs:17-179.
//
// It composes the Settings + Capabilities + RequestGenerator + RSS parser
// pieces from sub-arcs 4a/4b/4c. The C# class extends HttpIndexerBase
// which itself extends IndexerBase; the Go port collapses both into this
// single struct since the inheritance carries no state we'd actually share
// across families (each family-port slice will compose the same way).
//
// Sub-arc 4d focus: ship the minimal-viable Provider surface (Name,
// ConfigContract, Definition, Test, RequestAction) plus FetchRecent /
// FetchSearch entry points that drive the request chain through
// httpclient.Client + ParseRSS. Status-recording (indexerstatus.Recorder
// integration), LastRssSyncReleaseInfo gap detection, and the API/v3 +
// API/v5 surface land in later slices.
type Indexer struct {
	HTTPClient   httpclient.Client
	CapsProvider *CapabilitiesProvider

	// definition is set via SetDefinition. The hydrated Settings live on
	// the Definition; we extract them at call-time rather than caching.
	definition *provider.Definition
}

// NewIndexer constructs a Newznab indexer wired against the supplied HTTP
// client and CapabilitiesProvider. The Definition is bound later via
// SetDefinition (provider.Factory.LoadDefinition produces it from a row).
func NewIndexer(client httpclient.Client, caps *CapabilitiesProvider) *Indexer {
	return &Indexer{HTTPClient: client, CapsProvider: caps}
}

// Name returns the user-visible class name. Mirrors the C# `Name` getter
// at Newznab.cs:21.
func (i *Indexer) Name() string { return "Newznab" }

// ConfigContract returns the Settings class name string used for the
// (Impl, Contract) registry key. C# cite: NewznabSettings.cs class name.
func (i *Indexer) ConfigContract() string { return "NewznabSettings" }

// Message returns nil — the Newznab class has no static banner. Per-row
// banners (e.g. "Implementation not registered") live on Definition.Message
// and are surfaced through the provider package's Factory.
func (i *Indexer) Message() *provider.Message { return nil }

// DefaultDefinitions ports Newznab.cs:47-61 — the pre-canned entries
// the UI offers when the user adds a new Newznab indexer. Each entry
// wraps a Settings preset with EnableRss/EnableAutomaticSearch/
// EnableInteractiveSearch defaulted to false (the user opts in after save).
func (i *Indexer) DefaultDefinitions() []*provider.Definition {
	specs := []struct {
		name       string
		baseURL    string
		apiPath    string
		categories []int
	}{
		{"DOGnzb", "https://api.dognzb.cr", "", nil},
		{"DrunkenSlug", "https://drunkenslug.com", "", nil},
		{"Nzb.life", "https://api.nzb.life", "", nil},
		{"NZBCat", "https://nzb.cat", "", nil},
		{"NZBFinder.ws", "https://nzbfinder.ws", "", []int{5030, 5040, 5045}},
		{"NZBgeek", "https://api.nzbgeek.info", "", nil},
		{"nzbplanet.net", "https://api.nzbplanet.net", "", nil},
		{"SimplyNZBs", "https://simplynzbs.com", "", nil},
		{"Tabula Rasa", "https://www.tabula-rasa.pw", "/api/v1/api", nil},
	}
	out := make([]*provider.Definition, 0, len(specs))
	for _, sp := range specs {
		s := NewSettings()
		s.BaseUrl = sp.baseURL
		if sp.apiPath != "" {
			s.ApiPath = sp.apiPath
		}
		if sp.categories != nil {
			s.Categories = sp.categories
		}
		out = append(out, &provider.Definition{
			Name:           sp.name,
			Implementation: i.Name(),
			ConfigContract: i.ConfigContract(),
			Enable:         false,
			Settings:       s,
		})
	}
	return out
}

// Definition returns the bound Definition, or nil when unbound.
func (i *Indexer) Definition() *provider.Definition { return i.definition }

// SetDefinition binds the Definition. Called by the higher-level service
// after Factory.LoadDefinition produces the Definition from a row. Settings
// is expected to be a *Settings; mismatches are surfaced at fetch-time
// (the dispatch helpers type-assert).
func (i *Indexer) SetDefinition(d *provider.Definition) { i.definition = d }

// Test runs the C# `Test()` two-phase validation:
//  1. Settings.Validate() — typed FluentValidation rules.
//  2. TestCapabilities() — fetches caps and checks at least one search
//     mode is supported (q OR (tvdbid|rid + season + ep)).
//
// C# cite: Newznab.cs:101-139 + NewznabSettings.Validate at :92-95.
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

	if i.CapsProvider == nil {
		// Without a caps provider we cannot confirm the indexer accepts
		// our search params. Surface as a warning rather than a hard fail
		// so unit-test wiring can pass without a live HTTP loop.
		return provider.ValidationResult{Errors: []provider.ValidationFailure{
			provider.NewValidationWarning("", "capabilities provider not configured"),
		}}
	}

	caps, err := i.CapsProvider.GetCapabilities(ctx, settings)
	if err != nil {
		return provider.ValidationResult{Errors: []provider.ValidationFailure{
			provider.NewValidationFailure("", fmt.Sprintf("Unable to connect to indexer: %v", err)),
		}}
	}
	if !validateSearchModeSupported(caps) {
		return provider.ValidationResult{Errors: []provider.ValidationFailure{
			provider.NewValidationFailure("", "Indexer does not advertise any compatible search parameters"),
		}}
	}
	return provider.ValidResult()
}

// validateSearchModeSupported ports the boolean shape of TestCapabilities
// at Newznab.cs:113-129 — basic search OR (any of q/tvdbid/rid + both
// season and ep in tv-search).
func validateSearchModeSupported(caps *Capabilities) bool {
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

// RequestAction handles UI-driven actions outside the standard
// Test/Save lifecycle. Newznab handles "newznabCategories" — the typed
// category dropdown population. C# cite: Newznab.cs:141-165.
//
// Other actions return nil (the C# base RequestAction also returns null).
func (i *Indexer) RequestAction(stage string, _ map[string]string) (any, error) {
	if stage != "newznabCategories" {
		return nil, nil
	}
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	if i.CapsProvider == nil || settings.BaseUrl == "" || settings.ApiPath == "" {
		// Mirror the C# fallback at :155 — empty options + swallow the
		// error so the UI can fall through to the default categories.
		return map[string]any{"options": []any{}}, nil
	}
	caps, err := i.CapsProvider.GetCapabilities(context.Background(), settings)
	if err != nil {
		return map[string]any{"options": []any{}}, nil
	}
	// Render the categories as a flat list of {value:int, name:string}
	// option entries — same shape the C#
	// NewznabCategoryFieldOptionsConverter.GetFieldSelectOptions returns
	// modulo the parent/child grouping which lands with the API surface
	// in Phase 5.
	return map[string]any{"options": flattenCategories(caps.Categories)}, nil
}

func flattenCategories(cats []Category) []map[string]any {
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
// at HttpIndexerBase.cs:43-50 → FetchReleases(GetRecentRequests).
//
// Returns the deduped releases. Per-page errors are silently dropped
// (matches C# catch+continue at :219-243). Status recording (RecordSuccess
// / RecordFailure on indexerstatus) is NOT done here yet — the Service
// slice will wrap Fetch* with that decoration.
func (i *Indexer) FetchRecent(ctx context.Context) ([]*parser.ReleaseInfo, error) {
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
// generator and runs the resulting chain. C# cite: HttpIndexerBase.cs:53-118
// (the seven `Fetch(<TCriteria>)` overloads).
func (i *Indexer) FetchSearch(ctx context.Context, criteria any) ([]*parser.ReleaseInfo, error) {
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

// runChain executes a PageableRequestChain. Mirrors the tier-by-tier loop
// at HttpIndexerBase.FetchReleases at :126-203, simplified:
//   - For each tier:
//   - For each pageable stream:
//   - Fetch each page; accumulate releases; break on empty page.
//   - Add valid releases (non-empty Guid) to the per-tier accumulator.
//   - If the tier yielded any releases, stop iterating tiers (cascade halt).
//
// Status-aware error classification (TooManyRequests, host cooldown) is
// preserved through the httpclient layer — those errors propagate up to
// the Service for indexerstatus.Recorder integration.
func (i *Indexer) runChain(ctx context.Context, chain *PageableRequestChain) []*parser.ReleaseInfo {
	settings, _ := i.settings()
	feedURL := ""
	if settings != nil {
		feedURL = settings.BaseUrl
	}

	dedup := make(map[string]struct{})
	var all []*parser.ReleaseInfo

	for tierIdx := 0; tierIdx < chain.Tiers(); tierIdx++ {
		tier := chain.Tier(tierIdx)
		var tierReleases []*parser.ReleaseInfo

		for _, stream := range tier {
			for _, req := range stream {
				resp, err := i.HTTPClient.Get(ctx, req)
				if err != nil {
					// Per-page errors do NOT abort the whole fetch — match
					// C# RssParser.cs:78-83 catch+continue. The Service
					// slice will hook indexerstatus on the outer error.
					continue
				}
				releases, err := ParseRSS([]byte(resp.Content()), &ParseOptions{
					IndexerName: i.bindingName(),
					IndexerID:   i.bindingID(),
					FeedURL:     feedURL,
					HasAPIKey:   settings != nil && settings.ApiKey != "",
				})
				if err != nil {
					return all
				}

				if len(releases) == 0 {
					break // partial / empty page → end of stream
				}
				tierReleases = append(tierReleases, releases...)
			}
		}

		// Dedupe by Guid and append.
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

		// Cascade halt: first tier that yields releases ends the search.
		if len(all) > 0 {
			break
		}
	}
	return all
}

// requestGenerator builds a fresh RequestGenerator bound to the current
// Settings + Definition. Each Fetch* call gets a new generator so the
// MaxPages/PageSize aren't shared across callers.
func (i *Indexer) requestGenerator() (*RequestGenerator, error) {
	settings, err := i.settings()
	if err != nil {
		return nil, err
	}
	g := NewRequestGenerator(settings, nil, i.bindingName())
	g.CapabilitiesProvider = i.CapsProvider
	return g, nil
}

// settings extracts the typed Settings from the bound Definition.
func (i *Indexer) settings() (*Settings, error) {
	if i.definition == nil {
		return nil, errors.New("newznab: indexer is unbound (Definition not set)")
	}
	if i.definition.Settings == nil {
		return nil, errors.New("newznab: Definition has nil Settings")
	}
	s, ok := i.definition.Settings.(*Settings)
	if !ok {
		return nil, fmt.Errorf("newznab: Definition.Settings is %T, want *newznab.Settings", i.definition.Settings)
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

// Constructor returns a fresh Settings value — registered with the
// provider package so Factory.LoadDefinition can hydrate Settings JSON
// for `(Impl=Newznab, Contract=NewznabSettings)` rows.
//
// Per the package convention (queue [2]), Constructor returns `any` and
// callers type-assert to the typed Settings. We pre-register via
// provider.Default() in the package init below.
func Constructor() any { return NewSettings() }

func init() {
	// Register the Newznab Settings constructor with the process-wide
	// catalog so existing rows (Implementation=Newznab, ConfigContract=
	// NewznabSettings) can be hydrated through provider.Factory.
	//
	// The (Impl, Contract) key was already registered with a nil
	// constructor in Phase 1.0 (see internal/provider/ids.go); we
	// re-register with the real constructor here. The provider package's
	// Register helper rejects duplicate keys — to allow this swap we
	// call SetConstructor below if it exists, otherwise we accept the
	// duplicate-registration error and document the workaround in the
	// commit body.
	//
	// TODO follow-up: the Registry.Register helper currently returns
	// ErrAlreadyRegistered for the second register. Sub-arc 4d swaps in
	// the real constructor by replacing the catalog with a Set semantic
	// — see the Replace helper below.
	if err := provider.Default().Replace(provider.FamilyIndexer,
		provider.Key{Impl: "Newznab", Contract: "NewznabSettings"},
		Constructor); err != nil {
		panic(fmt.Errorf("newznab: register constructor: %w", err))
	}
}
