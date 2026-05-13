// Package xbmc ports the C# `NzbDrone.Core.Notifications.Xbmc`
// (Kodi) concrete notification provider. Slice 4.CK (C-cleanup [C3]
// sub-arc C3j).
//
// Surface: Settings + Validate + Proxy (JSON-RPC over HTTP to
// {scheme}://{Host}:{Port}{UrlBase}) + all 11 OnX hooks.
//
// Deferred (matches C5 #11):
//   - MediaServerUpdateQueue coalescing buffer (ICacheManager) — the
//     C# code batches per-series library updates; the Go port calls
//     Update/Clean immediately per event. ProcessQueue is a no-op.
//   - GetSeriesPath via VideoLibrary.GetTvShows — discovery fallback
//     for the exact Kodi-side path. The Go port calls
//     VideoLibrary.Scan with nil path (full library scan) when the
//     update event fires.
package xbmc

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `XbmcSettings` at
// `Notifications/Xbmc/XbmcSettings.cs:20-75`.
type Settings struct {
	// Host is the Kodi server host — REQUIRED. C# `:32`.
	Host string `json:"host"`
	// Port is the Kodi HTTP port — defaults to 8080. C# `:35`.
	Port int `json:"port"`
	// UseSsl toggles https vs http. C# `:39`.
	UseSsl bool `json:"useSsl"`
	// UrlBase is the JSON-RPC path — defaults to /jsonrpc. C# `:44`.
	UrlBase string `json:"urlBase"`
	// Username is the optional HTTP Basic auth username. C# `:47`.
	Username string `json:"username"`
	// Password is the optional HTTP Basic auth password. C# `:50`.
	Password string `json:"password"`
	// DisplayTime is the notification on-screen duration in seconds —
	// defaults to 5, minimum 2. C# `:54`.
	DisplayTime int `json:"displayTime"`
	// Notify toggles GUI notifications. C# `:57`.
	Notify bool `json:"notify"`
	// UpdateLibrary toggles per-event library scan. C# `:60`.
	UpdateLibrary bool `json:"updateLibrary"`
	// CleanLibrary toggles per-event library clean. C# `:63`.
	CleanLibrary bool `json:"cleanLibrary"`
	// AlwaysUpdate forces library updates even while video is
	// playing. C# `:66`.
	AlwaysUpdate bool `json:"alwaysUpdate"`
}

// Validate ports the C# `XbmcSettingsValidator` at `:10-18`.
//
// Rules:
//   - Host non-empty.
//   - DisplayTime >= 2.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.Host) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Host", ErrorMessage: "'Host' must not be empty."})
	}
	if s.DisplayTime < 2 {
		out = append(out, notifications.TestFailure{PropertyName: "DisplayTime", ErrorMessage: "'DisplayTime' must be at least 2."})
	}
	return out
}
