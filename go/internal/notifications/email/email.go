package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Email.cs:15` class name.
const Implementation = "Email"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `EmailSettings.cs:28` class name.
const ConfigContract = "EmailSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// Email uses BRANDED titles for every event EXCEPT
// OnImportComplete (PLAIN per C# `:50`).
const (
	titleEpisodeGrabbedBranded            = "Sonarr - Episode Grabbed"
	titleEpisodeDownloadedBranded         = "Sonarr - Episode Downloaded"
	titleImportComplete                   = "Import Complete"
	titleEpisodeDeletedBranded            = "Sonarr - Episode Deleted"
	titleSeriesAddedBranded               = "Sonarr - Series Added"
	titleSeriesDeletedBranded             = "Sonarr - Series Deleted"
	titleHealthIssueBranded               = "Sonarr - Health Check Failure"
	titleHealthRestoredBranded            = "Sonarr - Health Check Restored"
	titleApplicationUpdateBranded         = "Sonarr - Application Updated"
	titleManualInteractionRequiredBranded = "Sonarr - Manual Interaction"
)

// Email ports the C# `Email` notification provider at
// `Notifications/Email/Email.cs:15-208`.
type Email struct {
	def      *notifications.Notification
	settings Settings
}

// Compile-time guard.
var _ notifications.Provider = (*Email)(nil)

// New constructs an *Email from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, _ notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("email: nil definition")
	}
	// Match C# `EmailSettings()` constructor default at
	// `EmailSettings.cs:32-39`: Port=587.
	s := Settings{Port: 587}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("email: unmarshal settings: %w", err)
		}
	}
	return &Email{def: def, settings: s}, nil
}

// Name returns the provider display label. C# `:21`.
func (e *Email) Name() string { return "Email" }

// Link returns the docs URL. C# `:30` — Email has no link, returns
// empty string.
func (e *Email) Link() string { return "" }

// Definition returns the persisted row.
func (e *Email) Definition() *notifications.Notification { return e.def }

// OnGrab mirrors C# `:32-37`. Body suffixed with " sent to queue."
// per C# `:34`.
func (e *Email) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendEmail(ctx, &e.settings, titleEpisodeGrabbedBranded, msg.Message+" sent to queue.")
}

// OnDownload mirrors C# `:39-44`. Body suffixed with " Downloaded
// and sorted." per C# `:41`.
func (e *Email) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendEmail(ctx, &e.settings, titleEpisodeDownloadedBranded, msg.Message+" Downloaded and sorted.")
}

// OnImportComplete mirrors C# `:46-51`. Body wrapped with "All
// expected episode files in {msg} downloaded and sorted." per C# `:48`.
func (e *Email) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendEmail(ctx, &e.settings, titleImportComplete, "All expected episode files in "+msg.Message+" downloaded and sorted.")
}

// OnRename is a no-op — C# Email doesn't override OnRename.
func (e *Email) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:53-58`. Body suffixed with
// " deleted." per C# `:55`.
func (e *Email) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendEmail(ctx, &e.settings, titleEpisodeDeletedBranded, msg.Message+" deleted.")
}

// OnSeriesAdd mirrors C# `:60-65`.
func (e *Email) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendEmail(ctx, &e.settings, titleSeriesAddedBranded, msg.Message)
}

// OnSeriesDelete mirrors C# `:67-72`.
func (e *Email) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendEmail(ctx, &e.settings, titleSeriesDeletedBranded, msg.Message)
}

// OnHealthIssue mirrors C# `:74-77`.
func (e *Email) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendEmail(ctx, &e.settings, titleHealthIssueBranded, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:79-82`.
func (e *Email) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendEmail(ctx, &e.settings, titleHealthRestoredBranded, body)
}

// OnApplicationUpdate mirrors C# `:84-89`.
func (e *Email) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendEmail(ctx, &e.settings, titleApplicationUpdateBranded, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:91-96`. Body suffixed
// with " requires manual interaction." per C# `:93`.
func (e *Email) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendEmail(ctx, &e.settings, titleManualInteractionRequiredBranded, msg.Message+" requires manual interaction.")
}

// ProcessQueue is a no-op — Email has no queue.
func (e *Email) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test email. Mirrors C# `Email.Test` at
// `:98-104`. Failures surface with "Server" PropertyName per C# `:190`.
func (e *Email) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := e.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	body := "Success! You have properly configured your email notification settings"
	if err := sendEmail(ctx, &e.settings, "Sonarr - Test Notification", body); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "Server",
			ErrorMessage:        fmt.Sprintf("Unable to send test email: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Email overrides every
// OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (e *Email) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Email.SupportsOnGrab]. Returns false.
func (e *Email) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Email.SupportsOnGrab].
func (e *Email) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `Email` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (e *Email) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the Email constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
