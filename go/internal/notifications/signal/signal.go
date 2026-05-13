package signal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Signal.cs:7` class name.
const Implementation = "Signal"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `SignalSettings.cs:18` class name.
const ConfigContract = "SignalSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// Signal uses PLAIN titles for every event.
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

// Signal ports the C# `Signal` notification provider at
// `Notifications/Signal/Signal.cs:7-78`.
type Signal struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Signal)(nil)

// New constructs a *Signal from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("signal: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("signal: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Signal{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:16`.
func (s *Signal) Name() string { return "Signal" }

// Link returns the docs URL. C# `:17`.
func (s *Signal) Link() string { return "https://signal.org/" }

// Definition returns the persisted row.
func (s *Signal) Definition() *notifications.Notification { return s.def }

// OnGrab mirrors C# `:19-22`.
func (s *Signal) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleEpisodeGrabbed, msg.Message)
}

// OnDownload mirrors C# `:24-27`.
func (s *Signal) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleEpisodeDownloaded, msg.Message)
}

// OnImportComplete mirrors C# `:29-32`.
func (s *Signal) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleImportComplete, msg.Message)
}

// OnRename is a no-op — C# Signal doesn't override OnRename.
func (s *Signal) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:34-37`.
func (s *Signal) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleEpisodeDeleted, msg.Message)
}

// OnSeriesAdd mirrors C# `:39-42`.
func (s *Signal) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleSeriesAdded, msg.Message)
}

// OnSeriesDelete mirrors C# `:44-47`.
func (s *Signal) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleSeriesDeleted, msg.Message)
}

// OnHealthIssue mirrors C# `:49-52`.
func (s *Signal) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleHealthIssue, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:54-57`.
func (s *Signal) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, s.doer, &s.settings, titleHealthRestored, body)
}

// OnApplicationUpdate mirrors C# `:59-62`.
func (s *Signal) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleApplicationUpdate, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:64-67`.
func (s *Signal) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, s.doer, &s.settings, titleManualInteractionRequired, msg.Message)
}

// ProcessQueue is a no-op — Signal has no queue.
func (s *Signal) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `SignalProxy.Test`
// at `SignalProxy.cs:66-121`. Maps the distinguished sentinels to
// per-property failures.
func (s *Signal) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := s.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	err := sendNotification(ctx, s.doer, &s.settings, "Test Notification", "This is a test message from Sonarr")
	if err == nil {
		return failures, nil
	}
	switch {
	case errors.Is(err, ErrSslRequired):
		failures = append(failures, notifications.TestFailure{PropertyName: "UseSsl", ErrorMessage: "Use SSL must be enabled to connect to this server"})
	case errors.Is(err, ErrInvalidCredentials):
		failures = append(failures, notifications.TestFailure{PropertyName: "AuthUsername", ErrorMessage: "Invalid username or password"})
	default:
		var apiErr *ErrAPIError
		if errors.As(err, &apiErr) {
			prop := "Host"
			low := strings.ToLower(apiErr.Body)
			switch {
			case strings.Contains(low, "invalid group id"):
				prop = "ReceiverId"
			case strings.Contains(low, "invalid account"):
				prop = "SenderNumber"
			}
			failures = append(failures, notifications.TestFailure{PropertyName: prop, ErrorMessage: apiErr.Body})
		} else {
			failures = append(failures, notifications.TestFailure{
				PropertyName:        "Host",
				ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
				DetailedDescription: err.Error(),
			})
		}
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Signal overrides
// every OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (s *Signal) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Signal.SupportsOnGrab]. Returns false.
func (s *Signal) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Signal.SupportsOnGrab].
func (s *Signal) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `Signal` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (s *Signal) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the Signal constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
