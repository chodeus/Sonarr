// Package discord ports the C# `NzbDrone.Core.Notifications.Discord`
// concrete notification provider. Slice 4.AF (sub-arc N3b of queue
// [N3]).
//
// Scope at sub-arc N3b:
//   - [Settings] + Validate ports `DiscordSettings.cs` field-by-field
//     including the three multi-Select default lists
//     (GrabFields, ImportFields, ManualInteractionFields).
//   - Payload structs (DiscordPayload + Embed + DiscordAuthor +
//     DiscordField + DiscordImage) port `Discord/Payloads/*.cs`.
//   - DiscordColors + DiscordGrabFieldType + DiscordImportFieldType
//   - DiscordManualInteractionFieldType port the per-feature enums.
//   - [Discord] Provider implements [notifications.Provider]. All
//     11 OnX hooks build the matching Embed payload + invoke the
//     proxy. Test() sends a synthetic test embed.
//
// Out-of-scope (deferred sub-arc):
//   - Rich Embed field-by-field rendering for OnGrab / OnDownload /
//     OnImportComplete / OnRename / OnManualInteractionRequired —
//     the C# field-switch at `Discord.cs:70-150` reaches into
//     parser.RemoteEpisode + customformats + MediaInfo + decoded
//     series.Images / series.Ratings / series.Genres. None of these
//     are ported yet (series.Images/Ratings/Genres are stored as
//     raw JSON strings per internal/series/series.go:14-17). This
//     slice produces well-shaped Embeds with the available fields;
//     the field-render deferred until the upstream types port.
//   - GetLinksString helper (Discord.cs:631-723) reaches into
//     series identifier helpers (imdb/tvdb/trakt/tvmaze) not yet
//     ported; the Go port omits the Links field for now.
//
// C# cites lead each type with a file:line reference.
package discord

import (
	"net/url"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Default field-selection lists mirror the C# DiscordSettings
// constructor at `DiscordSettings.cs:18-66`. Storing them as
// package-level vars (not consts — Go arrays/slices can't be
// const) so DefaultSettings can copy them.
var (
	defaultGrabFields = []int{
		int(GrabFieldOverview), int(GrabFieldRating), int(GrabFieldGenres),
		int(GrabFieldQuality), int(GrabFieldGroup), int(GrabFieldSize),
		int(GrabFieldLinks), int(GrabFieldRelease), int(GrabFieldPoster),
		int(GrabFieldFanart), int(GrabFieldIndexer), int(GrabFieldCustomFormats),
		int(GrabFieldCustomFormatScore),
	}
	defaultImportFields = []int{
		int(ImportFieldOverview), int(ImportFieldRating), int(ImportFieldGenres),
		int(ImportFieldQuality), int(ImportFieldCodecs), int(ImportFieldGroup),
		int(ImportFieldSize), int(ImportFieldLanguages), int(ImportFieldSubtitles),
		int(ImportFieldLinks), int(ImportFieldRelease), int(ImportFieldPoster),
		int(ImportFieldFanart),
	}
	defaultManualInteractionFields = []int{
		int(ManualFieldOverview), int(ManualFieldRating), int(ManualFieldGenres),
		int(ManualFieldQuality), int(ManualFieldGroup), int(ManualFieldSize),
		int(ManualFieldLinks), int(ManualFieldDownloadTitle), int(ManualFieldPoster),
		int(ManualFieldFanart),
	}
)

// Settings ports `DiscordSettings` at
// `src/NzbDrone.Core/Notifications/Discord/DiscordSettings.cs:16-95`.
//
// JSON tag names match the persisted Settings JSON (the React UI
// form posts JSON keys lowerCamelCase matching the C# property
// names via Newtonsoft's default contract resolver).
type Settings struct {
	// WebHookURL is the Discord webhook URL — required. The URL
	// embeds the channel + token; auth is handled by the URL
	// itself. C# `:71`.
	WebHookURL string `json:"webHookUrl"`

	// Username is the optional display-name override for the bot.
	// When empty, Discord uses the webhook's default username.
	// C# `:74`.
	Username string `json:"username"`

	// Avatar is the optional avatar-URL override. When empty,
	// Discord uses the webhook's default avatar. C# `:77`.
	Avatar string `json:"avatar"`

	// Author is the optional embed-author override. When empty,
	// the embed author falls back to InstanceName (C# `Discord.cs:43`).
	// C# `:80`.
	Author string `json:"author"`

	// GrabFields is the int-list of [GrabField] values to include
	// in the OnGrab embed. Default: every value. C# `:83`.
	GrabFields []int `json:"grabFields"`

	// ImportFields — see [GrabFields] for the OnDownload /
	// OnImportComplete embed. C# `:86`.
	ImportFields []int `json:"importFields"`

	// ManualInteractionFields — see [GrabFields] for the
	// OnManualInteractionRequired embed. C# `:89`.
	ManualInteractionFields []int `json:"manualInteractionFields"`
}

// DefaultSettings returns a Settings struct with the C# constructor
// defaults at `DiscordSettings.cs:18-66`. The three multi-select
// field lists default to every supported value (the C# UI Select
// widget initialises with the full set).
func DefaultSettings() Settings {
	gf := make([]int, len(defaultGrabFields))
	copy(gf, defaultGrabFields)
	im := make([]int, len(defaultImportFields))
	copy(im, defaultImportFields)
	mi := make([]int, len(defaultManualInteractionFields))
	copy(mi, defaultManualInteractionFields)
	return Settings{
		GrabFields:              gf,
		ImportFields:            im,
		ManualInteractionFields: mi,
	}
}

// Validate runs the FluentValidation-equivalent rules from the C#
// `DiscordSettingsValidator` at `DiscordSettings.cs:8-14`.
//
// C# defines exactly one rule:
// `RuleFor(c => c.WebHookUrl).IsValidUrl()`.
// The Go port checks for non-empty URL + url.Parse success +
// absolute (scheme + host present) with scheme in {http, https}.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	trimmed := strings.TrimSpace(s.WebHookURL)
	if trimmed == "" {
		out = append(out, notifications.TestFailure{
			PropertyName: "WebHookUrl",
			ErrorMessage: "Please enter a valid URL.",
		})
		return out
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		out = append(out, notifications.TestFailure{
			PropertyName: "WebHookUrl",
			ErrorMessage: "Please enter a valid URL.",
		})
	}
	return out
}
