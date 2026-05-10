package newznab

// Capabilities ports src/NzbDrone.Core/Indexers/Newznab/NewznabCapabilities.cs:5-25.
//
// Built once per provider config (settings JSON hash) and cached in
// the CapabilitiesProvider (default 7-day TTL, mirroring C# :37). The
// `?t=caps` XML response from a Newznab indexer populates these fields;
// when the fetch fails or the document is malformed, the consumer
// re-uses the default-constructed Capabilities (non-fatal).
type Capabilities struct {
	DefaultPageSize             int
	MaxPageSize                 int
	SupportedSearchParameters   []string // nil when search is not advertised
	SupportedTvSearchParameters []string // nil when tv-search is not advertised
	SupportsAggregateIDSearch   bool
	TextSearchEngine            string
	TvTextSearchEngine          string
	Categories                  []Category
}

// NewCapabilities returns a Capabilities populated with the C# defaults
// at NewznabCapabilities.cs:13-22 — page sizes 100/100, search params
// {"q"}, tv-search params {"q","rid","season","ep"}, both engines
// "sphinx", no aggregate-ID search support.
//
// The C# comment on TvTextSearchEngine notes "This should remain 'sphinx'
// for older newznab installs" — the default doubles as a fallback when
// the caps response omits the searchEngine attribute. Same for the
// "rid" param — kept for backwards compat with older Newznab versions.
func NewCapabilities() *Capabilities {
	return &Capabilities{
		DefaultPageSize:             100,
		MaxPageSize:                 100,
		SupportedSearchParameters:   []string{"q"},
		SupportedTvSearchParameters: []string{"q", "rid", "season", "ep"},
		SupportsAggregateIDSearch:   false,
		TextSearchEngine:            "sphinx",
		TvTextSearchEngine:          "sphinx",
		Categories:                  nil,
	}
}

// Category ports NewznabCategory at NewznabCapabilities.cs:28-36. Top-level
// entries may carry a Subcategories slice; subcategories themselves are
// flat (no further nesting).
type Category struct {
	ID            int
	Name          string
	Description   string
	Subcategories []Category
}
