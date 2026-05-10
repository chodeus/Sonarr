// Package torrentrss is the Go port of NzbDrone.Core.Indexers.TorrentRss.
//
// TorrentRss is the generic RSS-2.0 fallback for torrent indexers that
// don't speak Torznab. The C# tree calls it "Torrent RSS Feed" — users
// configure a single feed URL + optional cookie header, and Sonarr
// auto-detects the per-feed format on first use.
//
// This package ships:
//   - settings.go    — Settings (BaseUrl/Cookie/MinimumSeeders/SeedCriteria/etc.)
//   - ParserSettings (per-feed format knobs)
//   - parser.go      — generic RSS torrent parser
//   - torrentrss.go  — Indexer Provider impl (SupportsSearch=false)
//
// C# tree mirrored: src/NzbDrone.Core/Indexers/TorrentRss/* +
// src/NzbDrone.Core/Indexers/TorrentRssParser.cs.
//
// Phase 4 scope: ship a pragmatic minimum. The C# TorrentRssParserFactory
// performs first-fetch format auto-detection (TorrentRssSettingsDetector,
// 315 LOC); the Go port lets users configure the per-feed knobs directly
// via ParserSettings. Auto-detection lands as a follow-up if the typical
// Sonarr deployment relies on it.
package torrentrss

import (
	"sonarr.tv/sonarr/internal/indexers/torznab"
	"sonarr.tv/sonarr/internal/provider"
)

// minimumSeedersDefault mirrors src/NzbDrone.Core/Indexers/IndexerDefaults.cs:5.
const minimumSeedersDefault = 1

// ParserSettings carries the per-feed format knobs the user configures
// after first-detection. C# cite:
// src/NzbDrone.Core/Indexers/TorrentRss/TorrentRssIndexerParserSettings.cs.
//
// All fields are zero-valued by default, which means: use the enclosure
// for download URL + length, no description-based size/seeder parsing,
// no custom element names. That matches the C# default-Detector path
// where the user hasn't yet customised.
type ParserSettings struct {
	UseEZTVFormat             bool `json:"useEZTVFormat,omitempty"`
	ParseSeedersInDescription bool `json:"parseSeedersInDescription,omitempty"`
	UseEnclosureUrl           bool `json:"useEnclosureUrl,omitempty"`
	UseEnclosureLength        bool `json:"useEnclosureLength,omitempty"`
	ParseSizeInDescription    bool `json:"parseSizeInDescription,omitempty"`
	// SizeElementName overrides the default <enclosure length>; common
	// values are "size", "torrent_filesize_bytes", "torrent:contentlength".
	SizeElementName string `json:"sizeElementName,omitempty"`
	// SeedsElementName / PeersElementName / MagnetElementName /
	// InfoHashElementName let the user point the parser at non-RSS-standard
	// elements. C# cite: TorrentRssParser.cs:14-32.
	SeedsElementName    string `json:"seedsElementName,omitempty"`
	PeersElementName    string `json:"peersElementName,omitempty"`
	MagnetElementName   string `json:"magnetElementName,omitempty"`
	InfoHashElementName string `json:"infoHashElementName,omitempty"`
	// CalculatePeersAsSum: when true, peers = peersElement + seeds (some
	// indexers treat <peers> as leechers-only).
	CalculatePeersAsSum bool `json:"calculatePeersAsSum,omitempty"`
}

// Settings ports src/NzbDrone.Core/Indexers/TorrentRss/TorrentRssIndexerSettings.cs:21-62.
//
// The parser-config fields the C# code threads through TorrentRssParserFactory
// land here as an embedded ParserSettings struct so the wire shape stays
// flat enough for the API/v3 settings round-trip. (The C# Settings class
// doesn't expose ParserSettings on the wire — it's discovered at runtime —
// but exposing them statically is the simpler fit for the Go port until
// auto-detection lands as a follow-up.)
type Settings struct {
	BaseUrl                                     string                       `json:"baseUrl"`
	Cookie                                      string                       `json:"cookie"`
	AllowZeroSize                               bool                         `json:"allowZeroSize"`
	MinimumSeeders                              int                          `json:"minimumSeeders"`
	SeedCriteria                                torznab.SeedCriteriaSettings `json:"seedCriteria"`
	RejectBlocklistedTorrentHashesWhileGrabbing bool                         `json:"rejectBlocklistedTorrentHashesWhileGrabbing"`
	MultiLanguages                              []int                        `json:"multiLanguages"`
	FailDownloads                               []int                        `json:"failDownloads"`

	// Parser knobs. Embedded for wire flatness — JSON tags carry through.
	Parser ParserSettings `json:"parser,omitempty"`
}

// NewSettings builds a Settings populated with the C# constructor defaults
// at TorrentRssIndexerSettings.cs:25-31 — BaseUrl="", AllowZeroSize=false,
// MinimumSeeders=1, MultiLanguages=[], FailDownloads=[].
func NewSettings() *Settings {
	return &Settings{
		BaseUrl:        "",
		AllowZeroSize:  false,
		MinimumSeeders: minimumSeedersDefault,
		SeedCriteria:   torznab.SeedCriteriaSettings{},
		MultiLanguages: []int{},
		FailDownloads:  []int{},
	}
}

// Validate ports the validator at TorrentRssIndexerSettings.cs:11-16.
//
// Rules:
//  1. BaseUrl must be a valid URL starting with http(s)://.
//  2. SeedCriteria warnings (delegated).
//
// Note: TorrentRss has no Categories/AnimeCategories rule (the feed is
// generic). No ApiKey rule either (the user attaches a Cookie header
// instead — different auth flow).
func (s *Settings) Validate() provider.ValidationResult {
	var failures []provider.ValidationFailure
	if !isValidRootURL(s.BaseUrl) {
		failures = append(failures, provider.NewValidationFailure(
			"BaseUrl",
			"must be valid URL that starts with http(s)://",
		))
	}
	failures = append(failures, validateSeedCriteria(&s.SeedCriteria)...)
	return provider.ValidationResult{Errors: failures}
}

// validateSeedCriteria duplicates the same checks torznab.validateSeedCriteria
// runs. Kept local to avoid an upward dep; the SeedCriteriaSettings type
// itself is reused from torznab.
//
// TODO follow-up: extract SeedCriteriaSettings + its validator into a
// shared `internal/indexers/seedcriteria` package once a third caller
// appears (download-clients in a separate Phase-4 loop).
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

// isValidRootURL ports the same shape as newznab.isValidRootURL — non-empty,
// scheme=http(s), host present.
func isValidRootURL(s string) bool {
	if s == "" {
		return false
	}
	u, err := parseURL(s)
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
