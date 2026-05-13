package mailgun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `MailGun.cs:9` class name (C# spelling is `MailGun`).
const Implementation = "MailGun"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `MailgunSettings.cs:19` class name (C# spelling is
// `MailgunSettings`).
const ConfigContract = "MailgunSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// Mailgun uses PLAIN titles for every event EXCEPT health-restored
// (C# `:68` uses HEALTH_RESTORED_TITLE_BRANDED).
const (
	titleEpisodeGrabbed            = "Episode Grabbed"
	titleEpisodeDownloaded         = "Episode Downloaded"
	titleImportComplete            = "Import Complete"
	titleEpisodeDeleted            = "Episode Deleted"
	titleSeriesAdded               = "Series Added"
	titleSeriesDeleted             = "Series Deleted"
	titleHealthIssue               = "Health Check Failure"
	titleHealthRestoredBranded     = "Sonarr - Health Check Restored"
	titleApplicationUpdate         = "Application Updated"
	titleManualInteractionRequired = "Manual Interaction"
)

// Mailgun ports the C# `MailGun` notification provider at
// `Notifications/Mailgun/Mailgun.cs:9-102`.
type Mailgun struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Mailgun)(nil)

// New constructs a *Mailgun from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("mailgun: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("mailgun: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Mailgun{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:22`.
func (m *Mailgun) Name() string { return "Mailgun" }

// Link returns the docs URL. C# `:23`.
func (m *Mailgun) Link() string { return "https://mailgun.com" }

// Definition returns the persisted row.
func (m *Mailgun) Definition() *notifications.Notification { return m.def }

// OnGrab mirrors C# `:25-28`.
func (m *Mailgun) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, m.doer, &m.settings, titleEpisodeGrabbed, msg.Message)
}

// OnDownload mirrors C# `:30-33`.
func (m *Mailgun) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, m.doer, &m.settings, titleEpisodeDownloaded, msg.Message)
}

// OnImportComplete mirrors C# `:35-38`.
func (m *Mailgun) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, m.doer, &m.settings, titleImportComplete, msg.Message)
}

// OnRename is a no-op — C# Mailgun doesn't override OnRename.
func (m *Mailgun) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:40-45`. Body is composed as
// "{deleteMessage.Message} deleted." per C# `:42`.
func (m *Mailgun) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, m.doer, &m.settings, titleEpisodeDeleted, msg.Message+" deleted.")
}

// OnSeriesAdd mirrors C# `:47-52`.
func (m *Mailgun) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, m.doer, &m.settings, titleSeriesAdded, msg.Message)
}

// OnSeriesDelete mirrors C# `:54-59`.
func (m *Mailgun) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, m.doer, &m.settings, titleSeriesDeleted, msg.Message)
}

// OnHealthIssue mirrors C# `:61-64`.
func (m *Mailgun) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, m.doer, &m.settings, titleHealthIssue, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:66-69`. Uses the BRANDED title
// variant.
func (m *Mailgun) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, m.doer, &m.settings, titleHealthRestoredBranded, body)
}

// OnApplicationUpdate mirrors C# `:71-74`.
func (m *Mailgun) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, m.doer, &m.settings, titleApplicationUpdate, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:76-79`.
func (m *Mailgun) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, m.doer, &m.settings, titleManualInteractionRequired, msg.Message)
}

// ProcessQueue is a no-op — Mailgun has no queue.
func (m *Mailgun) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test email. Mirrors C# `MailGun.Test` at
// `Mailgun.cs:81-100`.
func (m *Mailgun) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := m.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	if err := sendNotification(ctx, m.doer, &m.settings, "Test Notification", "This is a test message from Sonarr, though Mailgun."); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "",
			ErrorMessage:        fmt.Sprintf("Unable to send test message though Mailgun: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Mailgun overrides
// every OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (m *Mailgun) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Mailgun.SupportsOnGrab]. Returns false.
func (m *Mailgun) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Mailgun.SupportsOnGrab].
func (m *Mailgun) SupportsOnManualInteractionRequired() bool { return true }

// init registers the Mailgun constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
