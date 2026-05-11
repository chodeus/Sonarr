// Package sonarr ports the C# `NzbDrone.Core.ImportLists.Sonarr`
// concrete import-list provider — Sonarr peer / cross-instance
// import. Slice 4.AM (sub-arc I3a of queue [I3]).
//
// Scope at sub-arc I3a:
//   - [Settings] + Validate ports `SonarrSettings.cs` field-by-field
//     (BaseUrl + ApiKey + the five filter lists +
//     SyncSeasonMonitoring).
//   - [Proxy] ports the `SonarrV3Proxy` HTTP wrapper —
//     GetSeries / GetQualityProfiles / GetLanguageProfiles /
//     GetRootFolders / GetTags + the Test() entrypoint that maps
//     401/3xx/network errors to a [importlists.TestFailure].
//   - [SonarrImport] satisfies [importlists.Provider]. Fetch walks
//     the remote /api/v3/series, applies the four configured
//     filters (ProfileIds / LanguageProfileIds / TagIds /
//     RootFolderPaths), runs the result through [importlists.Dedup]
//     and stamps in the producing list's id+name (mirrors C#
//     `ImportListBase.CleanupListItems` at `ImportListBase.cs:85-96`).
//     The status recorder is invoked inside Fetch to match the C#
//     RecordSuccess/RecordFailure call-site at `SonarrImport.cs:83-91`.
//   - [Implementation] + [ConfigContract] string consts pin the
//     (Implementation, ConfigContract) pair the persisted row
//     uses to look up this provider.
//   - init() registers the constructor against the package-global
//     [importlists.Default] registry.
//
// Family pattern validated by this sub-arc:
//   - init() time registration into [importlists.Default].
//   - Settings deserialisation from the persisted JSON
//     (ImportLists.Settings column).
//   - HTTP proxy wrapper with a per-test httptest.Server-mockable
//     transport.
//   - Provider.Test surfaces FluentValidation-style failures.
//   - Provider.Fetch invokes RecordSuccess / RecordFailure on the
//     deps.StatusService (typed via a narrow interface), preserving
//     C# parity.
//
// Out-of-scope:
//   - LanguageProfile filter: the C# code still queries
//     `/api/v3/languageprofile` and filters on `LanguageProfileId`
//     even though modern Sonarr v3 has no language profiles. Port
//     ports the filter logic for parity but the endpoint returns
//     []SonarrProfile{} from modern peers — see the
//     [Proxy.GetLanguageProfiles] doc-comment.
//   - The C# `RequestAction("getTags"|"getProfiles"|...)` shape uses
//     `Filter("ApiKey").ThrowOnError()` which is the FluentValidation
//     fluent-API for "throw if ApiKey rule failed". The Go port
//     surfaces the same via a non-nil error return rather than a
//     panic.
package sonarr

import (
	"strings"

	"sonarr.tv/sonarr/internal/importlists"
)

// Implementation is the value stored in the ImportLists.Implementation
// column for rows wired to this provider. Mirrors the C# class
// name `SonarrImport` at `ImportLists/Sonarr/SonarrImport.cs:16`.
const Implementation = "SonarrImport"

// ConfigContract is the value stored in the ImportLists.ConfigContract
// column. Mirrors the C# `SonarrSettings` type-name at
// `ImportLists/Sonarr/SonarrSettings.cs:18`.
const ConfigContract = "SonarrSettings"

// Settings ports `SonarrSettings` at
// `src/NzbDrone.Core/ImportLists/Sonarr/SonarrSettings.cs:18-57`.
//
// Field order + JSON tag names match the persisted Settings JSON
// (the React UI form posts JSON keys lowerCamelCase matching the
// C# property names via Newtonsoft's default contract resolver).
//
// Default values mirror the C# constructor at `:22-29`: ApiKey="",
// every filter list = empty slice, SyncSeasonMonitoring=false.
type Settings struct {
	// BaseURL is the peer Sonarr root URL. Required; validated as
	// IsValidUrl by the C# `SonarrSettingsValidator` at `:13`.
	// Trailing slashes are trimmed at request-build time (mirrors
	// C# `settings.BaseUrl.TrimEnd('/')` at `SonarrV3Proxy.cs:100`).
	BaseURL string `json:"baseUrl"`

	// APIKey is the X-Api-Key header value sent to the peer. C# `:14`
	// rule: `RuleFor(c => c.ApiKey).NotEmpty()`.
	APIKey string `json:"apiKey"`

	// SyncSeasonMonitoring mirrors C# `:38`. When true, the per-item
	// Seasons hint is copied into the [importlists.ItemInfo] so the
	// orchestrator can honour the peer's per-season monitor state.
	SyncSeasonMonitoring bool `json:"syncSeasonMonitoring"`

	// ProfileIDs is the whitelist of quality-profile ids the import
	// should keep. Mirrors C# `:41`. Empty = no filter (keep all).
	ProfileIDs []int `json:"profileIds"`

	// TagIDs is the whitelist of tag ids the import should keep.
	// Mirrors C# `:44`. Empty = no filter. C# semantics at
	// `SonarrImport.cs:55-58`: KEEP when ANY of the user's TagIDs
	// is present in the remote series' Tags set.
	TagIDs []int `json:"tagIds"`

	// RootFolderPaths is the whitelist of root-folder substrings.
	// Mirrors C# `:47`. Empty = no filter. C# semantics at
	// `SonarrImport.cs:60-63`: KEEP when ANY user-configured root
	// path is contained (case-insensitive) in the remote series'
	// RootFolderPath.
	RootFolderPaths []string `json:"rootFolderPaths"`

	// LanguageProfileIDs is the whitelist of language-profile ids
	// the import should keep. Mirrors C# `:51`. Deprecated upstream
	// (modern Sonarr v3 has no language profiles); the C# code
	// still queries the `/api/v3/languageprofile` endpoint and
	// filters on `item.LanguageProfileId`. Port ports the filter
	// for parity but the endpoint returns [] from modern peers.
	LanguageProfileIDs []int `json:"languageProfileIds"`
}

// DefaultSettings returns a Settings struct populated with the C#
// constructor defaults at `SonarrSettings.cs:22-29` — APIKey="",
// all filter lists empty, SyncSeasonMonitoring=false.
func DefaultSettings() Settings {
	return Settings{
		APIKey:             "",
		ProfileIDs:         []int{},
		LanguageProfileIDs: []int{},
		TagIDs:             []int{},
		RootFolderPaths:    []string{},
	}
}

// Validate runs the FluentValidation-equivalent rules from the C#
// `SonarrSettingsValidator` at `SonarrSettings.cs:9-16`.
//
// C# defines two rules: `RuleFor(c => c.BaseUrl).ValidRootUrl()` +
// `RuleFor(c => c.ApiKey).NotEmpty()`. The Go port checks BaseUrl
// non-empty + parses as an http(s) URL, and ApiKey non-empty.
func (s *Settings) Validate() []importlists.TestFailure {
	var out []importlists.TestFailure
	if strings.TrimSpace(s.BaseURL) == "" {
		out = append(out, importlists.TestFailure{
			PropertyName: "BaseUrl",
			ErrorMessage: "Please enter a valid URL.",
		})
	} else if !isValidRootURL(s.BaseURL) {
		out = append(out, importlists.TestFailure{
			PropertyName: "BaseUrl",
			ErrorMessage: "Please enter a valid URL.",
		})
	}
	if strings.TrimSpace(s.APIKey) == "" {
		out = append(out, importlists.TestFailure{
			PropertyName: "ApiKey",
			ErrorMessage: "API Key is required.",
		})
	}
	return out
}
