// Package mailgun ports the C# `NzbDrone.Core.Notifications.Mailgun`
// concrete notification provider. Slice 4.CF (C-cleanup [C3]
// sub-arc C3e).
//
// Surface: Settings + Validate + Proxy (form-POST email via the
// Mailgun /v3/{SenderDomain}/messages endpoint with HTTP Basic auth
// "api":ApiKey) + all 11 OnX hooks.
package mailgun

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `MailgunSettings` at
// `Notifications/Mailgun/MailgunSettings.cs:19-47`.
type Settings struct {
	// ApiKey is the Mailgun API key — REQUIRED. C# `:29`.
	ApiKey string `json:"apiKey"`
	// UseEuEndpoint routes to api.eu.mailgun.net/v3 instead of the
	// US endpoint. C# `:32`.
	UseEuEndpoint bool `json:"useEuEndpoint"`
	// From is the sender email address — REQUIRED. C# `:35`.
	From string `json:"from"`
	// SenderDomain is the Mailgun-registered sender domain forming
	// the per-account resource path. C# `:38`.
	SenderDomain string `json:"senderDomain"`
	// Recipients is the per-event To address list — REQUIRED.
	// C# `:41`.
	Recipients []string `json:"recipients"`
}

// Validate ports the C# `MailGunSettingsValidator` at `:9-17`.
//
// Rules:
//   - ApiKey non-empty.
//   - From non-empty.
//   - Recipients non-empty.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.ApiKey) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ApiKey", ErrorMessage: "'ApiKey' must not be empty."})
	}
	if strings.TrimSpace(s.From) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "From", ErrorMessage: "'From' must not be empty."})
	}
	if len(s.Recipients) == 0 {
		out = append(out, notifications.TestFailure{PropertyName: "Recipients", ErrorMessage: "'Recipients' must not be empty."})
	}
	return out
}
