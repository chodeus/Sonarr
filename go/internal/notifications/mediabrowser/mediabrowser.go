package mediabrowser

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
// Mirrors C# `MediaBrowser.cs:12` class name.
const Implementation = "MediaBrowser"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `MediaBrowserSettings.cs:21` class name.
const ConfigContract = "MediaBrowserSettings"

// Update-type constants. Mirror C# `:18-20`.
const (
	updateCreated  = "Created"
	updateDeleted  = "Deleted"
	updateModified = "Modified"
)

// Per-event title constants. Mirror C# `NotificationBase.cs:13-22`.
// MediaBrowser uses BRANDED titles for every notification event.
const (
	titleEpisodeGrabbedBranded    = "Sonarr - Episode Grabbed"
	titleEpisodeDownloadedBranded = "Sonarr - Episode Downloaded"
	titleImportCompleteBranded    = "Sonarr - Import Complete"
	titleEpisodeDeletedBranded    = "Sonarr - Episode Deleted"
	titleSeriesAddedBranded       = "Sonarr - Series Added"
	titleSeriesDeletedBranded     = "Sonarr - Series Deleted"
	titleHealthIssueBranded       = "Sonarr - Health Check Failure"
	titleHealthRestoredBranded    = "Sonarr - Health Check Restored"
	titleApplicationUpdateBranded = "Sonarr - Application Updated"
)

// MediaBrowser ports the C# `MediaBrowser` notification provider at
// `Notifications/MediaBrowser/MediaBrowser.cs:12-156`.
type MediaBrowser struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
}

// Compile-time guard.
var _ notifications.Provider = (*MediaBrowser)(nil)

// New constructs a *MediaBrowser from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("mediabrowser: nil definition")
	}
	// Match C# `MediaBrowserSettings()` constructor defaults at
	// `MediaBrowserSettings.cs:25-29`: Port=8096, UpdateLibrary=true.
	s := Settings{Port: 8096, UpdateLibrary: true}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("mediabrowser: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &MediaBrowser{def: def, settings: s, doer: doer}, nil
}

// Name returns the provider display label. C# `:30`.
func (m *MediaBrowser) Name() string { return "Emby / Jellyfin" }

// Link returns the docs URL. C# `:29`.
func (m *MediaBrowser) Link() string { return "https://emby.media/" }

// Definition returns the persisted row.
func (m *MediaBrowser) Definition() *notifications.Notification { return m.def }

// updateIfEnabled fires a per-series library-update with the given
// update-type. Mirrors C# `UpdateIfEnabled` at `:138-145`. Skips when
// UpdateLibrary is disabled or series is nil.
//
// Coalescing-queue is deferred — see package doc. The C# code routes
// through MediaServerUpdateQueue which batches multiple events per
// series; the Go port calls update() per event directly.
func (m *MediaBrowser) updateIfEnabled(ctx context.Context, ser *series.Series, updateType string) error {
	if !m.settings.UpdateLibrary || ser == nil || ser.Path == "" {
		return nil
	}
	return update(ctx, m.doer, &m.settings, m.settings.applyMapping(ser.Path), updateType)
}

// OnGrab mirrors C# `:32-38`.
func (m *MediaBrowser) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	if !m.settings.Notify {
		return nil
	}
	return notify(ctx, m.doer, &m.settings, titleEpisodeGrabbedBranded, msg.Message)
}

// OnDownload mirrors C# `:40-48`.
func (m *MediaBrowser) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	if m.settings.Notify {
		if err := notify(ctx, m.doer, &m.settings, titleEpisodeDownloadedBranded, msg.Message); err != nil {
			return err
		}
	}
	return m.updateIfEnabled(ctx, msg.Series, updateCreated)
}

// OnImportComplete mirrors C# `:50-58`.
func (m *MediaBrowser) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	if m.settings.Notify {
		if err := notify(ctx, m.doer, &m.settings, titleImportCompleteBranded, msg.Message); err != nil {
			return err
		}
	}
	return m.updateIfEnabled(ctx, msg.Series, updateCreated)
}

// OnRename mirrors C# `:60-63`. Only fires the library-update — no
// notify. Series carried as `any` per RenameMessage signature.
func (m *MediaBrowser) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	if !m.settings.UpdateLibrary {
		return nil
	}
	if ser, ok := msg.Series.(*series.Series); ok && ser != nil && ser.Path != "" {
		return update(ctx, m.doer, &m.settings, m.settings.applyMapping(ser.Path), updateModified)
	}
	return nil
}

// OnEpisodeFileDelete mirrors C# `:65-73`.
func (m *MediaBrowser) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	if m.settings.Notify {
		if err := notify(ctx, m.doer, &m.settings, titleEpisodeDeletedBranded, msg.Message); err != nil {
			return err
		}
	}
	return m.updateIfEnabled(ctx, msg.Series, updateDeleted)
}

// OnSeriesAdd mirrors C# `:75-83`.
func (m *MediaBrowser) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	if m.settings.Notify {
		if err := notify(ctx, m.doer, &m.settings, titleSeriesAddedBranded, msg.Message); err != nil {
			return err
		}
	}
	return m.updateIfEnabled(ctx, msg.Series, updateCreated)
}

// OnSeriesDelete mirrors C# `:85-93`.
func (m *MediaBrowser) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	if m.settings.Notify {
		if err := notify(ctx, m.doer, &m.settings, titleSeriesDeletedBranded, msg.Message); err != nil {
			return err
		}
	}
	return m.updateIfEnabled(ctx, msg.Series, updateDeleted)
}

// OnHealthIssue mirrors C# `:95-101`. Notify-only — no library update.
func (m *MediaBrowser) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	if !m.settings.Notify {
		return nil
	}
	return notify(ctx, m.doer, &m.settings, titleHealthIssueBranded, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:103-109`.
func (m *MediaBrowser) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	if !m.settings.Notify {
		return nil
	}
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return notify(ctx, m.doer, &m.settings, titleHealthRestoredBranded, body)
}

// OnApplicationUpdate mirrors C# `:111-117`.
func (m *MediaBrowser) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	if !m.settings.Notify {
		return nil
	}
	return notify(ctx, m.doer, &m.settings, titleApplicationUpdateBranded, msg.Message)
}

// OnManualInteractionRequired is a no-op — C# MediaBrowser doesn't
// override the OnManualInteractionRequired hook.
func (m *MediaBrowser) OnManualInteractionRequired(_ context.Context, _ notifications.ManualInteractionRequiredMessage) error {
	return nil
}

// ProcessQueue is currently a no-op — the C# `:119-136` library-
// refresh coalescing buffer is deferred (see package doc).
func (m *MediaBrowser) ProcessQueue(_ context.Context) error { return nil }

// Test mirrors C# `MediaBrowserService.Test` at `:60-105`. Connection
// check via /System/Configuration, then (when Notify is enabled) a
// test notify call that distinguishes 404 (Jellyfin compatibility)
// per C# `:91-95`.
func (m *MediaBrowser) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := m.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	if err := testConnection(ctx, m.doer, &m.settings); err != nil {
		prop := "Host"
		if errors.Is(err, ErrInvalidAPIKey) {
			prop = "ApiKey"
		}
		failures = append(failures, notifications.TestFailure{
			PropertyName:        prop,
			ErrorMessage:        fmt.Sprintf("Unable to connect to Emby/Jellyfin: %v", err),
			DetailedDescription: err.Error(),
		})
		return failures, nil
	}
	if m.settings.Notify {
		if err := notify(ctx, m.doer, &m.settings, "Test from Sonarr", "Success! MediaBrowser has been successfully configured!"); err != nil {
			if errors.Is(err, ErrNotifyNotFound) {
				failures = append(failures, notifications.TestFailure{
					PropertyName: "Notify",
					ErrorMessage: "Unable to send notification to Emby. If you're using Jellyfin disable 'Send Notifications'",
				})
				return failures, nil
			}
			failures = append(failures, notifications.TestFailure{
				PropertyName:        "Host",
				ErrorMessage:        fmt.Sprintf("Test notify failed: %v", err),
				DetailedDescription: err.Error(),
			})
		}
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# MediaBrowser overrides
// every OnX hook EXCEPT OnManualInteractionRequired.
func (m *MediaBrowser) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnRename() bool { return true }

// SupportsOnSeriesAdd — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [MediaBrowser.SupportsOnGrab].
func (m *MediaBrowser) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired returns false — the C# class
// doesn't override this hook.
func (m *MediaBrowser) SupportsOnManualInteractionRequired() bool { return false }

// init registers the MediaBrowser constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
