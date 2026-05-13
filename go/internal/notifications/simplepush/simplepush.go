package simplepush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Simplepush.cs:7` class name.
const Implementation = "Simplepush"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `SimplepushSettings.cs:15` class name.
const ConfigContract = "SimplepushSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// Simplepush uses the PLAIN (non-branded) variants for every event.
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

// Simplepush ports the C# `Simplepush` notification provider at
// `Notifications/Simplepush/Simplepush.cs:7-78`.
type Simplepush struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Simplepush)(nil)

// New constructs a *Simplepush from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("simplepush: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("simplepush: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Simplepush{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:16`.
func (s *Simplepush) Name() string { return "Simplepush" }

// Link returns the docs URL. C# `:17`.
func (s *Simplepush) Link() string { return "https://simplepush.io/" }

// Definition returns the persisted row.
func (s *Simplepush) Definition() *notifications.Notification { return s.def }

// OnGrab mirrors C# `:19-22`.
func (s *Simplepush) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleEpisodeGrabbed, msg.Message)
}

// OnDownload mirrors C# `:24-27`.
func (s *Simplepush) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleEpisodeDownloaded, msg.Message)
}

// OnImportComplete mirrors C# `:29-32`.
func (s *Simplepush) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleImportComplete, msg.Message)
}

// OnRename is a no-op — C# Simplepush doesn't override OnRename.
func (s *Simplepush) OnRename(_ context.Context, _ notifications.RenameMessage) error {
	return nil
}

// OnEpisodeFileDelete mirrors C# `:34-37`.
func (s *Simplepush) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleEpisodeDeleted, msg.Message)
}

// OnSeriesAdd mirrors C# `:39-42`.
func (s *Simplepush) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleSeriesAdded, msg.Message)
}

// OnSeriesDelete mirrors C# `:44-47`.
func (s *Simplepush) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleSeriesDeleted, msg.Message)
}

// OnHealthIssue mirrors C# `:49-52`.
func (s *Simplepush) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleHealthIssue, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:54-57`.
func (s *Simplepush) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, s.doer, &s.settings, titleHealthRestored, body)
}

// OnApplicationUpdate mirrors C# `:59-62`.
func (s *Simplepush) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleApplicationUpdate, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:64-67`.
func (s *Simplepush) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleManualInteractionRequired, msg.Message)
}

// ProcessQueue is a no-op — Simplepush has no queue.
func (s *Simplepush) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `Simplepush.Test`
// + `SimplepushProxy.Test` at `SimplepushProxy.cs:44-60`.
func (s *Simplepush) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := s.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	if err := sendNotification(ctx, s.doer, &s.settings, "Test Notification", "This is a test message from Sonarr"); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "ApiKey",
			ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Simplepush overrides
// every OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (s *Simplepush) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Simplepush.SupportsOnGrab]. Returns false.
func (s *Simplepush) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Simplepush.SupportsOnGrab].
func (s *Simplepush) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `Simplepush` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (s *Simplepush) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the Simplepush constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
