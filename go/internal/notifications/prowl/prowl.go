package prowl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Prowl.cs:7` class name.
const Implementation = "Prowl"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `ProwlSettings.cs:15` class name.
const ConfigContract = "ProwlSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// Prowl uses the PLAIN (non-branded) variants for every event.
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

// Prowl ports the C# `Prowl` notification provider at
// `Notifications/Prowl/Prowl.cs:7-78`.
type Prowl struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Prowl)(nil)

// New constructs a *Prowl from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("prowl: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("prowl: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Prowl{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:17`.
func (p *Prowl) Name() string { return "Prowl" }

// Link returns the docs URL. C# `:16`.
func (p *Prowl) Link() string { return "https://www.prowlapp.com/" }

// Definition returns the persisted row.
func (p *Prowl) Definition() *notifications.Notification { return p.def }

// OnGrab mirrors C# `:19-22`.
func (p *Prowl) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleEpisodeGrabbed, msg.Message)
}

// OnDownload mirrors C# `:24-27`.
func (p *Prowl) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleEpisodeDownloaded, msg.Message)
}

// OnImportComplete mirrors C# `:29-32`.
func (p *Prowl) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleImportComplete, msg.Message)
}

// OnRename is a no-op — C# Prowl doesn't override OnRename.
func (p *Prowl) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:34-37`.
func (p *Prowl) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleEpisodeDeleted, msg.Message)
}

// OnSeriesAdd mirrors C# `:39-42`.
func (p *Prowl) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleSeriesAdded, msg.Message)
}

// OnSeriesDelete mirrors C# `:44-47`.
func (p *Prowl) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleSeriesDeleted, msg.Message)
}

// OnHealthIssue mirrors C# `:49-52`.
func (p *Prowl) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleHealthIssue, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:54-57`.
func (p *Prowl) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, p.doer, &p.settings, titleHealthRestored, body)
}

// OnApplicationUpdate mirrors C# `:59-62`.
func (p *Prowl) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleApplicationUpdate, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:64-67`.
func (p *Prowl) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleManualInteractionRequired, msg.Message)
}

// ProcessQueue is a no-op — Prowl has no queue.
func (p *Prowl) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `ProwlProxy.Test`
// at `ProwlProxy.cs:64-79`.
func (p *Prowl) Test(ctx context.Context) ([]notifications.TestFailure, error) {
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

// SupportsOnGrab reports OnGrab capability. C# Prowl overrides every
// OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (p *Prowl) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Prowl.SupportsOnGrab]. Returns false.
func (p *Prowl) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Prowl.SupportsOnGrab].
func (p *Prowl) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `Prowl` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (p *Prowl) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the Prowl constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
