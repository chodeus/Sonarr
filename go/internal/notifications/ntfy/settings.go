// Package ntfy ports the C# `NzbDrone.Core.Notifications.Ntfy`
// concrete notification provider. Slice 4.CD (C-cleanup [C3]
// sub-arc C3c).
//
// Surface: Settings + Validate + Priority enum + Proxy (POST to
// {ServerUrl|https://ntfy.sh}/{topic} with title/message/priority/
// tags/click as query-string params + optional Bearer token OR Basic
// auth) + all 11 OnX hooks.
package ntfy

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Priority mirrors the C# `NtfyPriority` enum at
// `NtfyPriority.cs:3-10`. Stored as int on Settings; values 1-5.
type Priority int

// Priority values mirror the C# enum names.
const (
	PriorityMin     Priority = 1
	PriorityLow     Priority = 2
	PriorityDefault Priority = 3
	PriorityHigh    Priority = 4
	PriorityMax     Priority = 5
)

// Settings ports `NtfySettings` at
// `Notifications/Ntfy/NtfySettings.cs:26-65`.
type Settings struct {
	// ServerUrl is the ntfy server base URL — defaults to
	// https://ntfy.sh when blank. C# `:38`.
	ServerUrl string `json:"serverUrl"`
	// AccessToken is an ntfy Bearer access-token. When set takes
	// precedence over UserName+Password. C# `:41`.
	AccessToken string `json:"accessToken"`
	// UserName is the Basic-auth username paired with Password.
	// C# `:44`.
	UserName string `json:"userName"`
	// Password is the Basic-auth password. C# `:47`.
	Password string `json:"password"`
	// Priority is the ntfy 1-5 priority. Defaults to 3 (Default).
	// C# `:50`.
	Priority int `json:"priority"`
	// Topics is the per-topic fan-out list — each POST goes to
	// {server}/{topic}. C# `:53`.
	Topics []string `json:"topics"`
	// Tags is the tag list appended as a comma-separated "tags"
	// query param. C# `:56`.
	Tags []string `json:"tags"`
	// ClickUrl is the optional click-through URL surfaced on the
	// notification. C# `:59`.
	ClickUrl string `json:"clickUrl"`
}

// invalidTopics matches C# `:23`. Ntfy reserves these topic names
// for system use; the Sonarr validator rejects them.
var invalidTopics = map[string]bool{
	"announcements": true, "app": true, "docs": true, "settings": true,
	"stats": true, "mytopic-rw": true, "mytopic-ro": true, "mytopic-wo": true,
}

var topicNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Validate ports the C# `NtfySettingsValidator` at `:10-23`.
//
// Rules:
//   - Topics non-empty.
//   - Priority in [1,5] inclusive.
//   - ServerUrl is valid URL when non-empty.
//   - ClickUrl is valid URL when non-empty.
//   - UserName required when Password set AND AccessToken blank.
//   - Password required when UserName set AND AccessToken blank.
//   - Per-topic: non-empty, matches [a-zA-Z0-9_-]+, not in InvalidTopics.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if len(s.Topics) == 0 {
		out = append(out, notifications.TestFailure{PropertyName: "Topics", ErrorMessage: "'Topics' must not be empty."})
	}
	if s.Priority < int(PriorityMin) || s.Priority > int(PriorityMax) {
		out = append(out, notifications.TestFailure{PropertyName: "Priority", ErrorMessage: "Priority must be between 1 and 5"})
	}
	if u := strings.TrimSpace(s.ServerUrl); u != "" && !isHTTPURL(u) {
		out = append(out, notifications.TestFailure{PropertyName: "ServerUrl", ErrorMessage: "Server URL must be a valid URL"})
	}
	if u := strings.TrimSpace(s.ClickUrl); u != "" && !isHTTPURL(u) {
		out = append(out, notifications.TestFailure{PropertyName: "ClickUrl", ErrorMessage: "Click URL must be a valid URL"})
	}
	tokenBlank := strings.TrimSpace(s.AccessToken) == ""
	if tokenBlank && strings.TrimSpace(s.Password) != "" && strings.TrimSpace(s.UserName) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "UserName", ErrorMessage: "'UserName' must not be empty when Password is set."})
	}
	if tokenBlank && strings.TrimSpace(s.UserName) != "" && strings.TrimSpace(s.Password) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Password", ErrorMessage: "'Password' must not be empty when UserName is set."})
	}
	for _, t := range s.Topics {
		if t == "" {
			out = append(out, notifications.TestFailure{PropertyName: "Topics", ErrorMessage: "Topic must not be empty"})
			continue
		}
		if !topicNameRE.MatchString(t) || invalidTopics[t] {
			out = append(out, notifications.TestFailure{PropertyName: "Topics", ErrorMessage: "Invalid topic"})
		}
	}
	return out
}

func isHTTPURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}
