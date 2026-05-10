// Package torznab is the Go port of NzbDrone.Core.Indexers.Torznab.
//
// Torznab is the Newznab protocol superset for torrents. The wire format
// is identical apart from a different attribute namespace
// (`http://torznab.com/schemas/2015/feed` vs Newznab's
// `http://www.newznab.com/DTD/2010/feeds/attributes/`) and the
// torrent-specific attrs (magneturl, infohash, seeders, peers, leechers,
// downloadvolumefactor, uploadvolumefactor, tags). C# Torznab.cs reuses
// NewznabRequestGenerator unchanged — only the Settings + RssParser
// differ.
//
// This package is split into:
//   - settings.go     — TorznabSettings (extends NewznabSettings) + Validate
//   - rss_parser.go   — torrent-specific ParseRSS producing *TorrentInfo
//   - torznab.go      — Indexer Provider impl
//
// C# tree mirrored: src/NzbDrone.Core/Indexers/Torznab/*.
package torznab

import (
	"sonarr.tv/sonarr/internal/indexers/newznab"
	"sonarr.tv/sonarr/internal/provider"
)

// minimumSeedersDefault mirrors src/NzbDrone.Core/Indexers/IndexerDefaults.cs:5.
const minimumSeedersDefault = 1

// SeedCriteriaSettings mirrors
// src/NzbDrone.Core/Indexers/SeedCriteriaSettings.cs:59-83.
//
// Pointer fields are nullable in C# (`double?` / `int?`). The Go port uses
// pointers to mirror the nullable-vs-zero semantics. The Validate path
// (in settings.go below) only emits warnings for explicit non-positive
// values.
type SeedCriteriaSettings struct {
	SeedRatio           *float64 `json:"seedRatio,omitempty"`
	SeedTime            *int     `json:"seedTime,omitempty"`
	SeasonPackSeedGoal  int      `json:"seasonPackSeedGoal,omitempty"`
	SeasonPackSeedRatio *float64 `json:"seasonPackSeedRatio,omitempty"`
	SeasonPackSeedTime  *int     `json:"seasonPackSeedTime,omitempty"`
}

// Settings ports src/NzbDrone.Core/Indexers/Torznab/TorznabSettings.cs:45-83.
//
// Embeds newznab.Settings to inherit BaseUrl/ApiPath/ApiKey/Categories/
// AnimeCategories/AdditionalParameters/MultiLanguages/FailDownloads —
// faithful to the C# `class TorznabSettings : NewznabSettings` shape.
//
// JSON tags on the embedded fields are inherited (Go's encoding/json
// flattens anonymous embedded structs). The added MinimumSeeders +
// SeedCriteria + RejectBlocklistedTorrentHashesWhileGrabbing fields
// land at the top level.
type Settings struct {
	newznab.Settings

	MinimumSeeders                              int                  `json:"minimumSeeders"`
	SeedCriteria                                SeedCriteriaSettings `json:"seedCriteria"`
	RejectBlocklistedTorrentHashesWhileGrabbing bool                 `json:"rejectBlocklistedTorrentHashesWhileGrabbing"`
}

// NewSettings builds a Settings populated with the C# constructor defaults
// at TorznabSettings.cs:50-53 (MinimumSeeders=1) plus the inherited
// Newznab defaults from newznab.NewSettings.
func NewSettings() *Settings {
	base := newznab.NewSettings()
	return &Settings{
		Settings:       *base,
		MinimumSeeders: minimumSeedersDefault,
		SeedCriteria:   SeedCriteriaSettings{},
	}
}

// Validate ports the Torznab validator at TorznabSettings.cs:11-43.
//
// The C# validator declares its own rule set that REPLICATES — but does
// not inherit from — the Newznab rules. The notable divergence: the
// ApiKeyAllowList for Torznab is `Array.Empty<string>()` so the
// ApiKey-required-when-host-is-on-allowlist rule never fires.
//
// The Go port runs Newznab's Validate (for the shared rules: BaseUrl
// ValidRootUrl, ApiPath ValidUrlBase, Categories-or-AnimeCategories
// non-empty, AdditionalParameters regex) and FILTERS OUT any ApiKey
// failure, then layers the SeedCriteria warnings on top. Filtering
// rather than re-implementing keeps the rule-set authoritative in one
// place (newznab.Settings.Validate).
func (s *Settings) Validate() provider.ValidationResult {
	r := s.Settings.Validate()
	// Strip the ApiKey-allowlist failure that Newznab's validator would
	// surface for hosts on its allowlist — Torznab's allowlist is empty.
	filtered := r.Errors[:0]
	for _, f := range r.Errors {
		if f.PropertyName == "ApiKey" {
			continue
		}
		filtered = append(filtered, f)
	}
	r.Errors = filtered
	r.Errors = append(r.Errors, validateSeedCriteria(&s.SeedCriteria)...)
	return r
}

// validateSeedCriteria ports the SeedCriteriaSettingsValidator constructor
// at SeedCriteriaSettings.cs:10-57. We only emit the always-on warning
// rules here (the indexer-specific minimum-floor rules at :32-56 require
// indexer-specific values which Torznab does not supply by default —
// IPTorrents is the only known caller; flagged as follow-up if needed).
func validateSeedCriteria(c *SeedCriteriaSettings) []provider.ValidationFailure {
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
