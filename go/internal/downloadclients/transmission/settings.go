// Package transmission ports the C# Transmission download client.
// Slice 4.Y (sub-arc 14b of queue [14]). Closes queue [14].
//
// Scope at sub-arc 14b:
//   - [Settings] + Validate ports `TransmissionSettings.cs` field-by-
//     field.
//   - [Proxy] ports the Transmission RPC wrapper for the Download +
//     Test paths (AddTorrentFromURL / AddTorrentFromData /
//     GetClientVersion / SetTorrentSeedingConfiguration). GetTorrents
//     / RemoveTorrent / SetTorrentLabels are deferred (queue [15]
//     consumer demand).
//   - [Transmission] Provider implements [downloadclients.Provider].
//     Name / Protocol / Definition / Download / Test live; GetItems /
//     GetStatus / RemoveItem / GetImportItem / MarkItemAsImported
//     return [downloadclients.ErrNotImplementedYet] until queue [15]
//     drives the missing methods.
//
// Family pattern (validated by sub-arc 14a + this slice):
//   - init() time registration in [downloadclients.Default].
//   - Settings JSON round-trip from DownloadClient.Settings column.
//   - HTTP proxy with session-id auth + 409-Conflict re-auth flow
//     (Transmission-specific).
//   - Download routes magnet → torrent-add(filename=magnet) and
//     bytes → torrent-add(metainfo=base64).
//
// C# cites lead each type with file:line references.
package transmission

import (
	"errors"
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Settings ports `TransmissionSettings` at
// `src/NzbDrone.Core/Download/Clients/Transmission/TransmissionSettings.cs:27-96`.
//
// Defaults mirror the C# constructor at `:32-38`:
// Host=localhost, Port=9091, UrlBase="/transmission/", TvCategory=
// "tv-sonarr".
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the Transmission RPC host. C# `:53`.
	Host string `json:"host"`
	// Port is the Transmission RPC port (default 9091). C# `:56`.
	Port int `json:"port"`
	// UseSsl flips http→https. C# `:60`.
	UseSsl bool `json:"useSsl"`
	// UrlBase is the optional URL prefix (default "/transmission/").
	// C# `:66`.
	UrlBase string `json:"urlBase"`
	// Username is the HTTP-basic auth username. C# `:69`.
	Username string `json:"username"`
	// Password is the HTTP-basic auth password. C# `:72`.
	Password string `json:"password"`
	// TvCategory is applied as a Transmission "label" on every
	// added torrent. Constrained to [a-z-] characters. C# `:75`.
	TvCategory string `json:"tvCategory"`
	// TvImportedCategory is the post-import label Sonarr applies
	// when MarkItemAsImported fires. C# `:78`.
	TvImportedCategory string `json:"tvImportedCategory"`
	// TvDirectory is the per-Sonarr download directory override
	// (mutually exclusive with TvCategory). C# `:81`.
	TvDirectory string `json:"tvDirectory"`
	// RecentTvPriority / OlderTvPriority pick between the
	// Transmission queue tiers based on Subject.IsRecent. C#
	// `:84,87`.
	RecentTvPriority int `json:"recentTvPriority"`
	OlderTvPriority  int `json:"olderTvPriority"`
	// AddPaused starts torrents in the paused state. C# `:90`.
	AddPaused bool `json:"addPaused"`
}

// NewSettings returns a Settings with the C# constructor defaults at
// `TransmissionSettings.cs:32-38`. JSON-unmarshalling onto this
// preserves the defaults for any missing fields.
func NewSettings() Settings {
	return Settings{
		Host:       "localhost",
		Port:       9091,
		UrlBase:    "/transmission/",
		TvCategory: "tv-sonarr",
	}
}

// categoryPattern mirrors C# `:19` — TvCategory must match
// `^\.?[-a-z]*$` case-insensitively. The dot-prefix variant is the
// hidden-folder convention some users adopt.
var categoryPattern = regexp.MustCompile(`(?i)^\.?[-a-z]*$`)

// Validate ports `TransmissionSettingsValidator` at C# `:10-25`.
// Returns the FluentValidation-style failure list.
//
// Rules:
//
//  1. Host non-empty (the C# ValidHost extension also checks
//     DNS-label format; the Go port checks non-empty + no
//     whitespace).
//  2. Port 1-65535.
//  3. UrlBase must start with "/" when set. The C# ValidUrlBase
//     extension is empty-tolerant; matches the default-init shape.
//  4. TvCategory must match the [a-z-] regex (case-insensitive,
//     optional leading dot).
//  5. TvCategory + TvDirectory are mutually exclusive (C# `:21-23`).
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

	if strings.TrimSpace(s.TvCategory) != "" && !categoryPattern.MatchString(s.TvCategory) {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: "Allowed characters a-z and -",
		})
	}

	if strings.TrimSpace(s.TvCategory) != "" && strings.TrimSpace(s.TvDirectory) != "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: "Cannot use Category and Directory",
		})
	}

	return failures
}

// ErrInvalidSettings is returned at Provider construction when
// Settings fail Validate. Callers wrap the field-level failures into
// the v3 API response.
var ErrInvalidSettings = errors.New("transmission: invalid settings")

// Priority constants port `TransmissionPriority.cs`. The Transmission
// RPC accepts these as the `bandwidthPriority` argument on
// torrent-add. Numeric order mirrors the C# enum.
const (
	PriorityLow    = -1
	PriorityNormal = 0
	PriorityHigh   = 1
)
