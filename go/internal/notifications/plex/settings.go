// Package plex ports the C# `NzbDrone.Core.Notifications.Plex.Server`
// PlexServer concrete notification provider. Slice 4.CN (C-cleanup
// [C3] sub-arc C3m — CLOSES [C3]).
//
// Surface: Settings + Validate + Section/Location/Container types
// (both modern MediaContainer + legacy _children shapes) + Proxy
// (X-Plex-Token-authenticated GET to /library/sections + /identity +
// /library/sections/{id}/refresh) + the 6 OnX hooks PlexServer
// overrides (OnDownload/OnImportComplete/OnRename/OnEpisodeFileDelete/
// OnSeriesAdd/OnSeriesDelete). The other 5 are no-ops with SupportsOnX
// false. The C# PlexTv ping + the section-path discovery + root-folder
// lookup are deferred (see package doc).
//
// Deferred (matches C5 #11):
//   - PlexTv ping (the C# `_plexTvService.Ping(Settings.AuthToken)` in
//     UpdateIfEnabled + Test) — keeps the plex.tv session alive but
//     isn't required for /library/sections to work.
//   - MediaServerUpdateQueue coalescing buffer — ProcessQueue no-op;
//     per-event UpdateLibrary is called directly.
//   - PlexVersionException check for the buggy 1.3.0 Plex Media
//     Server version (released 2017) — too old to matter; the modern
//     /identity payload still parses cleanly.
//   - IRootFolderService.GetBestRootFolderPath — the Go port uses
//     filepath.Dir(series.Path) as the heuristic root-folder fallback.
//     For typical Sonarr setups this matches the configured root.
//   - PlexClientIdentifier from IConfigService — the Go port uses a
//     fixed string until config plumbing lands.
//   - RequestAction admin path (startOAuth + continueOAuth + pollOAuth
//   - getOAuthToken + servers) — no equivalent Provider slot yet.
package plex

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `PlexServerSettings` at
// `Notifications/Plex/Server/PlexServerSettings.cs:20-74`.
type Settings struct {
	// Host is the Plex Media Server host — REQUIRED. C# `:37`.
	Host string `json:"host"`
	// Port is the Plex Media Server port — defaults to 32400.
	// C# `:40`.
	Port int `json:"port"`
	// UseSsl toggles https vs http. C# `:44`.
	UseSsl bool `json:"useSsl"`
	// UrlBase is the optional path prefix for reverse-proxied installs.
	// C# `:49`.
	UrlBase string `json:"urlBase"`
	// AuthToken is the X-Plex-Token value (per-user auth). C# `:52`.
	AuthToken string `json:"authToken"`
	// UpdateLibrary toggles per-event library refresh. Defaults true.
	// C# `:58`.
	UpdateLibrary bool `json:"updateLibrary"`
	// MapFrom is the Sonarr-side path prefix to translate. C# `:62`.
	MapFrom string `json:"mapFrom"`
	// MapTo is the Plex-side replacement prefix. C# `:66`.
	MapTo string `json:"mapTo"`
	// SignIn is the placeholder for the OAuth field-type UI element.
	// Held only for round-trip persistence. C# `:55`.
	SignIn string `json:"signIn"`
}

// Validate ports the C# `PlexServerSettingsValidator` at `:9-17`.
//
// Rules:
//   - Host non-empty.
//   - Port in [1, 65535].
//   - MapFrom/MapTo paired: when either is set the other must be too.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.Host) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Host", ErrorMessage: "'Host' must not be empty."})
	}
	if s.Port < 1 || s.Port > 65535 {
		out = append(out, notifications.TestFailure{PropertyName: "Port", ErrorMessage: "'Port' must be between 1 and 65535."})
	}
	mapFromBlank := strings.TrimSpace(s.MapFrom) == ""
	mapToBlank := strings.TrimSpace(s.MapTo) == ""
	if !mapFromBlank && mapToBlank {
		out = append(out, notifications.TestFailure{PropertyName: "MapTo", ErrorMessage: "'MapTo' must not be empty when MapFrom is set."})
	}
	if mapFromBlank && !mapToBlank {
		out = append(out, notifications.TestFailure{PropertyName: "MapFrom", ErrorMessage: "'MapFrom' must not be empty when MapTo is set."})
	}
	return out
}

// applyMapping returns the path with the MapFrom prefix replaced by
// MapTo. Mirrors C# `PlexServerService.UpdateSections` path mapping
// at `:111-115`.
func (s *Settings) applyMapping(path string) string {
	if s.MapFrom == "" || s.MapTo == "" {
		return path
	}
	if strings.HasPrefix(path, s.MapFrom) {
		return s.MapTo + strings.TrimPrefix(path, s.MapFrom)
	}
	return path
}
