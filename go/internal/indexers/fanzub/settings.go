// Package fanzub is the Go port of NzbDrone.Core.Indexers.Fanzub.
//
// Fanzub is a public Usenet RSS feed for anime content (typical host:
// `http://fanzub.com/rss/`). Distinct from the torrent-RSS indexers
// in the J146-J149 wave: Protocol=Usenet, parser produces
// [parser.ReleaseInfo] (NOT [parser.TorrentInfo]), Settings has no
// MinimumSeeders / SeedCriteria.
//
// C# files mirrored:
//   - Fanzub.cs               (30 LOC; Indexer config)
//   - FanzubRequestGenerator.cs (133 LOC; per-criteria search-URL building)
//   - FanzubSettings.cs       (48 LOC; BaseUrl + AnimeStandardFormatSearch +
//     standard MultiLanguages/FailDownloads)
//
// # J150 scope (RSS-only first slice)
//
// FetchRecent + Test + registry. FetchSearch returns empty; the
// per-criteria search-URL routing from
// FanzubRequestGenerator.cs:32-92 (Season + AnimeEpisode +
// AnimeSeason — 3 active overloads + 4 empty-default overloads)
// lands as a follow-up slice. Same posture as the Nyaa J148→J149
// split.
package fanzub

import (
	"sonarr.tv/sonarr/internal/provider"
)

// Identifiers used by the registry. Matches the C# class names
// verbatim.
const (
	Implementation = "Fanzub"
	ConfigContract = "FanzubSettings"
	DisplayName    = "Fanzub"
)

// defaultBaseURL mirrors the C# `FanzubSettings.cs:25` constructor
// default.
const defaultBaseURL = "http://fanzub.com/rss/"

// defaultPageSize mirrors the C# `FanzubRequestGenerator.cs:20`
// constructor default. The user can't override; pinned at the
// indexer level.
const defaultPageSize = 100

// Settings ports `FanzubSettings.cs:19-47`. Note the absence of
// MinimumSeeders / SeedCriteria / RejectBlocklisted — Fanzub is
// Usenet (no seeders / no torrent flags).
type Settings struct {
	BaseUrl                   string `json:"baseUrl"`
	AnimeStandardFormatSearch bool   `json:"animeStandardFormatSearch"`
	MultiLanguages            []int  `json:"multiLanguages"`
	FailDownloads             []int  `json:"failDownloads"`
}

// NewSettings builds a Settings with the C# constructor defaults at
// `FanzubSettings.cs:23-28`.
func NewSettings() *Settings {
	return &Settings{
		BaseUrl:        defaultBaseURL,
		MultiLanguages: []int{},
		FailDownloads:  []int{},
	}
}

// Validate ports `FanzubSettingsValidator.cs:13-17` — only one rule,
// BaseUrl must be a valid http(s):// root URL.
func (s *Settings) Validate() provider.ValidationResult {
	var failures []provider.ValidationFailure
	if !isValidRootURL(s.BaseUrl) {
		failures = append(failures, provider.NewValidationFailure(
			"BaseUrl",
			"must be valid URL that starts with http(s)://",
		))
	}
	return provider.ValidationResult{Errors: failures}
}

// Compile-time: Settings implements provider.Config.
var _ provider.Config = (*Settings)(nil)
