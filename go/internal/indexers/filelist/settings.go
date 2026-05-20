// Package filelist is the Go port of NzbDrone.Core.Indexers.FileList.
//
// FileList is a private-tracker JSON API at filelist.io. Auth is
// HTTP Basic over the user's Username + Passkey. The API exposes
// two action endpoints:
//
//   - `/api.php?action=latest-torrents&category=<csv>` — the RSS-
//     equivalent feed used by FetchRecent.
//   - `/api.php?action=search-torrents&category=<csv>&type=<imdb|name>&query=<...>&season=<>&episode=<>`
//     — the per-criteria search endpoint used by FetchSearch.
//
// Response: JSON array of [FileListTorrent] records, each mapped
// into a [parser.TorrentInfo]. Download URLs are constructed from
// the torrent id + the user's passkey:
// `<BaseUrl>/download.php?id=<id>&passkey=<...>`.
//
// C# files mirrored:
//   - FileList.cs                  (31 LOC; Indexer config)
//   - FileListSettings.cs          (106 LOC; Settings + Categories enum)
//   - FileListRequestGenerator.cs  (155 LOC; per-criteria request building)
//   - FileListParser.cs            (101 LOC; JSON → TorrentInfo mapping)
//   - FileListTorrent.cs           (25 LOC; API response shape)
//
// # J152 scope (RSS-only first slice)
//
// FetchRecent + Test + JSON-response parsing + registry. FetchSearch
// returns empty; the per-criteria 6-overload routing from
// FileListRequestGenerator.cs:24-120 (IMDB-search + Name-search +
// per-tier dispatch + per-episode-list fan-out for anime) lands as
// a follow-up slice. First JSON-API indexer in the port — see the
// package doc-comment for the parser-reuse story (none: this builds
// its own JSON decoder since FileList's payload shape is bespoke).
package filelist

import (
	"sonarr.tv/sonarr/internal/indexers/torznab"
	"sonarr.tv/sonarr/internal/provider"
)

// Identifiers used by the registry. Matches the C# class names
// verbatim.
const (
	Implementation = "FileList"
	ConfigContract = "FileListSettings"
	DisplayName    = "FileList"
)

// defaultBaseURL mirrors the C# `FileListSettings.cs:38` constructor
// default.
const defaultBaseURL = "https://filelist.io"

// minimumSeedersDefault mirrors IndexerDefaults.MINIMUM_SEEDERS.
const minimumSeedersDefault = 1

// Category enumeration. Wire-stable ints — match C#
// `FileListCategories` at `:89-105`.
const (
	CategoryAnime     = 24
	CategoryAnimation = 15
	CategoryTV4K      = 27
	CategoryTVHD      = 21
	CategoryTVSD      = 23
	CategorySport     = 13
	CategoryRoDubbed  = 28
)

// defaultCategories mirrors the C# default category set at `:42-46`.
var defaultCategories = []int{CategoryTVSD, CategoryTVHD, CategoryTV4K}

// Settings ports `FileListSettings.cs:32-87`.
type Settings struct {
	Username                                    string                       `json:"username"`
	Passkey                                     string                       `json:"passkey"`
	MultiLanguages                              []int                        `json:"multiLanguages"`
	BaseUrl                                     string                       `json:"baseUrl"`
	Categories                                  []int                        `json:"categories"`
	AnimeCategories                             []int                        `json:"animeCategories"`
	MinimumSeeders                              int                          `json:"minimumSeeders"`
	SeedCriteria                                torznab.SeedCriteriaSettings `json:"seedCriteria"`
	RejectBlocklistedTorrentHashesWhileGrabbing bool                         `json:"rejectBlocklistedTorrentHashesWhileGrabbing"`
	FailDownloads                               []int                        `json:"failDownloads"`
}

// NewSettings builds a Settings with the C# constructor defaults at
// `FileListSettings.cs:36-51`.
func NewSettings() *Settings {
	cats := make([]int, len(defaultCategories))
	copy(cats, defaultCategories)
	return &Settings{
		BaseUrl:         defaultBaseURL,
		MinimumSeeders:  minimumSeedersDefault,
		Categories:      cats,
		AnimeCategories: []int{},
		MultiLanguages:  []int{},
		FailDownloads:   []int{},
		SeedCriteria:    torznab.SeedCriteriaSettings{},
	}
}

// Validate ports `FileListSettingsValidator.cs:14-29`.
//
//  1. BaseUrl must be a valid http(s):// root URL.
//  2. Username required.
//  3. Passkey required.
//  4. Categories OR AnimeCategories must be non-empty.
//  5. SeedCriteria warnings (delegated).
func (s *Settings) Validate() provider.ValidationResult {
	var failures []provider.ValidationFailure
	if !isValidRootURL(s.BaseUrl) {
		failures = append(failures, provider.NewValidationFailure(
			"BaseUrl",
			"must be valid URL that starts with http(s)://",
		))
	}
	if s.Username == "" {
		failures = append(failures, provider.NewValidationFailure("Username", "must not be empty"))
	}
	if s.Passkey == "" {
		failures = append(failures, provider.NewValidationFailure("Passkey", "must not be empty"))
	}
	if len(s.Categories) == 0 && len(s.AnimeCategories) == 0 {
		failures = append(failures, provider.NewValidationFailure(
			"",
			"Either 'Categories' or 'Anime Categories' must be provided",
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
