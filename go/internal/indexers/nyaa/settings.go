// Package nyaa is the Go port of NzbDrone.Core.Indexers.Nyaa.
//
// Nyaa is a public anime-tracker RSS feed (typical host:
// `https://nyaa.si/`). The user configures BaseUrl + optional
// AdditionalParameters; the feed URL is
// `<BaseUrl>/?page=rss<AdditionalParameters>`. Supports keyword
// search via additional `&term=...` query params; the per-criteria
// dispatch is anime-focused (AbsoluteEpisode + standard-format S/E).
//
// C# files mirrored:
//   - Nyaa.cs                 (30 LOC; Indexer config)
//   - NyaaRequestGenerator.cs (130 LOC; per-criteria search URLs)
//   - NyaaSettings.cs         (65 LOC; BaseUrl + AdditionalParameters +
//     AnimeStandardFormatSearch + standard torrent settings)
//
// # J148 scope (RSS-only first slice)
//
// This slice ships FetchRecent + Test + the registry plumbing.
// FetchSearch returns empty; the per-criteria search-URL routing
// from NyaaRequestGenerator.cs:21-111 lands as a follow-up slice
// once the existing buildAdapter contract has a real consumer
// driving the criteria-typed dispatch.
package nyaa

import (
	"regexp"

	"sonarr.tv/sonarr/internal/indexers/torznab"
	"sonarr.tv/sonarr/internal/provider"
)

// Identifiers used by the registry. Matches the C# class names
// verbatim — no Name/Implementation divergence here.
const (
	Implementation = "Nyaa"
	ConfigContract = "NyaaSettings"
	DisplayName    = "Nyaa"
)

// defaultAdditionalParameters mirrors the C# constructor default at
// `NyaaSettings.cs:30` — restricts to category 1_0 (anime) with
// trusted-only filter=1.
const defaultAdditionalParameters = "&cats=1_0&filter=1"

// minimumSeedersDefault mirrors IndexerDefaults.MINIMUM_SEEDERS.
const minimumSeedersDefault = 1

// additionalParamsRegex ports the C# `Matches` at
// `NyaaSettings.cs:17` — AdditionalParameters must be a zero-or-more
// concatenation of `&key=value` pairs (alphanumeric values + `_`,
// case-insensitive). Empty string is valid.
var additionalParamsRegex = regexp.MustCompile(`^(?i)(&[a-z]+=[a-z0-9_]+)*$`)

// Settings ports `NyaaSettings.cs:23-65`.
type Settings struct {
	BaseUrl                                     string                       `json:"baseUrl"`
	AnimeStandardFormatSearch                   bool                         `json:"animeStandardFormatSearch"`
	AdditionalParameters                        string                       `json:"additionalParameters"`
	MinimumSeeders                              int                          `json:"minimumSeeders"`
	SeedCriteria                                torznab.SeedCriteriaSettings `json:"seedCriteria"`
	RejectBlocklistedTorrentHashesWhileGrabbing bool                         `json:"rejectBlocklistedTorrentHashesWhileGrabbing"`
	MultiLanguages                              []int                        `json:"multiLanguages"`
	FailDownloads                               []int                        `json:"failDownloads"`
}

// NewSettings builds a Settings with the C# constructor defaults at
// `NyaaSettings.cs:27-34`.
func NewSettings() *Settings {
	return &Settings{
		BaseUrl:              "",
		AdditionalParameters: defaultAdditionalParameters,
		MinimumSeeders:       minimumSeedersDefault,
		SeedCriteria:         torznab.SeedCriteriaSettings{},
		MultiLanguages:       []int{},
		FailDownloads:        []int{},
	}
}

// Validate ports `NyaaSettingsValidator.cs:14-20`.
func (s *Settings) Validate() provider.ValidationResult {
	var failures []provider.ValidationFailure
	if !isValidRootURL(s.BaseUrl) {
		failures = append(failures, provider.NewValidationFailure(
			"BaseUrl",
			"must be valid URL that starts with http(s)://",
		))
	}
	if !additionalParamsRegex.MatchString(s.AdditionalParameters) {
		failures = append(failures, provider.NewValidationFailure(
			"AdditionalParameters",
			"must be a concatenation of &key=value pairs",
		))
	}
	failures = append(failures, validateSeedCriteria(&s.SeedCriteria)...)
	return provider.ValidationResult{Errors: failures}
}

func validateSeedCriteria(c *torznab.SeedCriteriaSettings) []provider.ValidationFailure {
	var out []provider.ValidationFailure
	if c.SeedRatio != nil && *c.SeedRatio <= 0 {
		out = append(out, provider.NewValidationWarning("SeedRatio", "Should be greater than zero"))
	}
	if c.SeedTime != nil && *c.SeedTime <= 0 {
		out = append(out, provider.NewValidationWarning("SeedTime", "Should be greater than zero"))
	}
	if c.SeasonPackSeedRatio != nil && *c.SeasonPackSeedRatio <= 0 {
		out = append(out, provider.NewValidationWarning("SeasonPackSeedRatio", "Should be greater than zero"))
	}
	if c.SeasonPackSeedTime != nil && *c.SeasonPackSeedTime <= 0 {
		out = append(out, provider.NewValidationWarning("SeasonPackSeedTime", "Should be greater than zero"))
	}
	return out
}

// Compile-time: Settings implements provider.Config.
var _ provider.Config = (*Settings)(nil)
