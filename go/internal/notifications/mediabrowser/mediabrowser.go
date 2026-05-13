package mediabrowser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/mediaserverqueue"
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
	queue    *mediaserverqueue.Queue[string]
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
	return &MediaBrowser{
		def:      def,
		settings: s,
		doer:     doer,
		queue:    mediaserverqueue.New[string](),
	}, nil
}

// Name returns the provider display label. C# `:30`.
func (m *MediaBrowser) Name() string { return "Emby / Jellyfin" }

// Link returns the docs URL. C# `:29`.
func (m *MediaBrowser) Link() string { return "https://emby.media/" }

// Definition returns the persisted row.
func (m *MediaBrowser) Definition() *notifications.Notification { return m.def }

// updateIfEnabled enqueues a per-series library-update keyed by host.
// Mirrors C# `UpdateIfEnabled` at `:138-145`. The actual library-
// refresh HTTP call is deferred to [MediaBrowser.ProcessQueue] which
// the orchestrator drains periodically — exactly mirrors the C#
// MediaServerUpdateQueue flow.
//
// Returns error for signature parity with the OnX hooks; enqueue is
// in-process and cannot fail.
func (m *MediaBrowser) updateIfEnabled(_ context.Context, ser *series.Series, updateType string) error {
	if !m.settings.UpdateLibrary || ser == nil || ser.Path == "" {
		return nil
	}
	m.queue.Add(m.settings.Host, ser, updateType)
	return nil
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

// OnRename mirrors C# `:60-63`. Only enqueues the library-update — no
// notify. Series carried as `any` per RenameMessage signature.
func (m *MediaBrowser) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	ser, _ := msg.Series.(*series.Series)
	return m.updateIfEnabled(ctx, ser, updateModified)
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

// ProcessQueue drains the per-host coalescing buffer and fires one
// /Library/Media/Updated POST per batched series. Mirrors C#
// `:119-136`. Per the C# branch at `:130`: when the series has a
// single accumulated UpdateType across the batch window, that
// specific type is sent; otherwise an empty UpdateType is sent and
// Emby decides. The first per-series error is returned; remaining
// items are still attempted (mirrors C# foreach without break).
func (m *MediaBrowser) ProcessQueue(ctx context.Context) error {
	if !m.settings.UpdateLibrary {
		return nil
	}
	var firstErr error
	m.queue.Process(m.settings.Host, func(items []*mediaserverqueue.Item[string]) {
		for _, item := range items {
			updateType := ""
			if len(item.Info) == 1 {
				for k := range item.Info {
					updateType = k
				}
			}
			if err := update(ctx, m.doer, &m.settings, m.settings.applyMapping(item.Series.Path), updateType); err != nil {
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	})
	return firstErr
}

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
