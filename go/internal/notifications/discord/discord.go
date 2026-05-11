package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/series"
)

// Implementation is the value stored in the Notifications.Implementation
// column for rows wired to this provider. Mirrors the C# class
// name `Discord` at `Notifications/Discord/Discord.cs:18`.
const Implementation = "Discord"

// ConfigContract is the value stored in the Notifications.
// ConfigContract column. Mirrors C# `DiscordSettings` at
// `Notifications/Discord/DiscordSettings.cs:16`.
const ConfigContract = "DiscordSettings"

// authorIconURL is the Sonarr logo Discord renders next to the
// embed author bar. Hard-coded in C# at `Discord.cs:44`.
const authorIconURL = "https://raw.githubusercontent.com/Sonarr/Sonarr/develop/Logo/256.png"

// Discord ports the C# `Discord` notification provider at
// `Notifications/Discord/Discord.cs:18-739`.
//
// Scope-of-port note: rich Embed.Fields rendering for grab/import/
// rename/manual events reaches into parser.RemoteEpisode +
// customformats + MediaInfo + decoded series.Images/Ratings/Genres
// — none of which are ported. This slice produces well-shaped
// Embeds with the available fields (Title, Description, Color,
// Url, Timestamp, Author) but omits the per-feature Field tiles
// for those events. Health / SeriesAdd / SeriesDelete /
// EpisodeFileDelete / ApplicationUpdate events DO render fields
// since their source data is available. The deferred rendering is
// tracked at the package-level scope note in settings.go.
//
// Goroutine-safety: httpDoer is shared across concurrent OnX
// calls. settings is captured by value at construction time.
type Discord struct {
	def          *notifications.Notification
	settings     Settings
	doer         httpDoer
	instanceName string
	now          func() time.Time
}

// Compile-time assertion: *Discord satisfies notifications.Provider.
var _ notifications.Provider = (*Discord)(nil)

// New constructs a *Discord from a persisted [notifications.Notification]
// row + the cross-provider [notifications.Dependencies] bundle.
//
// Settings JSON deserialisation matches the webhook precedent at
// `internal/notifications/webhook/webhook.go:New`. instanceName is
// read off deps.ConfigProvider when wired (queue [N4] orchestrator
// binds this); falls back to "Sonarr" when not wired.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("discord: nil definition")
	}
	settings := DefaultSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("discord: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	instanceName := "Sonarr"
	if cp, ok := deps.ConfigProvider.(interface{ InstanceName() string }); ok {
		if name := cp.InstanceName(); name != "" {
			instanceName = name
		}
	}
	return &Discord{
		def:          def,
		settings:     settings,
		doer:         doer,
		instanceName: instanceName,
		now:          func() time.Time { return time.Now().UTC() },
	}, nil
}

// Name returns the provider display label. C# `Name` at `Discord.cs:31`.
func (d *Discord) Name() string { return "Discord" }

// Link returns the docs URL. C# `Link` at `Discord.cs:32`.
func (d *Discord) Link() string {
	return "https://support.discordapp.com/hc/en-us/articles/228383668-Intro-to-Webhooks"
}

// Definition returns the persisted row.
func (d *Discord) Definition() *notifications.Notification { return d.def }

// baseEmbed builds the per-event shared Embed shape — author + URL
// fallback + timestamp. Mirrors the prelude every C# OnX builder
// shares (e.g. `Discord.cs:39-52`).
//
// seriesURL — when non-empty, becomes Embed.URL. C# uses
// thetvdb.com/?tab=series&id={TvdbId}; pass "" for non-series
// events (Health / ApplicationUpdate).
func (d *Discord) baseEmbed(seriesURL string) Embed {
	author := d.settings.Author
	if author == "" {
		author = d.instanceName
	}
	return Embed{
		Author:    &Author{Name: author, IconURL: authorIconURL},
		URL:       seriesURL,
		Timestamp: d.now().Format(time.RFC3339Nano),
	}
}

// emit sends a single-Embed Payload through the proxy. Shared
// transport hook for every OnX method.
func (d *Discord) emit(ctx context.Context, embed Embed) error {
	return sendPayload(ctx, d.doer, &d.settings, &Payload{
		Username:  d.settings.Username,
		AvatarURL: d.settings.Avatar,
		Embeds:    []Embed{embed},
	})
}

// OnGrab mirrors C# `OnGrab` at `Discord.cs:34-150`. Rich field-
// rendering (Overview/Rating/Genres/Quality/Group/Size/Release/
// Links/CustomFormats/Indexer) deferred — see the Discord struct
// scope-of-port note.
func (d *Discord) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	embed := d.baseEmbed(seriesTvdbURL(msg.Series))
	embed.Description = "Episode Grabbed"
	embed.Color = int(ColorStandard)
	embed.Title = titleFromSeries(msg.Series)
	return d.emit(ctx, embed)
}

// OnDownload mirrors C# `OnDownload` at `Discord.cs:152-280` —
// "Episode Imported" for new files, "Episode Imported Upgrade"
// when OldFiles is non-empty (C# `:160-163`). Color flips from
// Standard → Upgrade on the upgrade path (C# `:184`).
func (d *Discord) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	embed := d.baseEmbed(seriesTvdbURL(msg.Series))
	if len(msg.OldFiles) > 0 {
		embed.Description = "Episode Imported Upgrade"
		embed.Color = int(ColorUpgrade)
	} else {
		embed.Description = "Episode Imported"
		embed.Color = int(ColorStandard)
	}
	embed.Title = titleFromSeries(msg.Series)
	return d.emit(ctx, embed)
}

// OnImportComplete mirrors C# `OnImportComplete` at
// `Discord.cs:282-403`.
func (d *Discord) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	embed := d.baseEmbed(seriesTvdbURL(msg.Series))
	embed.Description = "Import Complete"
	embed.Color = int(ColorStandard)
	embed.Title = titleFromSeries(msg.Series)
	return d.emit(ctx, embed)
}

// OnRename mirrors C# `OnRename` at `Discord.cs:405-422`.
func (d *Discord) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	embed := d.baseEmbed("")
	embed.Description = "Series Renamed"
	embed.Color = int(ColorStandard)
	if t := titleFromSeriesAny(msg.Series); t != "" {
		embed.Title = t
	}
	return d.emit(ctx, embed)
}

// OnEpisodeFileDelete mirrors C# `OnEpisodeFileDelete` at
// `Discord.cs:424-441`.
func (d *Discord) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	embed := d.baseEmbed(seriesTvdbURL(msg.Series))
	embed.Description = "Episode Deleted"
	embed.Color = int(ColorDanger)
	embed.Title = titleFromSeries(msg.Series)
	embed.Fields = []Field{{Name: "Reason", Value: msg.Reason.String()}}
	return d.emit(ctx, embed)
}

// OnSeriesAdd mirrors C# `OnSeriesAdd` at `Discord.cs:443-460`.
func (d *Discord) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	embed := d.baseEmbed(seriesTvdbURL(msg.Series))
	embed.Description = "Series Added"
	embed.Color = int(ColorStandard)
	embed.Title = titleFromSeries(msg.Series)
	return d.emit(ctx, embed)
}

// OnSeriesDelete mirrors C# `OnSeriesDelete` at
// `Discord.cs:462-489`.
func (d *Discord) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	embed := d.baseEmbed(seriesTvdbURL(msg.Series))
	embed.Description = "Series Deleted"
	embed.Color = int(ColorDanger)
	embed.Title = titleFromSeries(msg.Series)
	if msg.DeletedFilesMessage != "" {
		embed.Fields = []Field{{Name: "Files Deleted", Value: msg.DeletedFilesMessage}}
	}
	return d.emit(ctx, embed)
}

// OnHealthIssue mirrors C# `OnHealthIssue` at `Discord.cs:491-520`.
// Color maps Type → DiscordColors: Warning→Warning, Error→Danger,
// Notice/Ok→Warning fallback. C# uses the same mapping at `:506-509`.
func (d *Discord) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	embed := d.baseEmbed("")
	embed.Description = "Health Check Failure"
	embed.Title = msg.Check.Message
	embed.Color = healthColor(msg.Check.Type)
	if msg.Check.Source != "" {
		embed.Fields = append(embed.Fields, Field{Name: "Type", Value: msg.Check.Source})
	}
	if msg.Check.WikiURL != "" {
		embed.Fields = append(embed.Fields, Field{Name: "Wiki URL", Value: msg.Check.WikiURL})
	}
	return d.emit(ctx, embed)
}

// OnHealthRestored mirrors C# `OnHealthRestored` at
// `Discord.cs:522-551`. Description is "Health Check Restored"
// and the color is always Success.
func (d *Discord) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	embed := d.baseEmbed("")
	embed.Description = "Health Check Restored"
	embed.Title = msg.Check.Message
	embed.Color = int(ColorSuccess)
	if msg.Check.Source != "" {
		embed.Fields = append(embed.Fields, Field{Name: "Type", Value: msg.Check.Source})
	}
	return d.emit(ctx, embed)
}

// OnApplicationUpdate mirrors C# `OnApplicationUpdate` at
// `Discord.cs:553-580`.
func (d *Discord) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	embed := d.baseEmbed("")
	embed.Description = "Application Updated"
	embed.Color = int(ColorStandard)
	if msg.Message != "" {
		embed.Title = msg.Message
	}
	if msg.PreviousVersion != "" {
		embed.Fields = append(embed.Fields, Field{Name: "Previous Version", Value: msg.PreviousVersion, Inline: true})
	}
	if msg.NewVersion != "" {
		embed.Fields = append(embed.Fields, Field{Name: "New Version", Value: msg.NewVersion, Inline: true})
	}
	return d.emit(ctx, embed)
}

// OnManualInteractionRequired mirrors C# at `Discord.cs:582-720`.
// Field-rendering deferred — see scope note.
func (d *Discord) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	embed := d.baseEmbed(seriesTvdbURL(msg.Series))
	embed.Description = "Manual Interaction Required"
	embed.Color = int(ColorWarning)
	embed.Title = titleFromSeries(msg.Series)
	return d.emit(ctx, embed)
}

// ProcessQueue is a no-op — Discord has no library-refresh
// coalescing buffer.
func (d *Discord) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test payload. Mirrors C# `Test` at
// `Discord.cs:722-737` — the C# code builds a `Payload` with a
// single embed (Description=connection-test message, Color=Success)
// and POSTs it.
func (d *Discord) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	embed := d.baseEmbed("")
	embed.Description = "Discord - Test Notification"
	embed.Color = int(ColorSuccess)
	if err := d.emit(ctx, embed); err != nil {
		return []notifications.TestFailure{{
			PropertyName: "WebHookUrl",
			ErrorMessage: fmt.Sprintf("Unable to send test message: %s", err.Error()),
		}}, nil
	}
	return nil, nil
}

// healthColor maps a [healthcheck.Result] to the matching
// [Color]. Mirrors the C# switch arm at `Discord.cs:506-509`.
func healthColor(t healthcheck.Result) int {
	switch t {
	case healthcheck.ResultError:
		return int(ColorDanger)
	case healthcheck.ResultWarning, healthcheck.ResultNotice:
		return int(ColorWarning)
	case healthcheck.ResultOK:
		return int(ColorSuccess)
	default:
		return int(ColorWarning)
	}
}

// titleFromSeries returns the Title field from a *series.Series.
// Returns "" when the series is nil.
func titleFromSeries(s *series.Series) string {
	if s == nil {
		return ""
	}
	return s.Title
}

// titleFromSeriesAny accepts the RenameMessage.Series field which
// is typed as `any` (see notifications/message.go:RenameMessage)
// and reaches the Title field via type assertion to *series.Series.
func titleFromSeriesAny(s any) string {
	if s == nil {
		return ""
	}
	if v, ok := s.(*series.Series); ok {
		return v.Title
	}
	return ""
}

// seriesTvdbURL builds the thetvdb.com URL the C# emitters use for
// the Embed.URL field. Returns "" when series is nil or TvdbID is
// zero. Mirrors C# `Discord.cs:46` —
// `http://thetvdb.com/?tab=series&id={TvdbId}`.
func seriesTvdbURL(s *series.Series) string {
	if s == nil || s.TvdbID == 0 {
		return ""
	}
	return fmt.Sprintf("http://thetvdb.com/?tab=series&id=%d", s.TvdbID)
}

// SupportsOnGrab reports OnGrab capability. The C# Discord
// overrides every OnX hook, so every Supports* flag is true. See
// the [webhook.Webhook.SupportsOnGrab] precedent at
// `internal/notifications/webhook/webhook.go` for the rationale.
func (d *Discord) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnRename() bool { return true }

// SupportsOnSeriesAdd — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Discord.SupportsOnGrab].
func (d *Discord) SupportsOnManualInteractionRequired() bool { return true }

// init registers the Discord constructor into the package-global
// [notifications.Default] registry — same idiom as the webhook
// package precedent at
// `internal/notifications/webhook/webhook.go:init`.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
