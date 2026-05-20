// Package hdbits is the Go port of NzbDrone.Core.Indexers.HDBits.
//
// HDBits is a private-tracker JSON-REST API at hdbits.org. Distinct
// from FileList (J152-J153) in three ways:
//
//   - POST + JSON body instead of GET + query string. The auth
//     credentials (username + passkey) AND filter parameters
//     (category / codec / medium / limit) ride in the body.
//   - Wrapped response: `{"status": 0|N, "message": "...", "data": [...]}`.
//     A non-zero status means error; the array of TorrentQueryResponse
//     records lives inside `data`.
//   - Search routing uses TvdbId (not IMDB), with per-episode and
//     per-season fan-out for the criteria-typed dispatch.
//
// C# files mirrored:
//   - HDBits.cs                   (32 LOC; Indexer config)
//   - HDBitsSettings.cs           (124 LOC; Settings + Category /
//     Codec / Medium enums)
//   - HDBitsApi.cs                (130 LOC; request + response JSON
//     shapes + StatusCode enum)
//   - HDBitsParser.cs             (108 LOC; response → TorrentInfo)
//   - HDBitsRequestGenerator.cs   (178 LOC; per-criteria request
//     building via Clone-and-mutate pattern)
//
// # J154 scope (first slice, RSS-equivalent only)
//
// FetchRecent (no-filter latest call) + Test + JSON-response
// parsing + registry. FetchSearch returns empty; the per-criteria
// 7-overload routing from HDBitsRequestGenerator.cs:24-137 (Tvdb
// search + per-episode/season fan-out) lands as a follow-up slice.
// Same posture as FileList J152→J153 split.
package hdbits

import (
	"sonarr.tv/sonarr/internal/indexers/torznab"
	"sonarr.tv/sonarr/internal/provider"
)

// Identifiers used by the registry.
const (
	Implementation = "HDBits"
	ConfigContract = "HDBitsSettings"
	DisplayName    = "HDBits"
)

// defaultBaseURL mirrors C# `HDBitsSettings.cs:28`.
const defaultBaseURL = "https://hdbits.org"

// minimumSeedersDefault mirrors IndexerDefaults.MINIMUM_SEEDERS.
const minimumSeedersDefault = 1

// pageSize mirrors C# `HDBits.cs:15` (`PageSize => 100`) and the
// `query.Limit = 100` setting at `HDBitsRequestGenerator.cs:170`.
const pageSize = 100

// Category enumeration. Wire-stable — match C# `HdBitsCategory`.
const (
	CategoryMovie       = 1
	CategoryTV          = 2
	CategoryDocumentary = 3
	CategoryMusic       = 4
	CategorySport       = 5
	CategoryAudio       = 6
	CategoryXXX         = 7
	CategoryMiscDemo    = 8
)

// Codec enumeration. Wire-stable — match C# `HdBitsCodec`.
const (
	CodecH264  = 1
	CodecMPEG2 = 2
	CodecVC1   = 3
	CodecXviD  = 4
	CodecHEVC  = 5
)

// Medium enumeration. Wire-stable — match C# `HdBitsMedium`.
const (
	MediumBluRay  = 1
	MediumEncode  = 3
	MediumCapture = 4
	MediumRemux   = 5
	MediumWebDL   = 6
)

// defaultCategories mirrors C# `HDBitsSettings.cs:31` —
// `[TV=2, Documentary=3]`.
var defaultCategories = []int{CategoryTV, CategoryDocumentary}

// Settings ports `HDBitsSettings.cs:22-75`.
type Settings struct {
	BaseUrl                                     string                       `json:"baseUrl"`
	Username                                    string                       `json:"username"`
	ApiKey                                      string                       `json:"apiKey"`
	Categories                                  []int                        `json:"categories"`
	Codecs                                      []int                        `json:"codecs"`
	Mediums                                     []int                        `json:"mediums"`
	MinimumSeeders                              int                          `json:"minimumSeeders"`
	SeedCriteria                                torznab.SeedCriteriaSettings `json:"seedCriteria"`
	RejectBlocklistedTorrentHashesWhileGrabbing bool                         `json:"rejectBlocklistedTorrentHashesWhileGrabbing"`
	MultiLanguages                              []int                        `json:"multiLanguages"`
	FailDownloads                               []int                        `json:"failDownloads"`
}

// NewSettings builds Settings with the C# constructor defaults at
// `HDBitsSettings.cs:26-36`.
func NewSettings() *Settings {
	cats := make([]int, len(defaultCategories))
	copy(cats, defaultCategories)
	return &Settings{
		BaseUrl:        defaultBaseURL,
		MinimumSeeders: minimumSeedersDefault,
		Categories:     cats,
		Codecs:         []int{},
		Mediums:        []int{},
		MultiLanguages: []int{},
		FailDownloads:  []int{},
		SeedCriteria:   torznab.SeedCriteriaSettings{},
	}
}

// Validate ports `HDBitsSettingsValidator.cs:13-19`.
func (s *Settings) Validate() provider.ValidationResult {
	var failures []provider.ValidationFailure
	if !isValidRootURL(s.BaseUrl) {
		failures = append(failures, provider.NewValidationFailure(
			"BaseUrl",
			"must be valid URL that starts with http(s)://",
		))
	}
	if s.ApiKey == "" {
		failures = append(failures, provider.NewValidationFailure("ApiKey", "must not be empty"))
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

var _ provider.Config = (*Settings)(nil)
