// Package notifiarr ports the C# `NzbDrone.Core.Notifications.Notifiarr`
// concrete notification provider. Slice 4.CF (C-cleanup [C3]
// sub-arc C3e).
//
// Surface: Settings + Validate + Proxy (POST to notifiarr.com/api/v1/
// notification/sonarr with X-API-Key header, JSON-encoded
// [webhook.*Payload] body) + all 11 OnX hooks reusing the
// webhook.BuildXPayload helpers.
package notifiarr

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `NotifiarrSettings` at
// `Notifications/Notifiarr/NotifiarrSettings.cs:15-26`.
type Settings struct {
	// ApiKey is the Notifiarr per-user integration token — REQUIRED.
	// C# `:20`.
	ApiKey string `json:"apiKey"`
}

// Validate ports the C# `NotifiarrSettingsValidator` at `:7-13`.
//
// Rules:
//   - ApiKey non-empty.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.ApiKey) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ApiKey", ErrorMessage: "'ApiKey' must not be empty."})
	}
	return out
}
