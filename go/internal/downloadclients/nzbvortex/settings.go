// Package nzbvortex ports the C# NZBVortex download client at
// `src/NzbDrone.Core/Download/Clients/NzbVortex/`.
//
// NZBVortex is a macOS-native usenet client (commercial) that
// exposes a JSON-over-HTTPS API at `https://<host>:<port>/api`.
// Sonarr drives it via:
//
//   - `app/appversion` — version string (Test)
//   - `app/apilevel` — API-level number (version-gate at 2.3)
//   - `auth/nonce` + `auth/login` — SHA-256-nonce-based session
//     authentication; the sessionId is cached and replayed via
//     `?sessionid=<token>` on every subsequent call.
//   - `group` — list user-configured groups (TestCategory)
//   - `nzb` — queue listing (GetItems, TestAuthentication)
//   - `nzb/add` — add nzb file (Download)
//   - `nzb/{id}/cancel` / `cancelDelete` — Remove
//   - `file/{id}` — per-queue-item file listing
//
// Slice 5.J66 (this slice) ships the SKELETON:
//
//   - [Settings] + [NewSettings] + Validate.
//   - [Proxy] with the auth-nonce SHA-256 flow + session cache +
//     auth-retry; [Proxy.GetVersion] + [Proxy.GetApiVersion]
//     (the two no-auth probes Test uses).
//   - [NzbVortex] Provider with Name + Protocol + Definition +
//     Settings + Test (connection + API-version-gate at 2.3) +
//     GetStatus (IsLocalhost-only).
//   - Stubs for Download / GetItems / RemoveItem returning
//     [ErrNotImplemented].
//   - Registry registration via init().
//
// Follow-up slices fill in: GetItems (Proxy.GetQueue + state-table
// + GetFiles output-path resolution), Download (Proxy.DownloadNzb
// multipart upload), RemoveItem (Proxy.Remove cancel/cancelDelete),
// TestAuthentication (Proxy.GetQueue with apikey-failure detection),
// TestCategory (Proxy.GetGroups label gate).
package nzbvortex

import (
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Implementation + ConfigContract are the persisted-row identifiers.
// Mirrors C# class names `NzbVortex` + `NzbVortexSettings`.
const (
	Implementation = "NzbVortex"
	ConfigContract = "NzbVortexSettings"
)

// Priority constants port `NzbVortexPriority` at
// `NzbVortexPriority.cs:3-8`. Numeric values match the C# enum so
// Settings.RecentTvPriority / OlderTvPriority round-trip across
// the API edge.
const (
	PriorityLow    = -1
	PriorityNormal = 0
	PriorityHigh   = 1
)

// minAPIMajor + minAPIMinor are the API-level floor TestApiVersion
// enforces. Mirrors C# `Hadouken.cs:183` — `version.Major < 2 ||
// (version.Major == 2 && version.Minor < 3)`.
const (
	minAPIMajor = 2
	minAPIMinor = 3
)

// Settings ports `NzbVortexSettings` at
// `src/NzbDrone.Core/Download/Clients/NzbVortex/NzbVortexSettings.cs:25-66`.
//
// Defaults mirror the C# constructor at `:29-36`: Host="localhost",
// Port=4321, TvCategory="TV Shows", priorities=Normal.
//
// NZBVortex requires TLS — the proxy always builds an `https://`
// base URL (mirrors C# `BuildBaseUrl(useSsl: true, ...)` at
// `NzbVortexProxy.cs:118`). There is no UseSsl toggle on this
// Settings shape.
type Settings struct {
	downloadclients.BaseUsenetSettings

	// Host is the NZBVortex host. C# `:39`.
	Host string `json:"host"`

	// Port is the NZBVortex HTTPS port (default 4321). C# `:42`.
	Port int `json:"port"`

	// UrlBase is the optional URL prefix between host:port and
	// the `/api` suffix. C# `:47`.
	UrlBase string `json:"urlBase"`

	// ApiKey drives the SHA-256-nonce auth handshake at
	// [Proxy.authenticate]. C# `:50`.
	ApiKey string `json:"apiKey"`

	// TvCategory is the Group label applied on Download +
	// filtered against on GetItems. C# `:53`. Empty disables
	// group filtering (matches C# `IsNotNullOrWhiteSpace` guards).
	TvCategory string `json:"tvCategory"`

	// RecentTvPriority is the priority applied to grabs whose
	// matched episode aired within the last 14 days. C# `:56`.
	RecentTvPriority int `json:"recentTvPriority"`

	// OlderTvPriority is the priority applied to grabs whose
	// matched episode is older than 14 days. C# `:59`.
	OlderTvPriority int `json:"olderTvPriority"`
}

// NewSettings returns a Settings with the C# constructor defaults
// applied.
func NewSettings() Settings {
	return Settings{
		Host:             "localhost",
		Port:             4321,
		TvCategory:       "TV Shows",
		RecentTvPriority: PriorityNormal,
		OlderTvPriority:  PriorityNormal,
	}
}

// Validate ports `NzbVortexSettingsValidator` at C# `:8-22`. The C#
// `AsWarning()` arm on TvCategory is collapsed to "not enforced"
// in the Go port — Sonarr's UI treats both warning + error as
// settings rejection, and the Go-port [downloadclients.TestFailure]
// shape doesn't carry a severity flag yet.
//
// Rules:
//
//  1. Host non-empty.
//  2. Port 1..65535.
//  3. UrlBase must start with `/` when set.
//  4. ApiKey required (`NotEmpty` at C# `:16`).
func (s Settings) Validate() []downloadclients.TestFailure {
	var failures []downloadclients.TestFailure
	if strings.TrimSpace(s.Host) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Host",
			ErrorMessage: "Host is required",
		})
	}
	if s.Port < 1 || s.Port > 65535 {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Port",
			ErrorMessage: "Port must be between 1 and 65535",
		})
	}
	if strings.TrimSpace(s.UrlBase) != "" && !strings.HasPrefix(s.UrlBase, "/") {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "UrlBase",
			ErrorMessage: "URL base must start with '/'",
		})
	}
	if strings.TrimSpace(s.ApiKey) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "ApiKey",
			ErrorMessage: "API Key is required",
		})
	}
	return failures
}
