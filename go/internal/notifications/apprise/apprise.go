package apprise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Apprise.cs:10` class name.
const Implementation = "Apprise"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `AppriseSettings.cs:37` class name.
const ConfigContract = "AppriseSettings"

// sonarrTestPosterURL is the per-app logo URL the C# Test() uses.
// Mirrors C# `AppriseProxy.Test` poster constant at `:94`.
const sonarrTestPosterURL = "https://raw.githubusercontent.com/Sonarr/Sonarr/develop/Logo/128.png"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// Apprise uses PLAIN titles for every event.
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

// Apprise ports the C# `Apprise` notification provider at
// `Notifications/Apprise/Apprise.cs:10-87`.
type Apprise struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Apprise)(nil)

// New constructs an *Apprise from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("apprise: nil definition")
	}
	// Match C# `AppriseSettings()` constructor defaults at
	// `AppriseSettings.cs:41-45`: NotificationType=Info, Tags=[].
	s := Settings{NotificationType: int(NotificationTypeInfo)}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("apprise: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Apprise{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:12`.
func (a *Apprise) Name() string { return "Apprise" }

// Link returns the docs URL. C# `:14`.
func (a *Apprise) Link() string { return "https://github.com/caronc/apprise" }

// Definition returns the persisted row.
func (a *Apprise) Definition() *notifications.Notification { return a.def }

// posterURL is the deferred series-poster URL path. Currently
// returns "" because the series.Images blob isn't decoded into typed
// cover-type entries yet — see package doc.
func (a *Apprise) posterURL(_ any) string { return "" }

// OnGrab mirrors C# `:23-26`.
func (a *Apprise) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, a.doer, &a.settings, titleEpisodeGrabbed, msg.Message, a.posterURL(msg.Series))
}

// OnDownload mirrors C# `:28-31`.
func (a *Apprise) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, a.doer, &a.settings, titleEpisodeDownloaded, msg.Message, a.posterURL(msg.Series))
}

// OnImportComplete mirrors C# `:33-36`.
func (a *Apprise) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, a.doer, &a.settings, titleImportComplete, msg.Message, a.posterURL(msg.Series))
}

// OnRename is a no-op — C# Apprise doesn't override OnRename.
func (a *Apprise) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:38-41`.
func (a *Apprise) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, a.doer, &a.settings, titleEpisodeDeleted, msg.Message, a.posterURL(msg.Series))
}

// OnSeriesAdd mirrors C# `:43-46`.
func (a *Apprise) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, a.doer, &a.settings, titleSeriesAdded, msg.Message, a.posterURL(msg.Series))
}

// OnSeriesDelete mirrors C# `:48-51`.
func (a *Apprise) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, a.doer, &a.settings, titleSeriesDeleted, msg.Message, a.posterURL(msg.Series))
}

// OnHealthIssue mirrors C# `:53-56`. No series — no poster.
func (a *Apprise) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, a.doer, &a.settings, titleHealthIssue, msg.Check.Message, "")
}

// OnHealthRestored mirrors C# `:58-61`.
func (a *Apprise) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, a.doer, &a.settings, titleHealthRestored, body, "")
}

// OnApplicationUpdate mirrors C# `:63-66`.
func (a *Apprise) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, a.doer, &a.settings, titleApplicationUpdate, msg.Message, "")
}

// OnManualInteractionRequired mirrors C# `:68-71`.
func (a *Apprise) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, a.doer, &a.settings, titleManualInteractionRequired, msg.Message, a.posterURL(msg.Series))
}

// ProcessQueue is a no-op — Apprise has no queue.
func (a *Apprise) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `AppriseProxy.Test`
// at `AppriseProxy.cs:90-126`. Maps the distinguished sentinels to
// per-property failures: 401 => "AuthUsername"; ApiError => "" (empty
// PropertyName per C# `:113`); other transport errors => "Url".
func (a *Apprise) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := a.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	err := sendNotification(ctx, a.doer, &a.settings, "Sonarr - Test Notification", "Success! You have properly configured your apprise notification settings.", sonarrTestPosterURL)
	if err == nil {
		return failures, nil
	}
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		failures = append(failures, notifications.TestFailure{PropertyName: "AuthUsername", ErrorMessage: "HTTP Auth credentials are invalid"})
	case errors.Is(err, ErrAPIError):
		failures = append(failures, notifications.TestFailure{PropertyName: "", ErrorMessage: err.Error()})
	default:
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "Url",
			ErrorMessage:        fmt.Sprintf("Unable to connect to Apprise: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Apprise overrides
// every OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (a *Apprise) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Apprise.SupportsOnGrab]. Returns false.
func (a *Apprise) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Apprise.SupportsOnGrab].
func (a *Apprise) SupportsOnManualInteractionRequired() bool { return true }

// init registers the Apprise constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
