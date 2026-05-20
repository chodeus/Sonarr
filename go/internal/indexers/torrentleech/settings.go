// Package torrentleech is the Go port of NzbDrone.Core.Indexers.Torrentleech.
//
// Torrentleech is a private-tracker RSS feed at rss.torrentleech.org. The
// per-user ApiKey is embedded in the URL path: `<BaseUrl>/<ApiKey>`. No
// search support — RSS-only (SupportsSearch=false on the C# side at
// `Torrentleech.cs:14`); this Go port mirrors that.
//
// C# files mirrored:
//   - Torrentleech.cs               (32 LOC; Indexer config)
//   - TorrentleechRequestGenerator.cs (60 LOC; single-URL RSS request)
//   - TorrentleechSettings.cs       (60 LOC; BaseUrl/ApiKey/MinimumSeeders/...)
//
// Slice 5.J146 (first of the 7-indexer B.6 long-tail; Tier A by
// real-world share alongside IPTorrents).
package torrentleech

import (
	"sonarr.tv/sonarr/internal/indexers/torznab"
	"sonarr.tv/sonarr/internal/provider"
)

// Identifiers used by the registry. Wire-stable; the persisted
// `IndexerDefinitions.Implementation` and `.ConfigContract` columns
// store these literal strings.
// The persisted Implementation is "Torrentleech" (lowercase `l` —
// C# class-name-derived). The user-visible display name is
// "TorrentLeech" (capital `L` — C# `Torrentleech.Name` override).
// Don't confuse: Implementation is the wire-stable identifier; the
// display name lives on [Indexer.Name] separately.
const (
	Implementation = "Torrentleech"
	ConfigContract = "TorrentleechSettings"
	DisplayName    = "TorrentLeech"
)

// defaultBaseURL mirrors the C# `TorrentleechSettings.cs:28`
// constructor default. The user can override in the UI.
const defaultBaseURL = "http://rss.torrentleech.org"

// minimumSeedersDefault mirrors IndexerDefaults.MINIMUM_SEEDERS (=1).
const minimumSeedersDefault = 1

// Settings ports `TorrentleechSettings.cs:22-60`.
//
// Wire shape parity:
//   - BaseUrl: the RSS host (default `http://rss.torrentleech.org`).
//   - ApiKey: per-user feed key. Required.
//   - MinimumSeeders / SeedCriteria / RejectBlocklisted /
//     MultiLanguages / FailDownloads: standard torrent-indexer
//     posture; same shape as torznab + torrentrss.
type Settings struct {
	BaseUrl                                     string                       `json:"baseUrl"`
	ApiKey                                      string                       `json:"apiKey"`
	MinimumSeeders                              int                          `json:"minimumSeeders"`
	SeedCriteria                                torznab.SeedCriteriaSettings `json:"seedCriteria"`
	RejectBlocklistedTorrentHashesWhileGrabbing bool                         `json:"rejectBlocklistedTorrentHashesWhileGrabbing"`
	MultiLanguages                              []int                        `json:"multiLanguages"`
	FailDownloads                               []int                        `json:"failDownloads"`
}

// NewSettings builds a Settings populated with the C# constructor
// defaults at `TorrentleechSettings.cs:26-32`.
func NewSettings() *Settings {
	return &Settings{
		BaseUrl:        defaultBaseURL,
		MinimumSeeders: minimumSeedersDefault,
		SeedCriteria:   torznab.SeedCriteriaSettings{},
		MultiLanguages: []int{},
		FailDownloads:  []int{},
	}
}

// Validate ports `TorrentleechSettingsValidator.cs:11-20`.
//
//  1. BaseUrl must be a valid http(s):// root URL.
//  2. ApiKey must be non-empty.
//  3. SeedCriteria warnings (delegated; same posture as torznab/torrentrss).
func (s *Settings) Validate() provider.ValidationResult {
	var failures []provider.ValidationFailure
	if !isValidRootURL(s.BaseUrl) {
		failures = append(failures, provider.NewValidationFailure(
			"BaseUrl",
			"must be valid URL that starts with http(s)://",
		))
	}
	if s.ApiKey == "" {
		failures = append(failures, provider.NewValidationFailure(
			"ApiKey",
			"must not be empty",
		))
	}
	failures = append(failures, validateSeedCriteria(&s.SeedCriteria)...)
	return provider.ValidationResult{Errors: failures}
}

// validateSeedCriteria duplicates the warnings emitted by
// torrentrss.validateSeedCriteria. Kept local for the same reason —
// the SeedCriteriaSettings type is reused from torznab but the
// validator-traversal lives next to the consumer.
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
