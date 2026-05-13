package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/series"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Telegram.cs:9` class name.
const Implementation = "Telegram"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `TelegramSettings.cs:46` class name.
const ConfigContract = "TelegramSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`
// (the branded "Sonarr - " variant is built at runtime by
// applyBrandAndInstance).
const (
	titleEpisodeGrabbed            = "Episode Grabbed"
	titleEpisodeDownloaded         = "Episode Downloaded"
	titleEpisodeDeleted            = "Episode Deleted"
	titleSeriesAdded               = "Series Added"
	titleSeriesDeleted             = "Series Deleted"
	titleHealthIssue               = "Health Check Failure"
	titleHealthRestored            = "Health Check Restored"
	titleApplicationUpdate         = "Application Updated"
	titleManualInteractionRequired = "Manual Interaction"
)

// brandedPrefix mirrors C# `NotificationBase.cs:13` — every "branded"
// title constant uses "Sonarr - {Title}".
const brandedPrefix = "Sonarr - "

// Telegram ports the C# `Telegram` notification provider at
// `Notifications/Telegram/Telegram.cs:9-126`.
//
// Goroutine-safety: httpDoer is shared across concurrent OnX calls.
// settings is captured by value at construction time.
type Telegram struct {
	def          *notifications.Notification
	settings     Settings
	doer         httpDoer
	instanceName string
}

// Compile-time guard.
var _ notifications.Provider = (*Telegram)(nil)

// instanceNamer is the narrow interface a Dependencies.ConfigProvider
// must satisfy to surface IncludeInstanceNameInTitle. Mirrors the C#
// `IConfigFileProvider.InstanceName` dep at `Telegram.cs:14`.
type instanceNamer interface {
	InstanceName() string
}

// New constructs a *Telegram from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("telegram: nil definition")
	}
	// Match C# `TelegramSettings()` constructor defaults at
	// `TelegramSettings.cs:41-44`: LinkPreview=None, MetadataLinks=[].
	// Without this prelude a zero-value LinkPreview would be IMDb=0
	// and validation would require IMDb to be in MetadataLinks, which
	// doesn't match the C# fresh-settings shape.
	s := Settings{LinkPreview: int(LinkPreviewNone)}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("telegram: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	var instance string
	if n, ok := deps.ConfigProvider.(instanceNamer); ok {
		instance = n.InstanceName()
	}
	return &Telegram{def: def, settings: s, doer: doer, instanceName: instance}, nil
}

// Name returns the provider display label. C# `:20`.
func (t *Telegram) Name() string { return "Telegram" }

// Link returns the docs URL. C# `:21`.
func (t *Telegram) Link() string { return "https://telegram.org/" }

// Definition returns the persisted row.
func (t *Telegram) Definition() *notifications.Notification { return t.def }

// applyBrandAndInstance mirrors the per-event header at C# `:27-28` /
// every OnX hook in `Telegram.cs`. IncludeAppNameInTitle prepends
// "Sonarr - "; IncludeInstanceNameInTitle appends " - {InstanceName}".
func (t *Telegram) applyBrandAndInstance(title string) string {
	out := title
	if t.settings.IncludeAppNameInTitle {
		out = brandedPrefix + out
	}
	if t.settings.IncludeInstanceNameInTitle && t.instanceName != "" {
		out = out + " - " + t.instanceName
	}
	return out
}

// links builds the metadata link list for the given series — empty
// when ser is nil or no requested links match. Mirrors C# `:121-124`.
func (t *Telegram) links(ser *series.Series) []notifications.MetadataLink {
	return notifications.GenerateMetadataLinks(ser, t.settings.MetadataLinks)
}

// OnGrab mirrors C# `:25-32`.
func (t *Telegram) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleEpisodeGrabbed), msg.Message, t.links(msg.Series))
}

// OnDownload mirrors C# `:34-41`.
func (t *Telegram) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleEpisodeDownloaded), msg.Message, t.links(msg.Series))
}

// OnImportComplete mirrors C# `:43-50`. The C# code reuses the
// EPISODE_DOWNLOADED_TITLE constant rather than introducing a
// separate "Import Complete" title.
func (t *Telegram) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleEpisodeDownloaded), msg.Message, t.links(msg.Series))
}

// OnRename is a no-op — C# Telegram doesn't override OnRename.
func (t *Telegram) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:52-59`.
func (t *Telegram) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleEpisodeDeleted), msg.Message, t.links(msg.Series))
}

// OnSeriesAdd mirrors C# `:61-68`.
func (t *Telegram) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleSeriesAdded), msg.Message, t.links(msg.Series))
}

// OnSeriesDelete mirrors C# `:70-77`.
func (t *Telegram) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleSeriesDeleted), msg.Message, t.links(msg.Series))
}

// OnHealthIssue mirrors C# `:79-85`. No metadata links — health
// checks are not series-scoped.
func (t *Telegram) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleHealthIssue), msg.Check.Message, nil)
}

// OnHealthRestored mirrors C# `:87-93`.
func (t *Telegram) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleHealthRestored), body, nil)
}

// OnApplicationUpdate mirrors C# `:95-101`.
func (t *Telegram) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleApplicationUpdate), msg.Message, nil)
}

// OnManualInteractionRequired mirrors C# `:103-110`.
func (t *Telegram) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, t.doer, &t.settings, t.applyBrandAndInstance(titleManualInteractionRequired), msg.Message, t.links(msg.Series))
}

// ProcessQueue is a no-op — Telegram has no queue to drain.
func (t *Telegram) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `Telegram.Test` +
// `TelegramProxy.Test` at `TelegramProxy.cs:72-119`.
//
// The test message always includes a single "Sonarr.tv" link — the
// C# code constructs `new NotificationMetadataLink(null, "Sonarr.tv",
// "https://sonarr.tv")`. Because the Type is null, no LinkPreview
// match is possible — the preview URL falls back to empty.
func (t *Telegram) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := t.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	title := t.applyBrandAndInstance("Test Notification")
	body := "This is a test message from Sonarr"
	links := []notifications.MetadataLink{{Label: "Sonarr.tv", URL: "https://sonarr.tv"}}
	if err := sendNotification(ctx, t.doer, &t.settings, title, body, links); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "BotToken",
			ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Telegram overrides
// every OnX hook EXCEPT OnRename (the base NotificationBase.OnRename
// throws NotImplemented); the Go port reflects this via
// SupportsOnRename returning false so the orchestrator can gate
// correctly.
func (t *Telegram) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Telegram.SupportsOnGrab]. Returns false
// to reflect the C# Telegram non-override.
func (t *Telegram) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Telegram.SupportsOnGrab].
func (t *Telegram) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `Telegram` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (t *Telegram) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the Telegram constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
