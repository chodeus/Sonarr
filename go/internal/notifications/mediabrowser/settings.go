// Package mediabrowser ports the C# `NzbDrone.Core.Notifications.Emby`
// MediaBrowser concrete notification provider — covers Emby +
// Jellyfin (same API surface). Slice 4.CJ (C-cleanup [C3] sub-arc C3i).
//
// Surface: Settings + Validate + Proxy (POST JSON notifications +
// library-update events with X-MediaBrowser-Token header) + all 11
// OnX hooks. Path mapping (MapFrom/MapTo) is supported.
//
// Deferred (matches C5 #11):
//   - MediaServerUpdateQueue coalescing buffer (ICacheManager) —
//     the C# code batches per-series library-refresh events; the Go
//     port currently calls Update immediately per event. ProcessQueue
//     is a no-op.
//   - GetPaths (Emby /Items lookup to find existing series paths by
//     ProviderId) — used as a fallback when the Sonarr path doesn't
//     match Emby's stored path. The Go port skips this lookup and
//     just sends the (mapped) series.Path. For typical MapFrom/MapTo
//     setups this gives the same outcome; for the edge cases the C#
//     fallback covers, users will need to set MapFrom/MapTo.
package mediabrowser

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `MediaBrowserSettings` at
// `Notifications/MediaBrowser/MediaBrowserSettings.cs:21-72`.
type Settings struct {
	// Host is the Emby/Jellyfin server host — REQUIRED. C# `:32`.
	Host string `json:"host"`
	// Port is the Emby/Jellyfin server port — defaults to 8096.
	// C# `:35`.
	Port int `json:"port"`
	// UseSsl toggles https vs http. C# `:39`.
	UseSsl bool `json:"useSsl"`
	// UrlBase is the optional path prefix for reverse-proxied
	// installs. C# `:44`.
	UrlBase string `json:"urlBase"`
	// ApiKey is the Emby/Jellyfin API key — REQUIRED. C# `:47`.
	ApiKey string `json:"apiKey"`
	// Notify toggles per-event admin notifications. C# `:50`.
	Notify bool `json:"notify"`
	// UpdateLibrary toggles per-event library-refresh on add/delete/
	// rename. C# `:53`.
	UpdateLibrary bool `json:"updateLibrary"`
	// MapFrom is the Sonarr-side path prefix to translate. C# `:57`.
	MapFrom string `json:"mapFrom"`
	// MapTo is the Emby/Jellyfin-side replacement prefix. C# `:60`.
	MapTo string `json:"mapTo"`
}

// Validate ports the C# `MediaBrowserSettingsValidator` at `:9-18`.
//
// Rules:
//   - Host non-empty.
//   - ApiKey non-empty.
//   - MapFrom/MapTo paired: when either is set the other must be too.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.Host) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Host", ErrorMessage: "'Host' must not be empty."})
	}
	if strings.TrimSpace(s.ApiKey) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ApiKey", ErrorMessage: "'ApiKey' must not be empty."})
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
// MapTo. Mirrors C# `MediaBrowserService.Update` mapping at `:47-52`.
// Returns path unchanged when either MapFrom or MapTo is blank.
func (s *Settings) applyMapping(path string) string {
	if s.MapFrom == "" || s.MapTo == "" {
		return path
	}
	if strings.HasPrefix(path, s.MapFrom) {
		return s.MapTo + strings.TrimPrefix(path, s.MapFrom)
	}
	return path
}
