package pushover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Pushover.cs:7` class name.
const Implementation = "Pushover"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `PushoverSettings.cs:20` class name.
const ConfigContract = "PushoverSettings"

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

// Pushover ports the C# `Pushover` notification provider at
// `Notifications/Pushover/Pushover.cs:7-78`.
//
// Goroutine-safety: httpDoer is shared across concurrent OnX
// calls. settings is captured by value at construction time.
type Pushover struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Pushover)(nil)

// New constructs a *Pushover from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("pushover: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("pushover: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Pushover{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:16`.
func (p *Pushover) Name() string { return "Pushover" }

// Link returns the docs URL. C# `:17`.
func (p *Pushover) Link() string { return "https://pushover.net/" }

// Definition returns the persisted row.
func (p *Pushover) Definition() *notifications.Notification { return p.def }

// OnGrab mirrors C# `:19-22`.
func (p *Pushover) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleEpisodeGrabbed, msg.Message)
}

// OnDownload mirrors C# `:24-27`.
func (p *Pushover) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleEpisodeDownloaded, msg.Message)
}

// OnImportComplete mirrors C# `:29-32`.
func (p *Pushover) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleImportComplete, msg.Message)
}

// OnRename is a no-op — C# Pushover doesn't override it (the C#
// base NotificationBase.OnRename throws NotImplemented). The Go
// port returns nil since the orchestrator's per-provider gate
// checks SupportsOnRename() before dispatching.
func (p *Pushover) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:34-37`.
func (p *Pushover) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleEpisodeDeleted, msg.Message)
}

// OnSeriesAdd mirrors C# `:39-42`.
func (p *Pushover) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleSeriesAdded, msg.Message)
}

// OnSeriesDelete mirrors C# `:44-47`.
func (p *Pushover) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleSeriesDeleted, msg.Message)
}

// OnHealthIssue mirrors C# `:49-52`.
func (p *Pushover) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleHealthIssue, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:54-57`.
func (p *Pushover) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, p.doer, &p.settings, titleHealthRestored, body)
}

// OnApplicationUpdate mirrors C# `:59-62`.
func (p *Pushover) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleApplicationUpdate, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:64-67`.
func (p *Pushover) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleManualInteractionRequired, msg.Message)
}

// ProcessQueue is a no-op — Pushover doesn't have a queue.
func (p *Pushover) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `Test()` + the
// proxy.Test() at PushoverProxy.cs:64-80.
func (p *Pushover) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := p.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	if err := sendNotification(ctx, p.doer, &p.settings, "Test Notification", "This is a test message from Sonarr"); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "ApiKey",
			ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. Pushover does NOT
// support OnRename (C# doesn't override that method — the base
// NotificationBase throws NotImplemented); the Go port reflects
// this via SupportsOnRename returning false so the orchestrator
// can gate correctly. Every other OnX flag is true.
func (p *Pushover) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Pushover.SupportsOnGrab]. Returns false
// to reflect the C# Pushover non-override.
func (p *Pushover) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Pushover.SupportsOnGrab].
func (p *Pushover) SupportsOnManualInteractionRequired() bool { return true }

// init registers the Pushover constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
