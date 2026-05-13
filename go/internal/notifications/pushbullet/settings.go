// Package pushbullet ports the C# `NzbDrone.Core.Notifications.PushBullet`
// concrete notification provider. Slice 4.CH (C-cleanup [C3]
// sub-arc C3g).
//
// Surface: Settings + Validate + Proxy (form-POST to
// api.pushbullet.com/v2/pushes per-channel OR per-device OR one
// global push when neither set; Basic auth user=ApiKey + empty pass)
// + all 11 OnX hooks.
//
// Deferred: the C# `RequestAction("getDevices", ...)` admin path
// surfaces a per-row dynamic device-list — the Go-port Provider
// interface has no equivalent slot yet; the GetDevices proxy helper
// is exported for forward compat when the UI-action dispatch lands.
package pushbullet

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `PushBulletSettings` at
// `Notifications/Pushbullet/PushBulletSettings.cs:17-43`.
type Settings struct {
	// ApiKey is the Pushbullet access token — REQUIRED. C# `:28`.
	ApiKey string `json:"apiKey"`
	// DeviceIds is the list of device-iden strings to target.
	// Mutually exclusive with ChannelTags. C# `:31`.
	DeviceIds []string `json:"deviceIds"`
	// ChannelTags is the list of channel-tag strings to target.
	// When non-empty supersedes DeviceIds. C# `:34`.
	ChannelTags []string `json:"channelTags"`
	// SenderId is the optional source_device_iden the push appears
	// to come from. C# `:37`.
	SenderId string `json:"senderId"`
}

// Validate ports the C# `PushBulletSettingsValidator` at `:9-15`.
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
