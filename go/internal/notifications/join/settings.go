// Package join ports the C# `NzbDrone.Core.Notifications.Join`
// concrete notification provider. Slice 4.CI (C-cleanup [C3]
// sub-arc C3h).
//
// Surface: Settings + Validate + Priority enum + Proxy (GET to
// joinjoaomgcd.appspot.com/_ah/api/messaging/v1/sendPush with query
// params + JSON response body indicating success/failure) + all 11
// OnX hooks.
//
// Auth: Join's "API key" is a plain query-string param — no
// Authorization header. The "deviceNames" or "deviceIds" param targets
// specific receivers; without either the push goes to "group.all".
package join

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Priority mirrors the C# `JoinPriority` enum at `JoinPriority.cs:3-10`.
// Stored as int on Settings; values -2..2.
type Priority int

// Priority values mirror the C# enum names.
const (
	PrioritySilent    Priority = -2
	PriorityQuiet     Priority = -1
	PriorityNormal    Priority = 0
	PriorityHigh      Priority = 1
	PriorityEmergency Priority = 2
)

// Settings ports `JoinSettings` at
// `Notifications/Join/JoinSettings.cs:16-41`.
type Settings struct {
	// ApiKey is the Join API key — REQUIRED. C# `:26`.
	ApiKey string `json:"apiKey"`
	// DeviceIds is a legacy comma-separated device-id list — the
	// C# validator REJECTS this in favor of DeviceNames. Kept on
	// the struct for migration-path round-tripping. C# `:29`.
	DeviceIds string `json:"deviceIds"`
	// DeviceNames is the comma-separated device-name list. C# `:32`.
	DeviceNames string `json:"deviceNames"`
	// Priority is the int-encoded [Priority]. Defaults to Normal=0.
	// C# `:35`.
	Priority int `json:"priority"`
}

// Validate ports the C# `JoinSettingsValidator` at `:7-13`.
//
// Rules:
//   - ApiKey non-empty.
//   - DeviceIds must be EMPTY (legacy field deprecated). C# `:12`.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.ApiKey) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ApiKey", ErrorMessage: "'ApiKey' must not be empty."})
	}
	if strings.TrimSpace(s.DeviceIds) != "" {
		out = append(out, notifications.TestFailure{PropertyName: "DeviceIds", ErrorMessage: "Use Device Names instead"})
	}
	return out
}
