// Package pushcut ports the C# `NzbDrone.Core.Notifications.Pushcut`
// concrete notification provider. Slice 4.CE (C-cleanup [C3]
// sub-arc C3d).
//
// Surface: Settings + Validate + Payload + Proxy (POST to
// api.pushcut.io/v1/notifications/{NotificationName} with API-Key
// header) + all 11 OnX hooks. Per-event metadata-link Actions []
// emit per-link click-through buttons on the notification.
//
// Deferred (matches C5 #11): IncludePoster requires series.Images
// blob; not yet decoded. The toggle is preserved on the struct;
// per-event hooks set Image to "" until the typed cover-type list
// ports.
package pushcut

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `PushcutSettings` at
// `Notifications/Pushcut/PushcutSettings.cs:17-40`.
type Settings struct {
	// NotificationName is the named Pushcut notification template
	// to fire — REQUIRED. C# `:22`.
	NotificationName string `json:"notificationName"`
	// ApiKey is the Pushcut API-Key header value — REQUIRED.
	// C# `:25`.
	ApiKey string `json:"apiKey"`
	// TimeSensitive raises the iOS time-sensitive flag. C# `:28`.
	TimeSensitive bool `json:"timeSensitive"`
	// IncludePoster toggles per-event series-poster surfacing.
	// DEFERRED — see package doc. C# `:31`.
	IncludePoster bool `json:"includePoster"`
	// MetadataLinks is the int-list of [notifications.MetadataLinkType]
	// values surfaced as per-notification action buttons. C# `:34`.
	MetadataLinks []int `json:"metadataLinks"`
}

// Validate ports the C# `PushcutSettingsValidator` at `:8-15`.
//
// Rules:
//   - ApiKey non-empty.
//   - NotificationName non-empty.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.ApiKey) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ApiKey", ErrorMessage: "'ApiKey' must not be empty."})
	}
	if strings.TrimSpace(s.NotificationName) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "NotificationName", ErrorMessage: "'NotificationName' must not be empty."})
	}
	return out
}
