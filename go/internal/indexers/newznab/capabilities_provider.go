package newznab

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/httpclient"
)

// CapabilitiesProvider fetches and caches Newznab `?t=caps` responses.
// C# cite: src/NzbDrone.Core/Indexers/Newznab/NewznabCapabilitiesProvider.cs:21-89.
//
// Cache key: the JSON-serialised Settings (mirrors `indexerSettings.ToJson()`
// at C# :34). TTL: 7 days, matching the C# `TimeSpan.FromDays(7)` argument.
// Fetch failures are NOT cached — a transient network error doesn't
// poison the cache for a week. Parse failures DO get the default
// Capabilities returned (mirroring the C# fallback at :91 — "using the
// defaults instead till Sonarr restarts"); these stay in cache so the
// indexer can keep working.
type CapabilitiesProvider struct {
	client httpclient.Client
	now    func() time.Time
	ttl    time.Duration

	mu    sync.Mutex
	cache map[string]capsCacheEntry
}

type capsCacheEntry struct {
	caps      *Capabilities
	expiresAt time.Time
}

// NewCapabilitiesProvider builds a provider backed by the given HTTP
// client. The cache TTL defaults to 7 days; tests that want a shorter
// TTL can construct via NewCapabilitiesProviderWithTTL.
func NewCapabilitiesProvider(client httpclient.Client) *CapabilitiesProvider {
	return NewCapabilitiesProviderWithTTL(client, 7*24*time.Hour)
}

// NewCapabilitiesProviderWithTTL is the test-friendly variant.
func NewCapabilitiesProviderWithTTL(client httpclient.Client, ttl time.Duration) *CapabilitiesProvider {
	return &CapabilitiesProvider{
		client: client,
		now:    time.Now,
		ttl:    ttl,
		cache:  map[string]capsCacheEntry{},
	}
}

// SetClock is a test seam.
func (p *CapabilitiesProvider) SetClock(now func() time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.now = now
}

// GetCapabilities returns the cached capabilities for s, fetching them
// if absent or expired. C# cite: NewznabCapabilitiesProvider.cs:32-37.
//
// On fetch error: returns the error AND the default Capabilities (the C#
// equivalent throws and lets callers fall back; in Go we surface both
// so the caller can decide whether to retry or proceed with defaults).
// On parse error: returns nil error + the default Capabilities (mirrors
// C# :91 fallback) and caches the result.
func (p *CapabilitiesProvider) GetCapabilities(ctx context.Context, s *Settings) (*Capabilities, error) {
	if s == nil {
		return nil, errors.New("newznab: nil settings")
	}
	key, err := cacheKey(s)
	if err != nil {
		return nil, fmt.Errorf("newznab: cache key: %w", err)
	}

	p.mu.Lock()
	if e, ok := p.cache[key]; ok && p.now().Before(e.expiresAt) {
		p.mu.Unlock()
		return e.caps, nil
	}
	p.mu.Unlock()

	caps, fetchErr := p.fetchCapabilities(ctx, s)
	if fetchErr != nil {
		// Don't cache fetch failures — see doc comment.
		return caps, fetchErr
	}

	p.mu.Lock()
	p.cache[key] = capsCacheEntry{caps: caps, expiresAt: p.now().Add(p.ttl)}
	p.mu.Unlock()
	return caps, nil
}

func cacheKey(s *Settings) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// fetchCapabilities issues GET <baseUrl><apiPath>?t=caps[&apikey=...] and
// parses the XML response. Mirrors NewznabCapabilitiesProvider.cs:39-93.
func (p *CapabilitiesProvider) fetchCapabilities(ctx context.Context, s *Settings) (*Capabilities, error) {
	u := strings.TrimRight(s.BaseUrl, "/") + strings.TrimRight(s.ApiPath, "/") + "?t=caps"
	if strings.TrimSpace(s.ApiKey) != "" {
		u += "&apikey=" + s.ApiKey
	}

	req, err := httpclient.NewRequest(u, httpclient.AcceptRSS)
	if err != nil {
		return NewCapabilities(), fmt.Errorf("build caps request: %w", err)
	}
	req.AllowAutoRedirect = true

	resp, err := p.client.Get(ctx, req)
	if err != nil {
		return NewCapabilities(), fmt.Errorf("fetch caps from %s: %w", s.BaseUrl, err)
	}

	caps, err := ParseCapabilities([]byte(resp.Content()))
	if err != nil {
		// Parse failure → cache the default per C# :91 fallback. The
		// caller doesn't see an error, but the row's Message banner
		// (one layer up) flags the misconfiguration.
		return NewCapabilities(), nil
	}
	return caps, nil
}

// xmlCaps is the wire-shape root element. C# uses XDocument querying
// directly; Go's encoding/xml is more idiomatic with a typed shape.
type xmlCaps struct {
	XMLName    xml.Name         `xml:"caps"`
	Limits     *xmlLimits       `xml:"limits"`
	Searching  *xmlSearching    `xml:"searching"`
	Categories *xmlCategoryList `xml:"categories"`
	// `error` element shape — per NewznabRssParser.CheckError (sub-arc 4b);
	// surfaced minimally here so caps requests reject HTML responses or
	// API-error XML cleanly.
	Error *xmlAPIError `xml:"error"`
}

type xmlLimits struct {
	Default int `xml:"default,attr"`
	Max     int `xml:"max,attr"`
}

type xmlSearching struct {
	Search   *xmlSearchEntry `xml:"search"`
	TvSearch *xmlSearchEntry `xml:"tv-search"`
}

type xmlSearchEntry struct {
	Available       string `xml:"available,attr"` // "yes" / "no"
	SupportedParams string `xml:"supportedParams,attr"`
	SearchEngine    string `xml:"searchEngine,attr"`
}

type xmlCategoryList struct {
	Categories []xmlCategory `xml:"category"`
}

type xmlCategory struct {
	ID            string      `xml:"id,attr"`
	Name          string      `xml:"name,attr"`
	Description   string      `xml:"description,attr"`
	Subcategories []xmlSubcat `xml:"subcat"`
}

type xmlSubcat struct {
	ID          string `xml:"id,attr"`
	Name        string `xml:"name,attr"`
	Description string `xml:"description,attr"`
}

type xmlAPIError struct {
	Code        string `xml:"code,attr"`
	Description string `xml:"description,attr"`
}

// ParseCapabilities ports the XDocument-based parse at C# :95-181 to
// encoding/xml. Returns the populated Capabilities. Returns an error
// for completely-malformed XML; for missing-but-optional elements (no
// <limits>, no <searching>, etc.) the corresponding default field is
// preserved.
func ParseCapabilities(body []byte) (*Capabilities, error) {
	caps := NewCapabilities()

	var doc xmlCaps
	if err := xml.Unmarshal(body, &doc); err != nil {
		return caps, fmt.Errorf("parse caps: %w", err)
	}
	if doc.Error != nil {
		return caps, fmt.Errorf("newznab API error %s: %s", doc.Error.Code, doc.Error.Description)
	}

	if doc.Limits != nil {
		if doc.Limits.Default > 0 {
			caps.DefaultPageSize = doc.Limits.Default
		}
		if doc.Limits.Max > 0 {
			caps.MaxPageSize = doc.Limits.Max
		}
	}

	if doc.Searching != nil {
		// Basic search
		if doc.Searching.Search == nil || !strings.EqualFold(doc.Searching.Search.Available, "yes") {
			caps.SupportedSearchParameters = nil
		} else {
			if doc.Searching.Search.SupportedParams != "" {
				caps.SupportedSearchParameters = splitCSV(doc.Searching.Search.SupportedParams)
			}
			if doc.Searching.Search.SearchEngine != "" {
				caps.TextSearchEngine = doc.Searching.Search.SearchEngine
			}
		}

		// TV search
		if doc.Searching.TvSearch == nil || !strings.EqualFold(doc.Searching.TvSearch.Available, "yes") {
			caps.SupportedTvSearchParameters = nil
		} else {
			if doc.Searching.TvSearch.SupportedParams != "" {
				caps.SupportedTvSearchParameters = splitCSV(doc.Searching.TvSearch.SupportedParams)
				caps.SupportsAggregateIDSearch = true
			}
			if doc.Searching.TvSearch.SearchEngine != "" {
				caps.TvTextSearchEngine = doc.Searching.TvSearch.SearchEngine
			}
		}
	}

	if doc.Categories != nil {
		for _, c := range doc.Categories.Categories {
			id, err := strconv.Atoi(c.ID)
			if err != nil {
				continue
			}
			cat := Category{
				ID:          id,
				Name:        c.Name,
				Description: c.Description,
			}
			for _, sc := range c.Subcategories {
				sid, err := strconv.Atoi(sc.ID)
				if err != nil {
					continue
				}
				cat.Subcategories = append(cat.Subcategories, Category{
					ID:          sid,
					Name:        sc.Name,
					Description: sc.Description,
				})
			}
			caps.Categories = append(caps.Categories, cat)
		}
	}

	return caps, nil
}

// splitCSV is the comma-separated-no-trim split the C# code does at :131.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
