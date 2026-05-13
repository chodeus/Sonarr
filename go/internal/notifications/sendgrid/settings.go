// Package sendgrid ports the C# `NzbDrone.Core.Notifications.SendGrid`
// concrete notification provider. Slice 4.CG (C-cleanup [C3]
// sub-arc C3f).
//
// Surface: Settings + Validate + Payload + Proxy (POST JSON to
// {BaseUrl}/mail/send with Bearer ApiKey + multi-recipient
// personalization shape) + all 11 OnX hooks.
package sendgrid

import (
	"net/mail"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `SendGridSettings` at
// `Notifications/SendGrid/SendGridSettings.cs:20-45`.
type Settings struct {
	// BaseUrl is the SendGrid REST base URL. Defaults to
	// https://api.sendgrid.com/v3/. C# `:30`.
	BaseUrl string `json:"baseUrl"`
	// ApiKey is the SendGrid API key — REQUIRED. C# `:33`.
	ApiKey string `json:"apiKey"`
	// From is the sender email address — REQUIRED + must parse as
	// an email address. C# `:36`.
	From string `json:"from"`
	// Recipients is the per-event To address list — REQUIRED +
	// every entry must parse as an email. C# `:39`.
	Recipients []string `json:"recipients"`
}

// Validate ports the C# `SendGridSettingsValidator` at `:9-18`.
//
// Rules:
//   - ApiKey non-empty.
//   - From non-empty + parses as a valid email address.
//   - Recipients non-empty.
//   - Every Recipient parses as a valid email address.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.ApiKey) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ApiKey", ErrorMessage: "'ApiKey' must not be empty."})
	}
	from := strings.TrimSpace(s.From)
	switch {
	case from == "":
		out = append(out, notifications.TestFailure{PropertyName: "From", ErrorMessage: "'From' must not be empty."})
	case !isEmail(from):
		out = append(out, notifications.TestFailure{PropertyName: "From", ErrorMessage: "'From' is not a valid email address."})
	}
	if len(s.Recipients) == 0 {
		out = append(out, notifications.TestFailure{PropertyName: "Recipients", ErrorMessage: "'Recipients' must not be empty."})
	}
	for _, r := range s.Recipients {
		r = strings.TrimSpace(r)
		if r == "" || !isEmail(r) {
			out = append(out, notifications.TestFailure{PropertyName: "Recipients", ErrorMessage: "'Recipients' must be a valid email address."})
		}
	}
	return out
}

func isEmail(s string) bool {
	_, err := mail.ParseAddress(s)
	return err == nil
}
