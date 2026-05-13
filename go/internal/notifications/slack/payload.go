package slack

// Payload is the outer JSON envelope POSTed to the Slack
// incoming-webhook endpoint. Mirrors C# `SlackPayload` at
// `Notifications/Slack/Payloads/SlackPayload.cs:7-17`.
//
// JSON tags use the Slack API's lowerCamelCase / snake_case mix —
// "icon_emoji" / "icon_url" are snake_case per the Slack API
// contract; the rest mirror Newtonsoft defaults.
type Payload struct {
	// Text is the top-level message body rendered above the
	// attachment cards.
	Text string `json:"text"`
	// Username overrides the webhook's default bot username.
	Username string `json:"username"`
	// IconEmoji is the emoji-form avatar override (":parrot:"
	// style). Mutually exclusive with IconURL — C# `Slack.cs:251`
	// picks one based on the leading colon.
	IconEmoji string `json:"icon_emoji,omitempty"`
	// IconURL is the URL-form avatar override.
	IconURL string `json:"icon_url,omitempty"`
	// Channel is the optional channel override.
	Channel string `json:"channel,omitempty"`
	// Attachments is the per-event rich-card list.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment mirrors C# `Attachment` at
// `Notifications/Slack/Payloads/Attachment.cs:3-12`. Slack supports
// many more fields than the 4 below; Sonarr only emits these.
type Attachment struct {
	// Fallback is the plain-text fallback rendered when the client
	// can't display the rich attachment.
	Fallback string `json:"fallback,omitempty"`
	// Title is the attachment headline.
	Title string `json:"title,omitempty"`
	// Text is the attachment body.
	Text string `json:"text,omitempty"`
	// Color is the Slack-known sidebar-color keyword: "good" /
	// "warning" / "danger" / hex like "#00ff00".
	Color string `json:"color,omitempty"`
}

// Sidebar color keywords. Mirror the C# string literals scattered
// through `Slack.cs:37,54,148,166,182,199`.
const (
	ColorGood    = "good"
	ColorWarning = "warning"
	ColorDanger  = "danger"
)
