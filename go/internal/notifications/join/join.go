package join

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Join.cs:7` class name.
const Implementation = "Join"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `JoinSettings.cs:16` class name.
const ConfigContract = "JoinSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// Join uses BRANDED titles for every event per C# `Join.cs:20-69`.
const (
	titleEpisodeGrabbedBranded            = "Sonarr - Episode Grabbed"
	titleEpisodeDownloadedBranded         = "Sonarr - Episode Downloaded"
	titleImportCompleteBranded            = "Sonarr - Import Complete"
	titleEpisodeDeletedBranded            = "Sonarr - Episode Deleted"
	titleSeriesAddedBranded               = "Sonarr - Series Added"
	titleSeriesDeletedBranded             = "Sonarr - Series Deleted"
	titleHealthIssueBranded               = "Sonarr - Health Check Failure"
	titleHealthRestoredBranded            = "Sonarr - Health Check Restored"
	titleApplicationUpdateBranded         = "Sonarr - Application Updated"
	titleManualInteractionRequiredBranded = "Sonarr - Manual Interaction"
)

// Join ports the C# `Join` notification provider at
// `Notifications/Join/Join.cs:7-79`.
type Join struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Join)(nil)

// New constructs a *Join from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("join: nil definition")
	}
	// Match C# `JoinSettings()` constructor default at
	// `JoinSettings.cs:20-23`: Priority=Normal.
	s := Settings{Priority: int(PriorityNormal)}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("join: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Join{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:16`.
func (j *Join) Name() string { return "Join" }

// Link returns the docs URL. C# `:18`.
func (j *Join) Link() string { return "https://joaoapps.com/join/" }

// Definition returns the persisted row.
func (j *Join) Definition() *notifications.Notification { return j.def }

// OnGrab mirrors C# `:20-23`.
func (j *Join) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, j.doer, &j.settings, titleEpisodeGrabbedBranded, msg.Message)
}

// OnDownload mirrors C# `:25-28`.
func (j *Join) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, j.doer, &j.settings, titleEpisodeDownloadedBranded, msg.Message)
}

// OnImportComplete mirrors C# `:30-33`.
func (j *Join) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, j.doer, &j.settings, titleImportCompleteBranded, msg.Message)
}

// OnRename is a no-op — C# Join doesn't override OnRename.
func (j *Join) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:35-38`.
func (j *Join) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, j.doer, &j.settings, titleEpisodeDeletedBranded, msg.Message)
}

// OnSeriesAdd mirrors C# `:40-43`.
func (j *Join) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, j.doer, &j.settings, titleSeriesAddedBranded, msg.Message)
}

// OnSeriesDelete mirrors C# `:45-48`.
func (j *Join) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, j.doer, &j.settings, titleSeriesDeletedBranded, msg.Message)
}

// OnHealthIssue mirrors C# `:50-53`.
func (j *Join) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, j.doer, &j.settings, titleHealthIssueBranded, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:55-58`.
func (j *Join) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, j.doer, &j.settings, titleHealthRestoredBranded, body)
}

// OnApplicationUpdate mirrors C# `:60-63`.
func (j *Join) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, j.doer, &j.settings, titleApplicationUpdateBranded, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:65-68`.
func (j *Join) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, j.doer, &j.settings, titleManualInteractionRequiredBranded, msg.Message)
}

// ProcessQueue is a no-op — Join has no queue.
func (j *Join) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `JoinProxy.Test`
// at `JoinProxy.cs:49-79`. Maps the distinguished sentinels to per-
// property failures: ErrInvalidDevice => DeviceIds; otherwise ApiKey.
func (j *Join) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := j.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	err := sendNotification(ctx, j.doer, &j.settings, "Test Notification", "This is a test message from Sonarr.")
	if err == nil {
		return failures, nil
	}
	if errors.Is(err, ErrInvalidDevice) {
		failures = append(failures, notifications.TestFailure{PropertyName: "DeviceIds", ErrorMessage: "Invalid Device IDs supplied"})
		return failures, nil
	}
	failures = append(failures, notifications.TestFailure{
		PropertyName:        "ApiKey",
		ErrorMessage:        fmt.Sprintf("Unable to send test Join message: %v", err),
		DetailedDescription: err.Error(),
	})
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Join overrides every
// OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (j *Join) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Join.SupportsOnGrab]. Returns false.
func (j *Join) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Join.SupportsOnGrab].
func (j *Join) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `Join` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (j *Join) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the Join constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
