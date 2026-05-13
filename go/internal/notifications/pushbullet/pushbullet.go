package pushbullet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `PushBullet.cs:10` class name.
const Implementation = "PushBullet"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `PushBulletSettings.cs:17` class name.
const ConfigContract = "PushBulletSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// Pushbullet uses BRANDED titles for the grab/download/import +
// health/update/manual events, PLAIN for delete/series-add/-delete.
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

// Pushbullet ports the C# `PushBullet` notification provider at
// `Notifications/Pushbullet/PushBullet.cs:10-112`.
type Pushbullet struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Pushbullet)(nil)

// New constructs a *Pushbullet from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("pushbullet: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("pushbullet: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Pushbullet{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:19`.
func (p *Pushbullet) Name() string { return "Pushbullet" }

// Link returns the docs URL. C# `:20`.
func (p *Pushbullet) Link() string { return "https://www.pushbullet.com/" }

// Definition returns the persisted row.
func (p *Pushbullet) Definition() *notifications.Notification { return p.def }

// OnGrab mirrors C# `:22-25`.
func (p *Pushbullet) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleEpisodeGrabbedBranded, msg.Message)
}

// OnDownload mirrors C# `:27-30`.
func (p *Pushbullet) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleEpisodeDownloadedBranded, msg.Message)
}

// OnImportComplete mirrors C# `:32-35`.
func (p *Pushbullet) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleImportCompleteBranded, msg.Message)
}

// OnRename is a no-op — C# Pushbullet doesn't override OnRename.
func (p *Pushbullet) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:37-40`.
func (p *Pushbullet) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleEpisodeDeleted, msg.Message)
}

// OnSeriesAdd mirrors C# `:42-45`.
func (p *Pushbullet) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleSeriesAdded, msg.Message)
}

// OnSeriesDelete mirrors C# `:47-50`.
func (p *Pushbullet) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleSeriesDeleted, msg.Message)
}

// OnHealthIssue mirrors C# `:52-55`.
func (p *Pushbullet) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleHealthIssueBranded, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:57-60`.
func (p *Pushbullet) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, p.doer, &p.settings, titleHealthRestoredBranded, body)
}

// OnApplicationUpdate mirrors C# `:62-65`.
func (p *Pushbullet) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleApplicationUpdateBranded, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:67-70`.
func (p *Pushbullet) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, titleManualInteractionRequiredBranded, msg.Message)
}

// ProcessQueue is a no-op — Pushbullet has no queue.
func (p *Pushbullet) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `PushBulletProxy.Test`
// at `PushBulletProxy.cs:126-153`. Maps ErrInvalidAccessToken to
// per-property failures (ApiKey).
func (p *Pushbullet) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := p.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	err := sendNotification(ctx, p.doer, &p.settings, "Sonarr - Test Notification", "This is a test message from Sonarr")
	if err == nil {
		return failures, nil
	}
	if errors.Is(err, ErrInvalidAccessToken) {
		failures = append(failures, notifications.TestFailure{PropertyName: "ApiKey", ErrorMessage: "API Key is invalid"})
		return failures, nil
	}
	failures = append(failures, notifications.TestFailure{
		PropertyName:        "ApiKey",
		ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
		DetailedDescription: err.Error(),
	})
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Pushbullet overrides
// every OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (p *Pushbullet) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Pushbullet.SupportsOnGrab]. Returns false.
func (p *Pushbullet) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Pushbullet.SupportsOnGrab].
func (p *Pushbullet) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction implements the C# `PushBullet.RequestAction` admin
// path at `:81-110`. One action:
//
//   - getDevices — enumerates the user's Pushbullet devices via
//     GET /v2/devices with HTTP Basic auth (ApiKey, ""). Returns
//     `{options: [{id, name}, ...]}` filtered to non-empty
//     Nicknames and sorted case-insensitively by Nickname. When
//     ApiKey is empty, returns `{devices: []}` early (mirrors the
//     C# guard at `:86-92`).
//
// Unknown actions return `{}` (mirrors C# `:109`).
func (p *Pushbullet) RequestAction(ctx context.Context, action string, _ map[string]string) (any, error) {
	if action != "getDevices" {
		return map[string]any{}, nil
	}
	if p.settings.ApiKey == "" {
		// C# `:88-92` — early return with the matching legacy key
		// shape (not `options`). The v3 UI tolerates both.
		return map[string]any{"devices": []any{}}, nil
	}
	devices, err := GetDevices(ctx, p.doer, &p.settings)
	if err != nil {
		return nil, err
	}
	filtered := make([]Device, 0, len(devices))
	for _, d := range devices {
		if d.Nickname != "" {
			filtered = append(filtered, d)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return strings.ToLower(filtered[i].Nickname) < strings.ToLower(filtered[j].Nickname)
	})
	options := make([]map[string]any, 0, len(filtered))
	for _, d := range filtered {
		options = append(options, map[string]any{"id": d.ID, "name": d.Nickname})
	}
	return map[string]any{"options": options}, nil
}

// init registers the Pushbullet constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
