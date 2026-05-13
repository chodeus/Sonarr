// Package simplepush ports the C# `NzbDrone.Core.Notifications.Simplepush`
// concrete notification provider. Slice 4.CD (C-cleanup [C3]
// sub-arc C3c).
//
// Surface: Settings + Validate + Proxy (form-POST to
// https://api.simplepush.io/send with key/event/title/msg) + all 11
// OnX hooks.
package simplepush

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `SimplepushSettings` at
// `Notifications/Simplepush/SimplepushSettings.cs:15-31`.
type Settings struct {
	// Key is the simplepush user key — REQUIRED. C# `:20`.
	Key string `json:"key"`
	// Event is an optional event tag the user can attach to the
	// push for filtering. C# `:23`.
	Event string `json:"event"`
}

// Validate ports the C# `SimplepushSettingsValidator` at `:7-13`.
//
// Rules:
//   - Key non-empty.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.Key) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Key", ErrorMessage: "'Key' must not be empty."})
	}
	return out
}
