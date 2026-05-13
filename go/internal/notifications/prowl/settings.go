// Package prowl ports the C# `NzbDrone.Core.Notifications.Prowl`
// concrete notification provider. Slice 4.CE (C-cleanup [C3]
// sub-arc C3d).
//
// Surface: Settings + Validate + Priority enum + Proxy (form-POST to
// api.prowlapp.com/publicapi/add with apikey/application/event/
// description/priority) + all 11 OnX hooks.
package prowl

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Priority mirrors the C# `ProwlPriority` enum at
// `ProwlPriority.cs:3-10`. Stored as int on Settings; values -2..2.
type Priority int

// Priority values mirror the C# enum names.
const (
	PriorityVeryLow   Priority = -2
	PriorityLow       Priority = -1
	PriorityNormal    Priority = 0
	PriorityHigh      Priority = 1
	PriorityEmergency Priority = 2
)

// Settings ports `ProwlSettings` at
// `Notifications/Prowl/ProwlSettings.cs:15-31`.
type Settings struct {
	// ApiKey is the Prowl API key — REQUIRED. C# `:20`.
	ApiKey string `json:"apiKey"`
	// Priority is the int-encoded ProwlPriority value. C# `:23`.
	Priority int `json:"priority"`
}

// Validate ports the C# `ProwlSettingsValidator` at `:7-13` + the
// IsValid Priority range check at `:25`.
//
// Rules:
//   - ApiKey non-empty.
//   - Priority in [-2,2] inclusive.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.ApiKey) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ApiKey", ErrorMessage: "'ApiKey' must not be empty."})
	}
	if s.Priority < int(PriorityVeryLow) || s.Priority > int(PriorityEmergency) {
		out = append(out, notifications.TestFailure{PropertyName: "Priority", ErrorMessage: "Priority must be between -2 and 2"})
	}
	return out
}
