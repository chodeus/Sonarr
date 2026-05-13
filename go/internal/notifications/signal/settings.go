// Package signal ports the C# `NzbDrone.Core.Notifications.Signal`
// concrete notification provider. Slice 4.CH (C-cleanup [C3]
// sub-arc C3g).
//
// Surface: Settings + Validate + Payload + Proxy (POST JSON to
// {Host}:{Port}/v2/send with optional Basic auth) + all 11 OnX hooks.
//
// Targets a self-hosted signal-cli-rest-api instance — there's no
// public Signal API host. Host/Port + UseSsl compose into the base
// URL.
package signal

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `SignalSettings` at
// `Notifications/Signal/SignalSettings.cs:18-48`.
type Settings struct {
	// Host is the signal-cli-rest-api host — REQUIRED. C# `:23`.
	Host string `json:"host"`
	// Port is the API port — REQUIRED. C# `:26`.
	Port int `json:"port"`
	// UseSsl toggles https vs http. C# `:30`.
	UseSsl bool `json:"useSsl"`
	// SenderNumber is the Signal-registered phone number sending
	// the message — REQUIRED. C# `:33`.
	SenderNumber string `json:"senderNumber"`
	// ReceiverId is the recipient phone number OR group ID —
	// REQUIRED. C# `:36`.
	ReceiverId string `json:"receiverId"`
	// AuthUsername is the optional HTTP Basic auth username.
	// C# `:39`.
	AuthUsername string `json:"authUsername"`
	// AuthPassword is the optional HTTP Basic auth password.
	// C# `:42`.
	AuthPassword string `json:"authPassword"`
}

// Validate ports the C# `SignalSettingsValidator` at `:7-16`.
//
// Rules:
//   - Host non-empty.
//   - Port > 0.
//   - SenderNumber non-empty.
//   - ReceiverId non-empty.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.Host) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Host", ErrorMessage: "'Host' must not be empty."})
	}
	if s.Port <= 0 {
		out = append(out, notifications.TestFailure{PropertyName: "Port", ErrorMessage: "'Port' must not be empty."})
	}
	if strings.TrimSpace(s.SenderNumber) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "SenderNumber", ErrorMessage: "'SenderNumber' must not be empty."})
	}
	if strings.TrimSpace(s.ReceiverId) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ReceiverId", ErrorMessage: "'ReceiverId' must not be empty."})
	}
	return out
}
