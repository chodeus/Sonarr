// Package twitter ports the C# `NzbDrone.Core.Notifications.Twitter`
// concrete notification provider. Slice 4.CL (C-cleanup [C3]
// sub-arc C3k).
//
// Surface: Settings + Validate + Proxy (OAuth1-signed POST to
// api.twitter.com — statuses/update.json for status tweets or
// direct_messages/new.json for DMs) + all 11 OnX hooks.
//
// Deferred (matches C5 #11): the C# `Twitter.RequestAction` admin
// path (startOAuth + getOAuthToken) drives the per-row OAuth1
// 3-legged handshake — the Go-port Provider interface has no
// equivalent RequestAction slot yet; the GetOAuthRedirect /
// CompleteOAuth helpers are exported for forward compat when the UI-
// action dispatch lands.
package twitter

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `TwitterSettings` at
// `Notifications/Twitter/TwitterSettings.cs:30-65`.
type Settings struct {
	// ConsumerKey is the Twitter app consumer key — REQUIRED.
	// C# `:41`.
	ConsumerKey string `json:"consumerKey"`
	// ConsumerSecret is the Twitter app consumer secret — REQUIRED.
	// C# `:44`.
	ConsumerSecret string `json:"consumerSecret"`
	// AccessToken is the per-user OAuth1 access token — REQUIRED.
	// C# `:47`.
	AccessToken string `json:"accessToken"`
	// AccessTokenSecret is the per-user OAuth1 access-token secret —
	// REQUIRED. C# `:50`.
	AccessTokenSecret string `json:"accessTokenSecret"`
	// Mention is the screen-name appended to status tweets OR the
	// target screen-name for DMs. REQUIRED when DirectMessage=true.
	// C# `:53`.
	Mention string `json:"mention"`
	// DirectMessage toggles DM vs public-status mode. Defaults true.
	// C# `:56`.
	DirectMessage bool `json:"directMessage"`
}

// Validate ports the C# `TwitterSettingsValidator` at `:8-27`.
//
// Rules:
//   - ConsumerKey/ConsumerSecret/AccessToken/AccessTokenSecret non-empty.
//   - Mention non-empty when DirectMessage=true.
//
// The C# `DirectMessage.Equal(true)` warning + `AuthorizeNotification.Empty`
// rules are advisory-only and DEFERRED.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.ConsumerKey) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ConsumerKey", ErrorMessage: "'ConsumerKey' must not be empty."})
	}
	if strings.TrimSpace(s.ConsumerSecret) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ConsumerSecret", ErrorMessage: "'ConsumerSecret' must not be empty."})
	}
	if strings.TrimSpace(s.AccessToken) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "AccessToken", ErrorMessage: "'AccessToken' must not be empty."})
	}
	if strings.TrimSpace(s.AccessTokenSecret) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "AccessTokenSecret", ErrorMessage: "'AccessTokenSecret' must not be empty."})
	}
	if s.DirectMessage && strings.TrimSpace(s.Mention) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Mention", ErrorMessage: "'Mention' must not be empty when DirectMessage is enabled."})
	}
	return out
}
