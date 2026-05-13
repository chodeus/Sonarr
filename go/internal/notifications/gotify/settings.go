// Package gotify ports the C# `NzbDrone.Core.Notifications.Gotify`
// concrete notification provider. Slice 4.CC (C-cleanup [C3]
// sub-arc C3b).
//
// Surface: Settings + Validate + Priority enum + Payload (Title /
// Message / Priority / Extras with client::display + client::notification)
// + Proxy (POST to {Server}/message?token={AppToken}) + all 11 OnX
// hooks.
//
// Deferred (matches C5 #11): IncludeSeriesPoster requires the
// series.Images blob to be decoded into per-cover-type entries.
// The series row currently doesn't carry the typed Images list,
// so the per-series poster URL extraction is wired into Test
// (which uses a hard-coded Sonarr logo URL) but skipped in the
// per-event SendNotification — the Settings.IncludeSeriesPoster
// toggle is preserved on the struct for forward-compat.
package gotify

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `GotifySettings` at
// `Notifications/Gotify/GotifySettings.cs:44-77`.
type Settings struct {
	// Server is the base URL of the Gotify server — REQUIRED.
	// C# `:56`.
	Server string `json:"server"`
	// AppToken is the per-app token Gotify minted for Sonarr —
	// REQUIRED. C# `:59`.
	AppToken string `json:"appToken"`
	// Priority is the int-encoded GotifyPriority value. Default 5
	// (Normal). C# `:62`.
	Priority int `json:"priority"`
	// IncludeSeriesPoster toggles the per-event poster image.
	// Currently a forward-compat field — see package doc.
	// C# `:65`.
	IncludeSeriesPoster bool `json:"includeSeriesPoster"`
	// MetadataLinks is the int-list of [notifications.MetadataLinkType]
	// values to surface in the message body. C# `:68`.
	MetadataLinks []int `json:"metadataLinks"`
	// PreferredMetadataLink determines which link receives the
	// payload-level click-through URL. C# `:71`.
	PreferredMetadataLink int `json:"preferredMetadataLink"`
}

// Validate ports the C# `GotifySettingsValidator` at `:11-41`.
//
// Rules:
//   - Server must be a non-empty URL.
//   - AppToken non-empty.
//   - MetadataLinks contain only known enum values.
//   - PreferredMetadataLink, when MetadataLinks is non-empty, must
//     be one of the selected values.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	server := strings.TrimSpace(s.Server)
	if server == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Server", ErrorMessage: "Server must be a valid URL"})
	} else if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		out = append(out, notifications.TestFailure{PropertyName: "Server", ErrorMessage: "Server must be a valid URL"})
	}
	if strings.TrimSpace(s.AppToken) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "AppToken", ErrorMessage: "'AppToken' must not be empty."})
	}
	for _, l := range s.MetadataLinks {
		switch notifications.MetadataLinkType(l) {
		case notifications.MetadataLinkImdb, notifications.MetadataLinkTvdb, notifications.MetadataLinkTvmaze, notifications.MetadataLinkTrakt:
		default:
			out = append(out, notifications.TestFailure{PropertyName: "MetadataLinks", ErrorMessage: "MetadataLink is not valid"})
		}
	}
	if len(s.MetadataLinks) > 0 {
		found := false
		for _, l := range s.MetadataLinks {
			if l == s.PreferredMetadataLink {
				found = true
				break
			}
		}
		if !found {
			out = append(out, notifications.TestFailure{PropertyName: "PreferredMetadataLink", ErrorMessage: "Must be a selected link"})
		}
	}
	return out
}

// Priority mirrors the C# `GotifyPriority` enum at
// `GotifyPriority.cs:3-9`. Stored as int on Settings; values 0/2/5/8.
type Priority int

// Priority values mirror the C# enum names.
const (
	PriorityMin    Priority = 0
	PriorityLow    Priority = 2
	PriorityNormal Priority = 5
	PriorityHigh   Priority = 8
)
