// Package newznab is the Go port of NzbDrone.Core.Indexers.Newznab.
//
// Newznab is the most-common Usenet indexer protocol. Each row of the
// Indexers table with Implementation="Newznab" carries a NewznabSettings
// JSON blob in its Settings column; the runtime hydrates that blob via
// provider.Factory.LoadDefinition and dispatches RSS-feed pulls + search
// queries through the shared httpclient.Client.
//
// This package is split into:
//   - settings.go       — typed Settings + Validate
//   - capabilities.go   — Capabilities + Category structs
//   - capabilities_provider.go — `?t=caps` XML fetcher with TTL cache
//   - rss_parser.go     — NZB-XML response → ReleaseInfo (sub-arc 4b)
//   - request_generator.go — search→HttpRequest URL builder (sub-arc 4b)
//   - newznab.go        — Indexer struct that ties it all together (sub-arc 4b)
//
// C# tree mirrored: src/NzbDrone.Core/Indexers/Newznab/*.
package newznab

import (
	"net/url"
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/provider"
)

// Settings ports src/NzbDrone.Core/Indexers/Newznab/NewznabSettings.cs:53-100.
//
// JSON tag names mirror the C# `[FieldDefinition]` member names lowered
// to camelCase (matching Sonarr's STJson serialization defaults), so the
// embedded blob is byte-for-byte compatible with existing C#-written
// rows.
type Settings struct {
	BaseUrl                   string `json:"baseUrl"`
	ApiPath                   string `json:"apiPath"`
	ApiKey                    string `json:"apiKey"`
	Categories                []int  `json:"categories"`
	AnimeCategories           []int  `json:"animeCategories"`
	AnimeStandardFormatSearch bool   `json:"animeStandardFormatSearch"`
	AdditionalParameters      string `json:"additionalParameters"`
	MultiLanguages            []int  `json:"multiLanguages"`
	FailDownloads             []int  `json:"failDownloads"`
}

// NewSettings returns a Settings populated with the C# constructor
// defaults (NewznabSettings.cs:60-65): ApiPath=/api,
// Categories=[5030,5040], AnimeCategories=[], MultiLanguages=[],
// FailDownloads=[].
func NewSettings() *Settings {
	return &Settings{
		ApiPath:         "/api",
		Categories:      []int{5030, 5040},
		AnimeCategories: []int{},
		MultiLanguages:  []int{},
		FailDownloads:   []int{},
	}
}

// apiKeyAllowList ports the host fragments at NewznabSettings.cs:14-23.
// When BaseUrl contains any of these substrings (case-insensitive),
// the validator requires ApiKey to be non-empty.
var apiKeyAllowList = []string{
	"nzbs.org",
	"nzb.life",
	"dognzb.cr",
	"nzbplanet.net",
	"nzbid.org",
	"nzbndx.com",
	"nzbindex.in",
}

// shouldHaveAPIKey ports the static helper at NewznabSettings.cs:27-30.
func shouldHaveAPIKey(s *Settings) bool {
	if s.BaseUrl == "" {
		return false
	}
	lower := strings.ToLower(s.BaseUrl)
	for _, host := range apiKeyAllowList {
		if strings.Contains(lower, host) {
			return true
		}
	}
	return false
}

// additionalParametersRegex ports the regex at NewznabSettings.cs:32.
// Matches one or more `&key=value` pairs, anchored anywhere in the
// AdditionalParameters string. The C# regex is `(&.+?\=.+?)+`.
var additionalParametersRegex = regexp.MustCompile(`(&.+?\=.+?)+`)

// urlBaseRegex ports the regex at RuleBuilderExtensions.cs:43 (ValidUrlBase).
// Negative lookahead for `^/?https?://host` patterns — i.e. ApiPath must
// not be a full URL. We approximate with a positive predicate because
// Go's regexp does not support lookahead.
var urlBaseHostRegex = regexp.MustCompile(`(?i)^/?https?://[-_a-z0-9.]+`)

// Validate ports the FluentValidation rules at NewznabSettings.cs:34-50.
//
// Rules (in C# order):
//  1. Either Categories or AnimeCategories must be non-empty.
//  2. BaseUrl must be a valid URL starting with http(s)://.
//  3. ApiPath must be a relative URL path (NOT absolute).
//  4. ApiKey must be non-empty when BaseUrl matches the allowlist.
//  5. AdditionalParameters, when non-empty, must match the
//     `(&key=value)+` regex.
func (s *Settings) Validate() provider.ValidationResult {
	var failures []provider.ValidationFailure

	// Rule 1 — Categories OR AnimeCategories
	if len(s.Categories) == 0 && len(s.AnimeCategories) == 0 {
		failures = append(failures, provider.NewValidationFailure(
			"",
			"Either 'Categories' or 'Anime Categories' must be provided",
		))
	}

	// Rule 2 — BaseUrl must be a valid URL with http(s)://
	if !isValidRootURL(s.BaseUrl) {
		failures = append(failures, provider.NewValidationFailure(
			"BaseUrl",
			"must be valid URL that starts with http(s)://",
		))
	}

	// Rule 3 — ApiPath must NOT be an absolute URL
	if urlBaseHostRegex.MatchString(s.ApiPath) {
		failures = append(failures, provider.NewValidationFailure(
			"ApiPath",
			"Must be a valid URL path (ie: '/api')",
		))
	}

	// Rule 4 — ApiKey required for allowlist hosts
	if shouldHaveAPIKey(s) && strings.TrimSpace(s.ApiKey) == "" {
		failures = append(failures, provider.NewValidationFailure(
			"ApiKey",
			"'ApiKey' must not be empty",
		))
	}

	// Rule 5 — AdditionalParameters regex
	if strings.TrimSpace(s.AdditionalParameters) != "" {
		if !additionalParametersRegex.MatchString(s.AdditionalParameters) {
			failures = append(failures, provider.NewValidationFailure(
				"AdditionalParameters",
				"must match parameter format (e.g. '&key=value')",
			))
		}
	}

	return provider.ValidationResult{Errors: failures}
}

// isValidRootURL ports IsValidUrl + StartsWith("http") at
// RuleBuilderExtensions.cs:35-39 + NzbDrone.Common.Extensions.UrlExtensions.IsValidUrl.
//
// Rules:
//   - non-empty
//   - parses as an absolute URL
//   - scheme is http or https
//   - host is set
func isValidRootURL(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	return true
}

// Compile-time: Settings implements provider.Config.
var _ provider.Config = (*Settings)(nil)
