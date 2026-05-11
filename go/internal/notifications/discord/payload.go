package discord

// Color is the embed-tile sidebar color the Discord renderer
// uses. Mirrors the C# `DiscordColors` enum at
// `Notifications/Discord/DiscordColors.cs:3-10`.
//
// Values are the C# int RGB encodings — preserved exactly so
// existing Discord consumers render the same colors.
type Color int

const (
	// ColorDanger is the red used for error-level events.
	ColorDanger Color = 15749200
	// ColorSuccess is the green used for restored events.
	ColorSuccess Color = 2605644
	// ColorWarning is the orange used for warning-level events.
	ColorWarning Color = 16753920
	// ColorStandard is the neutral color used for grab/series-add.
	ColorStandard Color = 16761392
	// ColorUpgrade is the blue used for upgrade-import events.
	ColorUpgrade Color = 4089856
)

// GrabField mirrors C# `DiscordGrabFieldType` at
// `Notifications/Discord/DiscordFieldType.cs:3-18`.
type GrabField int

// GrabField values in the C# enum order (0-indexed; the C# enum
// has no explicit values so the int defaults match).
const (
	GrabFieldOverview GrabField = iota
	GrabFieldRating
	GrabFieldGenres
	GrabFieldQuality
	GrabFieldGroup
	GrabFieldSize
	GrabFieldLinks
	GrabFieldRelease
	GrabFieldPoster
	GrabFieldFanart
	GrabFieldIndexer
	GrabFieldCustomFormats
	GrabFieldCustomFormatScore
)

// ImportField mirrors C# `DiscordImportFieldType` at
// `Notifications/Discord/DiscordFieldType.cs:20-37`.
type ImportField int

// ImportField values in the C# enum order.
const (
	ImportFieldOverview ImportField = iota
	ImportFieldRating
	ImportFieldGenres
	ImportFieldQuality
	ImportFieldCodecs
	ImportFieldGroup
	ImportFieldSize
	ImportFieldLanguages
	ImportFieldSubtitles
	ImportFieldLinks
	ImportFieldRelease
	ImportFieldPoster
	ImportFieldFanart
	ImportFieldCustomFormats
	ImportFieldCustomFormatScore
)

// ManualField mirrors C# `DiscordManualInteractionFieldType` at
// `Notifications/Discord/DiscordFieldType.cs:39-51`.
type ManualField int

// ManualField values in the C# enum order.
const (
	ManualFieldOverview ManualField = iota
	ManualFieldRating
	ManualFieldGenres
	ManualFieldQuality
	ManualFieldGroup
	ManualFieldSize
	ManualFieldLinks
	ManualFieldDownloadTitle
	ManualFieldPoster
	ManualFieldFanart
)

// Payload is the outer JSON envelope POSTed to the Discord webhook
// endpoint. Mirrors C# `DiscordPayload` at
// `Notifications/Discord/Payloads/DiscordPayload.cs:7-16`.
//
// Field names + JSON tags match the Discord webhook API exactly —
// "avatar_url" is snake_case per Discord's API contract; the
// other fields are camelCase via the C# Newtonsoft default.
type Payload struct {
	// Content is the optional message text rendered above the
	// embed cards. C# `:9`. Most events omit this (empty string
	// is JSON-encoded as ""); the embed carries the actual body.
	Content string `json:"content,omitempty"`

	// Username overrides the webhook's default bot username. Maps
	// to [Settings.Username]. C# `:11`.
	Username string `json:"username,omitempty"`

	// AvatarURL overrides the webhook's default bot avatar. Maps
	// to [Settings.Avatar]. C# `:13` — wire-tagged
	// `avatar_url` per Discord's API.
	AvatarURL string `json:"avatar_url,omitempty"`

	// Embeds is the per-event rich-card array. Discord supports
	// up to 10 embeds per message; the Go port emits one per
	// event. C# `:15`.
	Embeds []Embed `json:"embeds,omitempty"`
}

// Embed mirrors C# `Embed` at
// `Notifications/Discord/Payloads/Embed.cs:5-17`.
//
// Field order matches the C# class; JSON tag names match the
// Discord webhook API (lowerCamelCase except Discord-prescribed
// snake_case which doesn't apply at this level).
type Embed struct {
	// Description is the embed body text.
	Description string `json:"description,omitempty"`
	// Title is the embed headline (rendered bold + large).
	Title string `json:"title,omitempty"`
	// Text is an alternative body slot — unused by Sonarr's
	// emitters but ported for wire-format completeness.
	Text string `json:"text,omitempty"`
	// Color is the sidebar color (see [Color] consts).
	Color int `json:"color,omitempty"`
	// URL is the click-through URL the title links to.
	URL string `json:"url,omitempty"`
	// Author is the (optional) embed-author bar (name + icon).
	Author *Author `json:"author,omitempty"`
	// Thumbnail is the small image rendered in the embed corner.
	Thumbnail *Image `json:"thumbnail,omitempty"`
	// Image is the wide image rendered below the body.
	Image *Image `json:"image,omitempty"`
	// Timestamp is the ISO-8601 "O"-format string the C# emitter
	// produces via `DateTime.UtcNow.ToString("O")`.
	Timestamp string `json:"timestamp,omitempty"`
	// Fields is the per-feature key/value list rendered as
	// inline-or-block tiles.
	Fields []Field `json:"fields,omitempty"`
}

// Author mirrors C# `DiscordAuthor` at
// `Notifications/Discord/Payloads/DiscordAuthor.cs:5-10`.
type Author struct {
	// Name is the author display name.
	Name string `json:"name"`
	// IconURL is the small icon next to the author name. Discord
	// API requires snake_case `icon_url`.
	IconURL string `json:"icon_url,omitempty"`
}

// Field mirrors C# `DiscordField` at
// `Notifications/Discord/Payloads/DiscordField.cs:5-9`.
type Field struct {
	// Name is the field label.
	Name string `json:"name"`
	// Value is the field body — Discord renders markdown.
	Value string `json:"value"`
	// Inline lays the field side-by-side with the next inline=true
	// field rather than full-width.
	Inline bool `json:"inline,omitempty"`
}

// Image mirrors C# `DiscordImage` at
// `Notifications/Discord/Payloads/DiscordImage.cs:3-6`.
type Image struct {
	// URL is the image source.
	URL string `json:"url"`
}
