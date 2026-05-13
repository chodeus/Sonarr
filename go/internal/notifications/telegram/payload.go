package telegram

// Payload ports the C# `TelegramPayload` at
// `Notifications/Telegram/TelegramPayload.cs:5-22`.
//
// JSON field-shape parity (Option A wire-shape policy): all field
// names byte-match the C# `[JsonProperty(...)]` annotations. ParseMode
// is fixed at "HTML" — the proxy never overrides it.
//
// MessageThreadId is a *int (nullable) to round-trip the C# `int?`:
// when Settings.TopicId is nil the field is JSON-omitted (omitempty
// on a pointer drops nil — C# parity is "absent property").
type Payload struct {
	ChatID              string         `json:"chat_id"`
	ParseMode           string         `json:"parse_mode"`
	Text                string         `json:"text"`
	DisableNotification bool           `json:"disable_notification"`
	MessageThreadID     *int           `json:"message_thread_id,omitempty"`
	LinkPreviewOptions  *PreviewOption `json:"link_preview_options,omitempty"`
}

// PreviewOption ports the C# `TelegramLinkPreviewOptions` at
// `Notifications/Telegram/TelegramLinkPreviewOptions.cs:7-20`.
//
// IsDisabled is true when Settings.LinkPreview == None (-1).
// URL is set to the first link in `links` whose Type matches
// Settings.LinkPreview; empty otherwise — the C# code uses
// LINQ `.FirstOrDefault(...)?.Link` and JSON-serialises null when
// no link matches. The Go port omits the URL key on empty via
// omitempty for byte-shape parity.
type PreviewOption struct {
	IsDisabled bool   `json:"is_disabled"`
	URL        string `json:"url,omitempty"`
}
