// Package pushover ports the C# `NzbDrone.Core.Notifications.Pushover`
// concrete notification provider. Slice 4.CB (C-cleanup [C3]
// sub-arc C3a).
//
// Scope: [Settings] + Validate ports PushoverSettings.cs field-for-
// field; [Pushover] Provider implements [notifications.Provider]
// with 10 OnX hooks (no OnRename per the C# parent class — Pushover
// doesn't override that path); init() registers with
// notifications.Default().
package pushover

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Priority mirrors C# `PushoverPriority` at
// `Notifications/Pushover/PushoverPriority.cs:1-11`.
type Priority int

// Priority values mirror C# enum-name → int.
const (
	PrioritySilent    Priority = -2
	PriorityQuiet     Priority = -1
	PriorityNormal    Priority = 0
	PriorityHigh      Priority = 1
	PriorityEmergency Priority = 2
)

// Settings ports `PushoverSettings` at
// `src/NzbDrone.Core/Notifications/Pushover/PushoverSettings.cs:20-62`.
type Settings struct {
	// ApiKey is the per-app API key (Sonarr ships its own; users
	// can clone it). When empty, falls back to the Sonarr default
	// on the proxy. C# `:32`.
	ApiKey string `json:"apiKey"`
	// UserKey is the per-user key — REQUIRED by the C# validator
	// at `:13`. C# `:35`.
	UserKey string `json:"userKey"`
	// Devices is the optional list of device names to target. When
	// empty, all of the user's devices receive the notification.
	// C# `:38`.
	Devices []string `json:"devices"`
	// Priority is the per-notification priority (see [Priority]).
	// Defaults to Normal (0). C# `:41`.
	Priority int `json:"priority"`
	// Retry is the Emergency-priority retry interval in seconds.
	// Required when Priority == Emergency; 30 <= Retry <= 86400.
	// C# `:44`.
	Retry int `json:"retry"`
	// Expire is the Emergency-priority expiry in seconds. C# `:47`.
	Expire int `json:"expire"`
	// Ttl is the per-message time-to-live (seconds). When 0, the
	// message persists per the Pushover default. C# `:50`.
	Ttl int `json:"ttl"`
	// Sound is the optional per-notification sound name. C# `:53`.
	Sound string `json:"sound"`
}

// Validate runs the FluentValidation-equivalent rules from
// `PushoverSettingsValidator` at `:11-17`. UserKey must be
// non-empty; the Retry/Ttl bounds apply only when Priority is
// Emergency.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.UserKey) == "" {
		out = append(out, notifications.TestFailure{
			PropertyName: "UserKey",
			ErrorMessage: "'UserKey' must not be empty.",
		})
	}
	if Priority(s.Priority) == PriorityEmergency {
		if s.Retry < 30 || s.Retry > 86400 {
			out = append(out, notifications.TestFailure{
				PropertyName: "Retry",
				ErrorMessage: "Retry must be between 30 and 86400 for Emergency priority.",
			})
		}
	}
	if s.Ttl < 0 {
		out = append(out, notifications.TestFailure{
			PropertyName: "Ttl",
			ErrorMessage: "Ttl must be ≥ 0.",
		})
	}
	return out
}
