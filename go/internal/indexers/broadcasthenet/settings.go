// Package broadcasthenet is the Go port of NzbDrone.Core.Indexers.BroadcastheNet.
//
// BroadcastheNet (`api.broadcasthe.net`) speaks JSON-RPC 2.0 over POST.
// All requests share the shape
// `{"jsonrpc": "2.0", "method": "getTorrents", "params": [apiKey, query, pageSize, offset]}`
// and the response is wrapped in JSON-RPC's
// `{"result": {results, torrents: {id: {...}}}, "error": null|<obj>}`.
// Distinct from the other JSON-API indexers (FileList/HDBits) in three ways:
//
//   - JSON-RPC envelope rather than bare arrays / wrapped status codes.
//     Errors land in `error`, not as a status int.
//   - Per-request pagination (offset increments by PageSize across
//     MaxPages=10 calls). The C# tree's `JsonRpcRequestBuilder`
//     mutates `params[3]` to advance the offset.
//   - Response is keyed by torrent-id (Dictionary<int,Torrent>) rather
//     than an array.
//
// C# files mirrored:
//   - BroadcastheNet.cs                 (44 LOC; Indexer config)
//   - BroadcastheNetSettings.cs         (60 LOC; Settings)
//   - BroadcastheNetTorrent.cs          (34 LOC; per-torrent JSON shape)
//   - BroadcastheNetTorrentQuery.cs     (40 LOC; query body shape)
//   - BroadcastheNetTorrents.cs         (10 LOC; response data shape)
//   - BroadcastheNetParser.cs           (137 LOC; status-code + JSON parsing)
//   - BroadcastheNetRequestGenerator.cs (259 LOC; per-criteria routing)
//
// # J156 scope (first slice)
//
// FetchRecent + Test + JSON-RPC response parsing + registry. Recent-
// fetch uses only the `Age=<=86400` (24-hour window) tier — the C#
// `LastRecentTorrentId` optimization that walks `IIndexerStatusService.
// GetLastRssSyncReleaseInfo` to issue a more efficient `Id>=<last-100>`
// query is deferred. Search routing (the 7 criteria-typed dispatch)
// also deferred to a follow-up slice. Same J154→J155 split posture.
package broadcasthenet

import (
	"sonarr.tv/sonarr/internal/indexers/torznab"
	"sonarr.tv/sonarr/internal/provider"
)

// Identifiers used by the registry. The C# class file is named
// `BroadcastheNet.cs` but the C# `Name` property override is
// "BroadcasTheNet" (capital T in "The"). The wire-stable
// Implementation is the class-name form; DisplayName the Name-property
// form.
const (
	Implementation = "BroadcastheNet"
	ConfigContract = "BroadcastheNetSettings"
	DisplayName    = "BroadcasTheNet"
)

// defaultBaseURL mirrors C# `BroadcastheNetSettings.cs:28`.
const defaultBaseURL = "https://api.broadcasthe.net/"

// minimumSeedersDefault mirrors IndexerDefaults.MINIMUM_SEEDERS.
const minimumSeedersDefault = 1

// pageSize mirrors C# `BroadcastheNet.cs:17` (`PageSize => 100`) and
// the request generator's `PageSize = 100` default at `:19`.
const pageSize = 100

// maxPages mirrors C# `BroadcastheNetRequestGenerator.cs:18`
// (`MaxPages = 10`). The C# request generator paginates up to this
// number of pages per criteria tier.
const maxPages = 10

// Settings ports `BroadcastheNetSettings.cs:22-59`.
type Settings struct {
	BaseUrl                                     string                       `json:"baseUrl"`
	ApiKey                                      string                       `json:"apiKey"`
	MinimumSeeders                              int                          `json:"minimumSeeders"`
	SeedCriteria                                torznab.SeedCriteriaSettings `json:"seedCriteria"`
	RejectBlocklistedTorrentHashesWhileGrabbing bool                         `json:"rejectBlocklistedTorrentHashesWhileGrabbing"`
	MultiLanguages                              []int                        `json:"multiLanguages"`
	FailDownloads                               []int                        `json:"failDownloads"`
}

// NewSettings builds Settings with the C# constructor defaults at
// `BroadcastheNetSettings.cs:26-32`.
func NewSettings() *Settings {
	return &Settings{
		BaseUrl:        defaultBaseURL,
		MinimumSeeders: minimumSeedersDefault,
		MultiLanguages: []int{},
		FailDownloads:  []int{},
		SeedCriteria:   torznab.SeedCriteriaSettings{},
	}
}

// Validate ports `BroadcastheNetSettingsValidator.cs:13-19`. C# also
// adds custom (1.0, 24*60, 5*24*60) defaults to the SeedCriteria
// validator — those are advisory recommendations on the C# side
// rather than hard validation rules; the Go port keeps the standard
// zero-or-greater warnings.
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
