// Package slack ports the C# `NzbDrone.Core.Notifications.Slack`
// concrete notification provider. Slice 4.CB (C-cleanup [C3]
// sub-arc C3a).
//
// Scope at sub-arc C3a (Slack):
//   - [Settings] + Validate ports SlackSettings.cs field-for-field
//     (WebHookURL + Username + Icon + Channel).
//   - Payload structs (SlackPayload + Attachment) port the
//     `Slack/Payloads/*.cs` wire shapes.
//   - [Slack] Provider implements [notifications.Provider]. All 11
//     OnX hooks build the matching Attachment payload + invoke the
//     proxy. Test() sends a synthetic message via SendPayload.
//
// Follows the slice 4.AE (webhook) + 4.AF (discord) precedent —
// per-provider package + per-method per-event payload + init()
// registers with notifications.Default().
package slack

import (
	"net/url"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `SlackSettings` at
// `src/NzbDrone.Core/Notifications/Slack/SlackSettings.cs:16-37`.
type Settings struct {
	// WebHookURL is the Slack incoming-webhook URL — required.
	// Slack embeds the channel + token in the URL itself. C# `:20`.
	WebHookURL string `json:"webHookUrl"`

	// Username is the bot display name — required per validator
	// `:11`. C# `:23`.
	Username string `json:"username"`

	// Icon is the optional emoji-or-URL avatar override. Format:
	// ":emoji:" → renders as Slack emoji; otherwise treated as a URL.
	// C# `:26`.
	Icon string `json:"icon"`

	// Channel is the optional channel override (e.g. "#sonarr" or
	// "@me"). When empty, Slack uses the webhook's default channel.
	// C# `:29`.
	Channel string `json:"channel"`
}

// Validate runs the FluentValidation-equivalent rules from C#
// `SlackSettingsValidator` at `:8-14`: WebHookURL must parse as
// a valid http(s) URL; Username must be non-empty.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	trimmed := strings.TrimSpace(s.WebHookURL)
	if trimmed == "" {
		out = append(out, notifications.TestFailure{
			PropertyName: "WebHookUrl",
			ErrorMessage: "Please enter a valid URL.",
		})
	} else if u, err := url.Parse(trimmed); err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		out = append(out, notifications.TestFailure{
			PropertyName: "WebHookUrl",
			ErrorMessage: "Please enter a valid URL.",
		})
	}
	if strings.TrimSpace(s.Username) == "" {
		out = append(out, notifications.TestFailure{
			PropertyName: "Username",
			ErrorMessage: "'Username' must not be empty.",
		})
	}
	return out
}
