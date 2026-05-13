package sendgrid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `SendGrid.cs:9` class name.
const Implementation = "SendGrid"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `SendGridSettings.cs:20` class name.
const ConfigContract = "SendGridSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// SendGrid uses PLAIN titles for every event.
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

// SendGrid ports the C# `SendGrid` notification provider at
// `Notifications/SendGrid/SendGrid.cs:9-95`.
type SendGrid struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*SendGrid)(nil)

// New constructs a *SendGrid from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("sendgrid: nil definition")
	}
	// Match C# `SendGridSettings()` constructor defaults at
	// `SendGridSettings.cs:24-28`: BaseUrl + empty Recipients.
	s := Settings{BaseUrl: defaultBaseURL}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("sendgrid: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &SendGrid{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:22`.
func (s *SendGrid) Name() string { return "SendGrid" }

// Link returns the docs URL. C# `:23`.
func (s *SendGrid) Link() string { return "https://sendgrid.com/" }

// Definition returns the persisted row.
func (s *SendGrid) Definition() *notifications.Notification { return s.def }

// OnGrab mirrors C# `:25-28`.
func (s *SendGrid) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleEpisodeGrabbed, msg.Message)
}

// OnDownload mirrors C# `:30-33`.
func (s *SendGrid) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleEpisodeDownloaded, msg.Message)
}

// OnImportComplete mirrors C# `:35-38`.
func (s *SendGrid) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleImportComplete, msg.Message)
}

// OnRename is a no-op — C# SendGrid doesn't override OnRename.
func (s *SendGrid) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:40-43`.
func (s *SendGrid) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleEpisodeDeleted, msg.Message)
}

// OnSeriesAdd mirrors C# `:45-48`.
func (s *SendGrid) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleSeriesAdded, msg.Message)
}

// OnSeriesDelete mirrors C# `:50-53`.
func (s *SendGrid) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleSeriesDeleted, msg.Message)
}

// OnHealthIssue mirrors C# `:55-58`.
func (s *SendGrid) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleHealthIssue, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:60-63`.
func (s *SendGrid) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, s.doer, &s.settings, titleHealthRestored, body)
}

// OnApplicationUpdate mirrors C# `:65-68`.
func (s *SendGrid) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleApplicationUpdate, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:70-73`.
func (s *SendGrid) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleManualInteractionRequired, msg.Message)
}

// ProcessQueue is a no-op — SendGrid has no queue.
func (s *SendGrid) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test email. Mirrors C# `SendGrid.Test` at
// `SendGrid.cs:75-93`. Failures surface with empty PropertyName per
// C# `:89`.
func (s *SendGrid) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := s.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	if err := sendNotification(ctx, s.doer, &s.settings, "Test Notification", "This is a test message from Sonarr"); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "",
			ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# SendGrid overrides
// every OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (s *SendGrid) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [SendGrid.SupportsOnGrab]. Returns false.
func (s *SendGrid) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [SendGrid.SupportsOnGrab].
func (s *SendGrid) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `SendGrid` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (s *SendGrid) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the SendGrid constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
