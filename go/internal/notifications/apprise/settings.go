// Package apprise ports the C# `NzbDrone.Core.Notifications.Apprise`
// concrete notification provider. Slice 4.CG (C-cleanup [C3]
// sub-arc C3f).
//
// Surface: Settings + Validate + NotificationType enum + Payload +
// Proxy (POST JSON to {ServerUrl}/notify/{ConfigurationKey} OR
// {ServerUrl}/notify for stateless + optional Basic auth) + all 11
// OnX hooks.
//
// Deferred (matches C5 #11): IncludePoster requires the
// series.Images blob to be decoded into per-cover-type entries.
// The toggle is preserved on the struct; per-event hooks pass ""
// for the poster URL until the typed cover-type list ports.
package apprise

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// NotificationType mirrors the C# `AppriseNotificationType` enum at
// `AppriseNotificationType.cs:5-19`. Persisted as int on Settings;
// the JSON wire representation uses the lowercase string EnumMember
// value (info / success / warning / failure).
type NotificationType int

// NotificationType values mirror C# enum-name → int.
const (
	NotificationTypeInfo    NotificationType = 0
	NotificationTypeSuccess NotificationType = 1
	NotificationTypeWarning NotificationType = 2
	NotificationTypeFailure NotificationType = 3
)

// String returns the lowercase string the Apprise API consumes —
// mirrors the C# `[EnumMember(Value="...")]` attributes.
func (n NotificationType) String() string {
	switch n {
	case NotificationTypeSuccess:
		return "success"
	case NotificationTypeWarning:
		return "warning"
	case NotificationTypeFailure:
		return "failure"
	default:
		return "info"
	}
}

// Settings ports `AppriseSettings` at
// `Notifications/Apprise/AppriseSettings.cs:37-75`.
type Settings struct {
	// ServerUrl is the Apprise-API base URL — REQUIRED. C# `:48`.
	ServerUrl string `json:"serverUrl"`
	// ConfigurationKey identifies a server-side stored URL bundle.
	// Mutually exclusive with StatelessUrls. C# `:51`.
	ConfigurationKey string `json:"configurationKey"`
	// StatelessUrls is the comma-or-newline-separated Apprise URL
	// list. Mutually exclusive with ConfigurationKey. C# `:54`.
	StatelessUrls string `json:"statelessUrls"`
	// NotificationType is the int-encoded [NotificationType] value
	// — defaults to Info=0. C# `:57`.
	NotificationType int `json:"notificationType"`
	// Tags is the per-event tag list — incompatible with stateless
	// URLs per validator. C# `:60`.
	Tags []string `json:"tags"`
	// IncludePoster — deferred (see package doc). C# `:63`.
	IncludePoster bool `json:"includePoster"`
	// AuthUsername is the optional HTTP Basic auth username.
	// C# `:66`.
	AuthUsername string `json:"authUsername"`
	// AuthPassword is the optional HTTP Basic auth password.
	// C# `:69`.
	AuthPassword string `json:"authPassword"`
}

// configKeyRE matches the C# `Matches("^[a-z0-9-]*$")` at `:20`.
var configKeyRE = regexp.MustCompile(`^[a-z0-9-]*$`)

// Validate ports the C# `AppriseSettingsValidator` at `:10-34`.
//
// Rules:
//   - ServerUrl is a valid URL.
//   - ConfigurationKey is required when StatelessUrls is blank.
//   - ConfigurationKey, when set, must match ^[a-z0-9-]*$.
//   - StatelessUrls is required when ConfigurationKey is blank.
//   - StatelessUrls must be empty when ConfigurationKey is set
//     (mutual exclusion).
//   - Tags must be empty when StatelessUrls is set.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	server := strings.TrimSpace(s.ServerUrl)
	if server == "" || (!strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://")) {
		out = append(out, notifications.TestFailure{PropertyName: "ServerUrl", ErrorMessage: "Server URL must be a valid URL"})
	}
	keyBlank := strings.TrimSpace(s.ConfigurationKey) == ""
	statelessBlank := strings.TrimSpace(s.StatelessUrls) == ""
	if keyBlank && statelessBlank {
		out = append(out, notifications.TestFailure{PropertyName: "ConfigurationKey", ErrorMessage: "Use either Configuration Key or Stateless URLs"})
	}
	if !keyBlank && !statelessBlank {
		out = append(out, notifications.TestFailure{PropertyName: "StatelessUrls", ErrorMessage: "Use either Configuration Key or Stateless URLs"})
	}
	if !keyBlank && !configKeyRE.MatchString(s.ConfigurationKey) {
		out = append(out, notifications.TestFailure{PropertyName: "ConfigurationKey", ErrorMessage: "Allowed characters a-z, 0-9 and -"})
	}
	if !statelessBlank && len(s.Tags) > 0 {
		out = append(out, notifications.TestFailure{PropertyName: "Tags", ErrorMessage: "Stateless URLs do not support tags"})
	}
	return out
}
