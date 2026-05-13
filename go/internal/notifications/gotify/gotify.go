package gotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/series"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Gotify.cs:14` class name.
const Implementation = "Gotify"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `GotifySettings.cs:44` class name.
const ConfigContract = "GotifySettings"

// sonarrImageURL is the per-app logo URL the C# Test() uses when
// IncludeSeriesPoster is enabled. Mirrors C# `Gotify.cs:16`.
const sonarrImageURL = "https://raw.githubusercontent.com/Sonarr/Sonarr/develop/Logo/128.png"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
const (
	titleEpisodeGrabbed            = "Episode Grabbed"
	titleEpisodeDownloaded         = "Episode Downloaded"
	titleImportComplete            = "Import Complete"
	titleEpisodeDeleted            = "Episode Deleted"
	titleSeriesAdded               = "Series Added"
	titleSeriesDeleted             = "Series Deleted"
	titleHealthIssue               = "Health Check Failure"
	titleHealthRestored            = "Health Check Restored"
	titleApplicationUpdate         = "Application Updated"
	titleManualInteractionRequired = "Manual Interaction"
)

// Gotify ports the C# `Gotify` notification provider at
// `Notifications/Gotify/Gotify.cs:14-208`.
//
// Goroutine-safety: httpDoer is shared across concurrent OnX
// calls. settings is captured by value at construction time.
type Gotify struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Gotify)(nil)

// New constructs a *Gotify from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("gotify: nil definition")
	}
	// Match C# `GotifySettings()` constructor defaults at
	// `GotifySettings.cs:48-53`: Priority=5 (Normal),
	// PreferredMetadataLink=Tvdb. Without these, a zero-value
	// PreferredMetadataLink would be IMDb=0 and tests against
	// fresh-settings shapes would diverge from C#.
	s := Settings{
		Priority:              int(PriorityNormal),
		PreferredMetadataLink: int(notifications.MetadataLinkTvdb),
	}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("gotify: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Gotify{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:29`.
func (g *Gotify) Name() string { return "Gotify" }

// Link returns the docs URL. C# `:30`.
func (g *Gotify) Link() string { return "https://gotify.net/" }

// Definition returns the persisted row.
func (g *Gotify) Definition() *notifications.Notification { return g.def }

// send mirrors C# `Gotify.SendNotification` at `Gotify.cs:131-207`.
// Builds the markdown/plain body from message + per-series metadata
// links, sets per-link click-through URL on the preferred link, and
// POSTs via the proxy.
//
// IncludeSeriesPoster is deferred — see package doc.
func (g *Gotify) send(ctx context.Context, title, message string, ser *series.Series) error {
	var sb strings.Builder
	sb.WriteString(message)
	sb.WriteString("\n")
	isMarkdown := false
	payload := &Payload{
		Title:    title,
		Priority: g.settings.Priority,
	}
	if ser != nil && len(g.settings.MetadataLinks) > 0 {
		links := notifications.GenerateMetadataLinks(ser, g.settings.MetadataLinks)
		if len(links) > 0 {
			isMarkdown = true
			sb.WriteString("\n")
			for _, l := range links {
				fmt.Fprintf(&sb, "[%s](%s)\n", l.Label, l.URL)
				if int(l.Type) == g.settings.PreferredMetadataLink {
					payload.setClickURL(l.URL)
				}
			}
		}
	}
	payload.Message = sb.String()
	payload.setContentType(isMarkdown)
	return sendNotification(ctx, g.doer, &g.settings, payload)
}

// OnGrab mirrors C# `:32-35`.
func (g *Gotify) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return g.send(ctx, titleEpisodeGrabbed, msg.Message, msg.Series)
}

// OnDownload mirrors C# `:37-40`.
func (g *Gotify) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return g.send(ctx, titleEpisodeDownloaded, msg.Message, msg.Series)
}

// OnImportComplete mirrors C# `:42-45`.
func (g *Gotify) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return g.send(ctx, titleImportComplete, msg.Message, msg.Series)
}

// OnRename is a no-op — C# Gotify doesn't override OnRename.
func (g *Gotify) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:47-50`.
func (g *Gotify) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return g.send(ctx, titleEpisodeDeleted, msg.Message, msg.Series)
}

// OnSeriesAdd mirrors C# `:52-55`.
func (g *Gotify) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return g.send(ctx, titleSeriesAdded, msg.Message, msg.Series)
}

// OnSeriesDelete mirrors C# `:57-60`.
func (g *Gotify) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return g.send(ctx, titleSeriesDeleted, msg.Message, msg.Series)
}

// OnHealthIssue mirrors C# `:62-65`. No series — metadata links skipped.
func (g *Gotify) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return g.send(ctx, titleHealthIssue, msg.Check.Message, nil)
}

// OnHealthRestored mirrors C# `:67-70`.
func (g *Gotify) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return g.send(ctx, titleHealthRestored, body, nil)
}

// OnApplicationUpdate mirrors C# `:72-75`.
func (g *Gotify) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return g.send(ctx, titleApplicationUpdate, msg.Message, nil)
}

// OnManualInteractionRequired mirrors C# `:77-80`.
func (g *Gotify) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return g.send(ctx, titleManualInteractionRequired, msg.Message, msg.Series)
}

// ProcessQueue is a no-op — Gotify has no queue.
func (g *Gotify) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `Gotify.Test` at
// `Gotify.cs:82-129`.
//
// Body composition:
//   - "This is a test message from Sonarr"
//   - When IncludeSeriesPoster: appends "![]({sonarrImageURL})" +
//     sets payload BigImageURL = sonarrImageURL.
//   - When MetadataLinks non-empty: appends a "[Sonarr.tv](...)"
//     link and sets payload Click = "https://sonarr.tv".
func (g *Gotify) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := g.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	var sb strings.Builder
	sb.WriteString("This is a test message from Sonarr\n")
	isMarkdown := false
	payload := &Payload{
		Title:    "Test Notification",
		Priority: g.settings.Priority,
	}
	if g.settings.IncludeSeriesPoster {
		isMarkdown = true
		fmt.Fprintf(&sb, "\r![](%s)\n", sonarrImageURL)
		payload.setImage(sonarrImageURL)
	}
	if len(g.settings.MetadataLinks) > 0 {
		isMarkdown = true
		sb.WriteString("\n")
		sb.WriteString("[Sonarr.tv](https://sonarr.tv)\n")
		payload.setClickURL("https://sonarr.tv")
	}
	payload.Message = sb.String()
	payload.setContentType(isMarkdown)
	if err := sendNotification(ctx, g.doer, &g.settings, payload); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "",
			ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Gotify overrides every
// OnX hook EXCEPT OnRename (the base NotificationBase.OnRename throws
// NotImplemented); the Go port reflects this via SupportsOnRename
// returning false.
func (g *Gotify) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Gotify.SupportsOnGrab]. Returns false
// to reflect the C# Gotify non-override.
func (g *Gotify) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Gotify.SupportsOnGrab].
func (g *Gotify) SupportsOnManualInteractionRequired() bool { return true }

// init registers the Gotify constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
