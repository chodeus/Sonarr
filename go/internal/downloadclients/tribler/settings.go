// Package tribler ports the C# Tribler download client at
// `src/NzbDrone.Core/Download/Clients/Tribler/`.
//
// Tribler is a privacy-focused decentralized BitTorrent client with
// an anonymization layer; Sonarr drives it via a JSON-over-HTTP API
// at `<scheme>://<host>:<port>/<urlBase>/api` using a static
// `X-Api-Key` header for auth. Endpoints:
//
//   - GET `/api/settings` — global config (Test arm + GetStatus).
//   - GET `/api/downloads` — list (GetItems).
//   - GET `/api/downloads/<hash>/files` — per-download files (used
//     by GetImportItem to resolve output paths).
//   - PUT `/api/downloads` — add task from magnet URL.
//   - DELETE `/api/downloads/<hash>` body `{remove_data:bool}` — remove.
//
// Slice 5.J81 (this slice + the LAST long-tail DC) ships the
// SKELETON:
//
//   - [Settings] + [NewSettings] + Validate.
//   - [Proxy] with [Proxy.GetConfig] for the Test arm; the X-Api-Key
//     header is applied on every authenticated call.
//   - [TriblerDownloadClient] Provider with Name + Protocol +
//     Definition + Settings + Test (GetConfig probe) + GetStatus
//     (IsLocalhost-only) + identity methods. Download / GetItems
//     / RemoveItem stub out to [ErrNotImplemented].
//   - Registry registration via init().
//
// Follow-up slices fill in the per-method ports (Download via PUT
// /api/downloads with the magnet URL payload, GetItems via GET
// /api/downloads with the per-state status mapping, RemoveItem via
// DELETE with the remove_data flag).
package tribler

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Implementation + ConfigContract are the persisted-row identifiers.
// Mirrors C# class names `TriblerDownloadClient` +
// `TriblerDownloadSettings`.
const (
	Implementation = "TriblerDownloadClient"
	ConfigContract = "TriblerDownloadSettings"
)

// categoryPattern mirrors the C# `^\.?[-a-zA-Z]*$/i` at
// `TriblerDownloadSettings.cs:18`.
var categoryPattern = regexp.MustCompile(`^\.?[-a-zA-Z]*$`)

// Settings ports `TriblerDownloadSettings` at
// `src/NzbDrone.Core/Download/Clients/Tribler/TriblerDownloadSettings.cs:26-72`.
//
// Defaults mirror the C# constructor at `:30-37`: Host="localhost",
// Port=20100, AnonymityLevel=1, SafeSeeding=true.
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the Tribler host. C# `:40`.
	Host string `json:"host"`

	// Port is the Tribler API port (default 20100). C# `:43`.
	Port int `json:"port"`

	// UseSsl flips http→https. C# `:46`.
	UseSsl bool `json:"useSsl"`

	// UrlBase is the optional URL prefix between host:port and the
	// `/api/...` paths. Empty (default) means no prefix. C# `:51`.
	UrlBase string `json:"urlBase"`

	// ApiKey is the static X-Api-Key header value. Required.
	// C# `:54`.
	ApiKey string `json:"apiKey"`

	// TvCategory is the optional sub-folder name appended to the
	// Tribler default-destination folder. Mutually exclusive with
	// TvDirectory. C# `:57`. Must match [categoryPattern].
	TvCategory string `json:"tvCategory"`

	// TvDirectory is the optional Tribler-side absolute path where
	// new downloads land. Mutually exclusive with TvCategory.
	// C# `:60`.
	TvDirectory string `json:"tvDirectory"`

	// AnonymityLevel selects the number of hops the Tor-like
	// anonymization layer uses. 0 = direct, 1 = single relay (default),
	// 2+ = multi-hop. C# `:64`.
	AnonymityLevel int `json:"anonymityLevel"`

	// SafeSeeding toggles the safe-seeding mode (Tribler keeps the
	// anonymization layer on while seeding). C# `:67`.
	SafeSeeding bool `json:"safeSeeding"`
}

// NewSettings returns a Settings with the C# constructor defaults.
func NewSettings() Settings {
	return Settings{
		Host:           "localhost",
		Port:           20100,
		AnonymityLevel: 1,
		SafeSeeding:    true,
	}
}

// Validate ports `TriblerSettingsValidator` at C# `:10-23`. Rules:
//
//  1. Host non-empty.
//  2. Port 1..65535.
//  3. UrlBase must start with `/` when non-empty.
//  4. ApiKey required.
//  5. TvCategory must match [categoryPattern] when set.
//  6. TvCategory + TvDirectory mutually exclusive.
//  7. AnonymityLevel >= 0.
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
	if s.TvCategory != "" && !categoryPattern.MatchString(s.TvCategory) {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: "Allowed characters a-z and -",
		})
	}
	if strings.TrimSpace(s.TvDirectory) != "" && strings.TrimSpace(s.TvCategory) != "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: "Cannot use Category and Directory",
		})
	}
	if s.AnonymityLevel < 0 {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "AnonymityLevel",
			ErrorMessage: "Anonymity level must be >= 0",
		})
	}
	return failures
}
