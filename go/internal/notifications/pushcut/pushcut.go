package pushcut

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/series"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Pushcut.cs:10` class name.
const Implementation = "Pushcut"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `PushcutSettings.cs:17` class name.
const ConfigContract = "PushcutSettings"

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// Pushcut uses PLAIN titles for series/episode events and BRANDED
// titles for health/update/manual-interaction events.
const (
	titleEpisodeGrabbed                   = "Episode Grabbed"
	titleEpisodeDownloaded                = "Episode Downloaded"
	titleImportComplete                   = "Import Complete"
	titleEpisodeDeleted                   = "Episode Deleted"
	titleSeriesAdded                      = "Series Added"
	titleSeriesDeleted                    = "Series Deleted"
	titleHealthIssueBranded               = "Sonarr - Health Check Failure"
	titleHealthRestoredBranded            = "Sonarr - Health Check Restored"
	titleApplicationUpdateBranded         = "Sonarr - Application Updated"
	titleManualInteractionRequiredBranded = "Sonarr - Manual Interaction"
)

// Pushcut ports the C# `Pushcut` notification provider at
// `Notifications/Pushcut/Pushcut.cs:10-91`.
type Pushcut struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*Pushcut)(nil)

// New constructs a *Pushcut from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("pushcut: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("pushcut: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Pushcut{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:19`.
func (p *Pushcut) Name() string { return "Pushcut" }

// Link returns the docs URL. C# `:21`.
func (p *Pushcut) Link() string { return "https://www.pushcut.io" }

// Definition returns the persisted row.
func (p *Pushcut) Definition() *notifications.Notification { return p.def }

// buildPayload mirrors the inline payload composition at
// `PushcutProxy.cs:45-62`. Poster IS deferred — see package doc.
func (p *Pushcut) buildPayload(title, message string, ser *series.Series) *Payload {
	ts := p.settings.TimeSensitive
	payload := &Payload{
		Title:           title,
		Text:            message,
		IsTimeSensitive: &ts,
		Actions:         []Action{},
	}
	for _, l := range notifications.GenerateMetadataLinks(ser, p.settings.MetadataLinks) {
		payload.Actions = append(payload.Actions, Action{Name: l.Label, URL: l.URL})
	}
	// IncludePoster path deferred (no typed series.Images blob yet).
	return payload
}

// OnGrab mirrors C# `:32-35`.
func (p *Pushcut) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleEpisodeGrabbed, msg.Message, msg.Series))
}

// OnDownload mirrors C# `:37-40`.
func (p *Pushcut) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleEpisodeDownloaded, msg.Message, msg.Series))
}

// OnImportComplete mirrors C# `:42-45`.
func (p *Pushcut) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleImportComplete, msg.Message, msg.Series))
}

// OnRename is a no-op — C# Pushcut doesn't override OnRename.
func (p *Pushcut) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:47-50`.
func (p *Pushcut) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleEpisodeDeleted, msg.Message, msg.Series))
}

// OnSeriesAdd mirrors C# `:52-55`. C# composes the body as
// "{series.Title} added to library".
func (p *Pushcut) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	body := msg.Message
	if msg.Series != nil {
		body = msg.Series.Title + " added to library"
	}
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleSeriesAdded, body, msg.Series))
}

// OnSeriesDelete mirrors C# `:57-60`.
func (p *Pushcut) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleSeriesDeleted, msg.Message, msg.Series))
}

// OnHealthIssue mirrors C# `:62-65`. No series — no poster + no
// metadata-link actions. Title uses the branded variant.
func (p *Pushcut) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleHealthIssueBranded, msg.Check.Message, nil))
}

// OnHealthRestored mirrors C# `:67-70`. Title uses branded variant.
func (p *Pushcut) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleHealthRestoredBranded, body, nil))
}

// OnApplicationUpdate mirrors C# `:72-75`. Title uses branded variant.
func (p *Pushcut) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleApplicationUpdateBranded, msg.Message, nil))
}

// OnManualInteractionRequired mirrors C# `:77-80`. Branded title.
func (p *Pushcut) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return sendNotification(ctx, p.doer, &p.settings, p.buildPayload(titleManualInteractionRequiredBranded, msg.Message, nil))
}

// ProcessQueue is a no-op — Pushcut has no queue.
func (p *Pushcut) ProcessQueue(_ context.Context) error { return nil }

// Test sends a synthetic test message. Mirrors C# `PushcutProxy.Test`
// at `PushcutProxy.cs:78-107`. Maps the distinguished sentinels to
// per-property failures: 403 => "API Key"; ApiError => "Url"; other
// transport errors => "Host".
func (p *Pushcut) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := p.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	payload := p.buildPayload("Sonarr Test Title", "Success! You have properly configured your Pushcut notification settings.", nil)
	err := sendNotification(ctx, p.doer, &p.settings, payload)
	if err == nil {
		return failures, nil
	}
	switch {
	case errors.Is(err, ErrInvalidAPIKey):
		failures = append(failures, notifications.TestFailure{PropertyName: "API Key", ErrorMessage: "API Key is invalid"})
	case errors.Is(err, ErrAPIError):
		failures = append(failures, notifications.TestFailure{PropertyName: "Url", ErrorMessage: err.Error()})
	default:
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to connect to Pushcut: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Pushcut overrides
// every OnX hook EXCEPT OnRename — SupportsOnRename returns false.
func (p *Pushcut) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Pushcut.SupportsOnGrab]. Returns false.
func (p *Pushcut) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Pushcut.SupportsOnGrab].
func (p *Pushcut) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `Pushcut` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (p *Pushcut) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the Pushcut constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
