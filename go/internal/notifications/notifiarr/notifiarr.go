package notifiarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/webhook"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Notifiarr.cs:15` class name.
const Implementation = "Notifiarr"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `NotifiarrSettings.cs:15` class name.
const ConfigContract = "NotifiarrSettings"

// Notifiarr ports the C# `Notifiarr` notification provider at
// `Notifications/Notifiarr/Notifiarr.cs:15-106`. The C# class extends
// `WebhookBase<NotifiarrSettings>` and the OnX hooks invoke the
// shared BuildXPayload helpers; the Go port reuses
// [webhook.BuildGrabPayload] / etc.
type Notifiarr struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Notifiarr)(nil)

// New constructs a *Notifiarr from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("notifiarr: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("notifiarr: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Notifiarr{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:26`.
func (n *Notifiarr) Name() string { return "Notifiarr" }

// Link returns the docs URL. C# `:25`.
func (n *Notifiarr) Link() string { return "https://notifiarr.com" }

// Definition returns the persisted row.
func (n *Notifiarr) Definition() *notifications.Notification { return n.def }

// OnGrab mirrors C# `:28-31`.
func (n *Notifiarr) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildGrabPayload(msg))
}

// OnDownload mirrors C# `:33-36`.
func (n *Notifiarr) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildDownloadPayload(msg))
}

// OnImportComplete mirrors C# `:38-41`.
func (n *Notifiarr) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildImportCompletePayload(msg))
}

// OnRename mirrors C# `:43-46`.
func (n *Notifiarr) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildRenamePayload(msg))
}

// OnEpisodeFileDelete mirrors C# `:48-51`.
func (n *Notifiarr) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildEpisodeFileDeletePayload(msg))
}

// OnSeriesAdd mirrors C# `:53-56`.
func (n *Notifiarr) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildSeriesAddPayload(msg))
}

// OnSeriesDelete mirrors C# `:58-61`.
func (n *Notifiarr) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildSeriesDeletePayload(msg))
}

// OnHealthIssue mirrors C# `:63-66`.
func (n *Notifiarr) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildHealthPayload(msg))
}

// OnHealthRestored mirrors C# `:68-71`.
func (n *Notifiarr) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildHealthRestoredPayload(msg))
}

// OnApplicationUpdate mirrors C# `:73-76`.
func (n *Notifiarr) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildApplicationUpdatePayload(msg))
}

// OnManualInteractionRequired mirrors C# `:78-81`.
func (n *Notifiarr) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, webhook.BuildManualInteractionPayload(msg))
}

// ProcessQueue is a no-op — Notifiarr has no queue.
func (n *Notifiarr) ProcessQueue(_ context.Context) error { return nil }

// Test sends the BuildTestPayload shape via the proxy. Mirrors C#
// `Notifiarr.Test` + `SendWebhookTest` at `Notifiarr.cs:83-104`.
func (n *Notifiarr) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := n.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	if err := sendNotification(ctx, n.doer, &n.settings, webhook.BuildTestPayload()); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "APIKey",
			ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Notifiarr extends
// WebhookBase and overrides every OnX hook including OnRename —
// every Supports* flag is true.
func (n *Notifiarr) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnRename() bool { return true }

// SupportsOnSeriesAdd — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Notifiarr.SupportsOnGrab].
func (n *Notifiarr) SupportsOnManualInteractionRequired() bool { return true }

// init registers the Notifiarr constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
