// Package iptorrents is the Go port of NzbDrone.Core.Indexers.IPTorrents.
//
// IPTorrents is a private-tracker RSS feed where the entire feed URL
// (host + path + per-user query params) is configured by the user as
// BaseUrl. Unlike Torrentleech the ApiKey isn't a separate field —
// the credentials ride embedded in the query string the user pastes
// from the IPTorrents preferences page. RSS-only, no search.
//
// C# files mirrored:
//   - IPTorrents.cs               (32 LOC; Indexer config)
//   - IPTorrentsRequestGenerator.cs (60 LOC; single-URL RSS request)
//   - IPTorrentsSettings.cs       (63 LOC; BaseUrl + standard
//     torrent-indexer settings)
//
// Slice 5.J147 (second of the 7-indexer B.6 long-tail; the other
// Tier A port).
package iptorrents

import (
	"regexp"

	"sonarr.tv/sonarr/internal/indexers/torznab"
	"sonarr.tv/sonarr/internal/provider"
)

// Identifiers used by the registry. The C# Implementation key is
// "IPTorrents" (verbatim class name). The user-visible Name override
// is "IP Torrents" (with a space; C# `IPTorrents.cs:11`).
const (
	Implementation = "IPTorrents"
	ConfigContract = "IPTorrentsSettings"
	DisplayName    = "IP Torrents"
)

// minimumSeedersDefault mirrors IndexerDefaults.MINIMUM_SEEDERS.
const minimumSeedersDefault = 1

// rssURLRegex mirrors the C# Matches at `IPTorrentsSettings.cs:19`
// (`(?:/|t\.)rss\?.+$`) — the user-pasted URL MUST end in an `rss?`
// query string (typical shape: `https://iptorrents.com/t/rss?...`).
var rssURLRegex = regexp.MustCompile(`(?:/|t\.)rss\?.+$`)

// directDownloadRegex mirrors `IPTorrentsSettings.cs:21` — the URL
// SHOULD include the `;download` segment that flags direct-binary
// payload links instead of HTML torrent pages. Warning when missing
// (matches the C# WithMessage at :22).
var directDownloadRegex = regexp.MustCompile(`(?:/|t\.)rss\?.+;download(?:;|$)`)

// Settings ports `IPTorrentsSettings.cs:29-62`.
//
// Compared to torrentleech: no ApiKey field — the user-pasted
// BaseUrl carries the per-user credentials embedded in the query
// string ("u=USER;tp=TOKEN;...").
type Settings struct {
	BaseUrl                                     string                       `json:"baseUrl"`
	MinimumSeeders                              int                          `json:"minimumSeeders"`
	SeedCriteria                                torznab.SeedCriteriaSettings `json:"seedCriteria"`
	RejectBlocklistedTorrentHashesWhileGrabbing bool                         `json:"rejectBlocklistedTorrentHashesWhileGrabbing"`
	MultiLanguages                              []int                        `json:"multiLanguages"`
	FailDownloads                               []int                        `json:"failDownloads"`
}

// NewSettings builds a Settings with the C# constructor defaults at
// `:33-38`. BaseUrl is empty — the user must fill it in.
func NewSettings() *Settings {
	return &Settings{
		MinimumSeeders: minimumSeedersDefault,
		SeedCriteria:   torznab.SeedCriteriaSettings{},
		MultiLanguages: []int{},
		FailDownloads:  []int{},
	}
}

// Validate ports `IPTorrentsSettingsValidator.cs:15-26`.
//
//  1. BaseUrl must be a valid http(s):// root URL.
//  2. BaseUrl must match the rss-query-string regex (hard failure).
//  3. BaseUrl SHOULD also include the `;download` direct-link flag
//     (warning, not error — matches C# WithMessage).
//  4. SeedCriteria warnings (delegated).
func (s *Settings) Validate() provider.ValidationResult {
	var failures []provider.ValidationFailure
	if !isValidRootURL(s.BaseUrl) {
		failures = append(failures, provider.NewValidationFailure(
			"BaseUrl",
			"must be valid URL that starts with http(s)://",
		))
	}
	if s.BaseUrl != "" && !rssURLRegex.MatchString(s.BaseUrl) {
		failures = append(failures, provider.NewValidationFailure(
			"BaseUrl",
			"must be an IPTorrents RSS feed URL (ends with /rss?...)",
		))
	}
	if s.BaseUrl != "" && rssURLRegex.MatchString(s.BaseUrl) && !directDownloadRegex.MatchString(s.BaseUrl) {
		failures = append(failures, provider.NewValidationWarning(
			"BaseUrl",
			"Use Direct Download Url (;download)",
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
