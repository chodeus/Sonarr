// Package webhook ports the C# `NzbDrone.Core.Notifications.Webhook`
// concrete notification provider. Slice 4.AE (sub-arc N3a of queue
// [N3]).
//
// Scope at sub-arc N3a:
//   - [Settings] + Validate ports `WebhookSettings.cs` field-by-field.
//   - [Proxy] ports the HTTP wrapper for the SendWebhook path
//     (POST/PUT JSON with optional Basic auth + custom headers).
//   - [Webhook] Provider implements [notifications.Provider]. All 11
//     OnX hooks build the matching payload + invoke the proxy. Test()
//     sends a synthetic test payload. ProcessQueue is a no-op (the
//     webhook family has no library-refresh coalescing buffer).
//
// Family pattern validated by this sub-arc:
//   - init() time registration into [notifications.Default].
//   - Settings deserialisation from the persisted JSON
//     (Notifications.Settings column).
//   - HTTP proxy wrapper that consumes net/http with a per-test
//     httptest.Server-mockable transport.
//   - Provider.Test surfaces FluentValidation-style failures.
//
// Out-of-scope (deferred to follow-up sub-arc N3a-bis or to the
// Discord slice N3b):
//   - Wire-faithful nested payload structs (WebhookSeries,
//     WebhookEpisode, WebhookRelease, WebhookEpisodeFile etc — see
//     `src/NzbDrone.Core/Notifications/Webhook/*.cs`). These require
//     parser.RemoteEpisode + customformats + episodefiles richer
//     fields that haven't ported. The Go port stubs the nested
//     payload sub-objects as `any` and populates them with the
//     concrete go-port types where they exist (series.Series,
//     healthcheck.HealthCheck, episodefiles.File, etc.).
//
// C# cites lead each type with a file:line reference.
package webhook

import (
	"net/url"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `WebhookSettings` at
// `src/NzbDrone.Core/Notifications/Webhook/WebhookSettings.cs:17-46`.
//
// Field order + JSON tag names match the persisted Settings JSON
// (the React UI form posts JSON keys lowerCamelCase matching the
// C# property names via Newtonsoft's default contract resolver).
//
// Default values mirror the C# constructor at `:21-25`:
// Method=POST (1), Headers=[] (empty list).
type Settings struct {
	// URL is the webhook target URL. Required; validated with
	// IsValidUrl by the C# `WebhookSettingsValidator` at
	// `WebhookSettings.cs:13`.
	URL string `json:"url"`

	// Method is the HTTP method (POST=1 or PUT=2 — see [Method]
	// consts). C# `:31` stores an int because the React UI's
	// Select widget posts the enum's int value.
	Method int `json:"method"`

	// Username is the optional Basic-auth username. When either
	// Username or Password is non-empty, the proxy attaches a
	// Basic Authorization header. C# `:34`.
	Username string `json:"username"`

	// Password is the optional Basic-auth password. See [Username].
	// C# `:37`.
	Password string `json:"password"`

	// Headers is the optional custom-header list. Each entry is a
	// (name, value) pair the proxy adds to the outgoing request.
	// C# `:40` (IEnumerable<KeyValuePair<string,string>>).
	Headers []HeaderEntry `json:"headers"`
}

// HeaderEntry is one (Key, Value) pair from the [Settings.Headers]
// list. Mirrors the C# `KeyValuePair<string,string>` shape with
// lowerCamelCase JSON tags matching the React UI's KeyValueList
// widget post body.
type HeaderEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DefaultSettings returns a Settings struct populated with the C#
// constructor defaults at `WebhookSettings.cs:21-25` —
// Method=POST, Headers=empty list.
func DefaultSettings() Settings {
	return Settings{
		Method:  int(MethodPOST),
		Headers: []HeaderEntry{},
	}
}

// Validate runs the FluentValidation-equivalent rules from the C#
// `WebhookSettingsValidator` at `WebhookSettings.cs:9-15`.
//
// C# defines exactly one rule: `RuleFor(c => c.Url).IsValidUrl()`.
// The Go port checks for non-empty URL + url.Parse success +
// absolute (scheme + host present), matching the
// `NzbDrone.Common.Extensions.UrlValidation.IsValidUrl` semantics.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	trimmed := strings.TrimSpace(s.URL)
	if trimmed == "" {
		out = append(out, notifications.TestFailure{
			PropertyName: "Url",
			ErrorMessage: "Please enter a valid URL.",
		})
		return out
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		out = append(out, notifications.TestFailure{
			PropertyName: "Url",
			ErrorMessage: "Please enter a valid URL.",
		})
	}
	return out
}
