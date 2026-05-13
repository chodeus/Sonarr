// Package telegram ports the C# `NzbDrone.Core.Notifications.Telegram`
// concrete notification provider. Slice 4.CC (C-cleanup [C3]
// sub-arc C3b).
//
// Surface: Settings + Validate + per-event title brand/instance
// flags + Payload (chat_id + parse_mode HTML + text + disable_notification
// + message_thread_id + link_preview_options) + Proxy (POST to
// api.telegram.org/bot{token}/sendmessage) + all 11 OnX hooks.
//
// Out of scope (matches the C5 deferred-fields posture): series-
// images-based features. Telegram doesn't use them — all the
// metadata-links work through the typed series.ImdbID / TvdbID /
// TvMazeID which ARE decoded.
package telegram

import (
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// LinkPreviewType mirrors C# `MetadataLinkPreviewType` at
// `Telegram/MetadataLinkType.cs:1-22`. None=-1, IMDb=0, TVMaze=2,
// Trakt=3 (TVDb=1 is documented as unsupported by Telegram).
type LinkPreviewType int

// LinkPreview values mirror the C# `MetadataLinkPreviewType` enum.
const (
	LinkPreviewNone   LinkPreviewType = -1
	LinkPreviewImdb   LinkPreviewType = 0
	LinkPreviewTvmaze LinkPreviewType = 2
	LinkPreviewTrakt  LinkPreviewType = 3
)

// Settings ports `TelegramSettings` at
// `Notifications/Telegram/TelegramSettings.cs:46-83`.
type Settings struct {
	// BotToken is the Telegram bot HTTP API token — REQUIRED. C# `:54`.
	BotToken string `json:"botToken"`
	// ChatId is the Telegram chat ID to message — REQUIRED. C# `:57`.
	ChatId string `json:"chatId"`
	// TopicId is the optional forum-topic ID. When set must be > 1.
	// C# `:60`.
	TopicId *int `json:"topicId"`
	// SendSilently fires the message with disabled-notification
	// (no sound). C# `:63`.
	SendSilently bool `json:"sendSilently"`
	// IncludeAppNameInTitle prepends "Sonarr - " to the per-event
	// title. C# `:66`.
	IncludeAppNameInTitle bool `json:"includeAppNameInTitle"`
	// IncludeInstanceNameInTitle appends " - {InstanceName}" to the
	// title. C# `:69`.
	IncludeInstanceNameInTitle bool `json:"includeInstanceNameInTitle"`
	// MetadataLinks is the int-list of [notifications.MetadataLinkType]
	// values to surface in the message. C# `:72`.
	MetadataLinks []int `json:"metadataLinks"`
	// LinkPreview selects which of the included links should be
	// previewed by Telegram's URL-preview engine. C# `:75`.
	LinkPreview int `json:"linkPreview"`
}

// Validate ports the C# `TelegramSettingsValidator` at `:11-44`.
//
// Rules:
//   - BotToken non-empty.
//   - ChatId non-empty.
//   - TopicId nil OR > 1.
//   - MetadataLinks contain only known enum values.
//   - LinkPreview is None OR matches one of MetadataLinks.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.BotToken) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "BotToken", ErrorMessage: "'BotToken' must not be empty."})
	}
	if strings.TrimSpace(s.ChatId) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "ChatId", ErrorMessage: "'ChatId' must not be empty."})
	}
	if s.TopicId != nil && *s.TopicId <= 1 {
		out = append(out, notifications.TestFailure{PropertyName: "TopicId", ErrorMessage: "Topic ID must be greater than 1 or empty"})
	}
	for _, l := range s.MetadataLinks {
		switch notifications.MetadataLinkType(l) {
		case notifications.MetadataLinkImdb, notifications.MetadataLinkTvdb, notifications.MetadataLinkTvmaze, notifications.MetadataLinkTrakt:
		default:
			out = append(out, notifications.TestFailure{PropertyName: "MetadataLinks", ErrorMessage: "MetadataLink is not valid"})
		}
	}
	// LinkPreview validation: known value + (None OR in MetadataLinks).
	switch LinkPreviewType(s.LinkPreview) {
	case LinkPreviewNone, LinkPreviewImdb, LinkPreviewTvmaze, LinkPreviewTrakt:
	default:
		out = append(out, notifications.TestFailure{PropertyName: "LinkPreview", ErrorMessage: "Selected value is not valid"})
	}
	if LinkPreviewType(s.LinkPreview) != LinkPreviewNone {
		found := false
		for _, l := range s.MetadataLinks {
			if l == s.LinkPreview {
				found = true
				break
			}
		}
		if !found {
			out = append(out, notifications.TestFailure{PropertyName: "LinkPreview", ErrorMessage: "Link Preview must be one of the selected Metadata Links"})
		}
	}
	return out
}
