// Package custom ports the C# `NzbDrone.Core.ImportLists.Custom`
// concrete import-list provider — opens queue [C4] (import-lists
// long-tail). Slice 4.CO (sub-arc C4a of queue [C4]).
//
// The Custom import-list is the simplest of the [C4] long-tail
// providers: GET a user-configured URL, expect a JSON array of
// {Title, TvdbId, TmdbId, ImdbId} records, map to the
// [importlists.ItemInfo] shape. No auth + no pagination + no
// per-list filter rules — the URL is presumed to return the
// caller's already-filtered list.
//
// Slice scope: Settings + Validate + Proxy (GET + parse) + Provider
// (Fetch + Test + RequestAction-noop) + tests.
package custom

import (
	"strings"

	"sonarr.tv/sonarr/internal/importlists"
)

// Implementation is the value stored in the ImportLists.Implementation
// column for rows wired to this provider. Mirrors the C# class name
// `CustomImport` at `ImportLists/Custom/CustomImport.cs:13`.
const Implementation = "CustomImport"

// ConfigContract is the value stored in the ImportLists.ConfigContract
// column. Mirrors the C# class name `CustomSettings` at
// `ImportLists/Custom/CustomSettings.cs:16`.
const ConfigContract = "CustomSettings"

// Settings ports `CustomSettings` at `CustomSettings.cs:16-28`.
//
// Default values mirror the C# constructor: BaseURL="". The C# class
// inherits the (initially empty) field from `ImportListSettingsBase`.
type Settings struct {
	// BaseURL is the JSON-list endpoint the provider polls.
	// Required; validated as ValidRootUrl by the C# `CustomSettingsValidator`
	// at `CustomSettings.cs:11`. Trailing slashes are trimmed at
	// request-build time (mirrors C# `settings.BaseUrl.TrimEnd('/')`
	// at `CustomImportProxy.cs:74`).
	BaseURL string `json:"baseUrl"`
}

// DefaultSettings returns a Settings struct populated with the C#
// constructor defaults — BaseURL="".
func DefaultSettings() Settings {
	return Settings{BaseURL: ""}
}

// Validate runs the FluentValidation-equivalent rules from the C#
// `CustomSettingsValidator` at `CustomSettings.cs:8-14`.
//
// C# defines one rule: `RuleFor(c => c.BaseUrl).ValidRootUrl()`. The
// Go port checks BaseURL non-empty + parses as an http(s) URL with a
// non-empty host (matches the [internal/importlists/sonarr.isValidRootURL]
// precedent).
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
	return out
}
