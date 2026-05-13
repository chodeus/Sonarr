package ntfy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Ntfy.cs:8` class name.
const Implementation = "Ntfy"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `NtfySettings.cs:26` class name.
const ConfigContract = "NtfySettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// "Branded" variants prepend "Sonarr - " — Ntfy uses branded for
// most events, but plain for delete/series-add/series-delete.
const (
	titleEpisodeGrabbedBranded            = "Sonarr - Episode Grabbed"
	titleEpisodeDownloadedBranded         = "Sonarr - Episode Downloaded"
	titleImportCompleteBranded            = "Sonarr - Import Complete"
	titleEpisodeDeleted                   = "Episode Deleted"
	titleSeriesAdded                      = "Series Added"
	titleSeriesDeleted                    = "Series Deleted"
	titleHealthIssueBranded               = "Sonarr - Health Check Failure"
	titleHealthRestoredBranded            = "Sonarr - Health Check Restored"
	titleApplicationUpdateBranded         = "Sonarr - Application Updated"
	titleManualInteractionRequiredBranded = "Sonarr - Manual Interaction"
)

// Ntfy ports the C# `Ntfy` notification provider at
// `Notifications/Ntfy/Ntfy.cs:8-80`.
//
// Goroutine-safety: httpDoer is shared across concurrent OnX calls.
// settings is captured by value at construction time.
type Ntfy struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Ntfy)(nil)

// New constructs a *Ntfy from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("ntfy: nil definition")
	}
	// Match C# `NtfySettings()` constructor defaults at
	// `NtfySettings.cs:30-34`: Topics=[], Priority=3 (Default).
	s := Settings{Priority: int(PriorityDefault)}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("ntfy: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Ntfy{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:17`.
func (n *Ntfy) Name() string { return "ntfy.sh" }

// Link returns the docs URL. C# `:19`.
func (n *Ntfy) Link() string { return "https://ntfy.sh/" }

// Definition returns the persisted row.
func (n *Ntfy) Definition() *notifications.Notification { return n.def }

// OnGrab mirrors C# `:21-24`.
func (n *Ntfy) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, titleEpisodeGrabbedBranded, msg.Message)
}

// OnDownload mirrors C# `:26-29`.
func (n *Ntfy) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, titleEpisodeDownloadedBranded, msg.Message)
}

// OnImportComplete mirrors C# `:31-34`.
func (n *Ntfy) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, titleImportCompleteBranded, msg.Message)
}

// OnRename is a no-op — C# Ntfy doesn't override OnRename.
func (n *Ntfy) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:36-39`.
func (n *Ntfy) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, titleEpisodeDeleted, msg.Message)
}

// OnSeriesAdd mirrors C# `:41-44`.
func (n *Ntfy) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, titleSeriesAdded, msg.Message)
}

// OnSeriesDelete mirrors C# `:46-49`.
func (n *Ntfy) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, titleSeriesDeleted, msg.Message)
}

// OnHealthIssue mirrors C# `:51-54`.
func (n *Ntfy) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, titleHealthIssueBranded, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:56-59`.
func (n *Ntfy) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, n.doer, &n.settings, titleHealthRestoredBranded, body)
}

// OnApplicationUpdate mirrors C# `:61-64`.
func (n *Ntfy) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, titleApplicationUpdateBranded, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:66-69`.
func (n *Ntfy) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, n.doer, &n.settings, titleManualInteractionRequiredBranded, msg.Message)
}

// ProcessQueue is a no-op — Ntfy has no queue.
func (n *Ntfy) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `Ntfy.Test` +
// `NtfyProxy.Test` at `NtfyProxy.cs:71-111`. Maps ErrAuthRequired
// to per-property failures (AccessToken vs UserName) per the C#
// catch branches.
func (n *Ntfy) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := n.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	err := sendNotification(ctx, n.doer, &n.settings, "Sonarr - Test Notification", "This is a test message from Sonarr")
	if err == nil {
		return failures, nil
	}
	if errors.Is(err, ErrAuthRequired) {
		switch {
		case n.settings.AccessToken != "":
			failures = append(failures, notifications.TestFailure{PropertyName: "AccessToken", ErrorMessage: "Invalid access token"})
		case n.settings.UserName != "" && n.settings.Password != "":
			failures = append(failures, notifications.TestFailure{PropertyName: "UserName", ErrorMessage: "Invalid username or password"})
		default:
			failures = append(failures, notifications.TestFailure{PropertyName: "AccessToken", ErrorMessage: "Authorization is required"})
		}
		return failures, nil
	}
	failures = append(failures, notifications.TestFailure{
		PropertyName:        "ServerUrl",
		ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
		DetailedDescription: err.Error(),
	})
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Ntfy overrides every
// OnX hook EXCEPT OnRename — SupportsOnRename returns false to
// reflect the C# non-override.
func (n *Ntfy) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Ntfy.SupportsOnGrab]. Returns false.
func (n *Ntfy) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Ntfy.SupportsOnGrab].
func (n *Ntfy) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `Ntfy` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (n *Ntfy) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the Ntfy constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
