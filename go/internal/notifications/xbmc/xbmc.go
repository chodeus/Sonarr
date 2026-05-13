package xbmc

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
// Mirrors C# `Xbmc.cs:13` class name.
const Implementation = "Xbmc"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `XbmcSettings.cs:20` class name.
const ConfigContract = "XbmcSettings"

// Per-event title constants. Mirror C# `Xbmc.cs:28-101` — Kodi uses
// the same SHORT branded titles for grab/download/import/delete/add
// but the standard BRANDED titles for health/update/manual.
const (
	titleGrabbed                   = "Sonarr - Grabbed"
	titleDownloaded                = "Sonarr - Downloaded"
	titleImported                  = "Sonarr - Imported"
	titleDeleted                   = "Sonarr - Deleted"
	titleAdded                     = "Sonarr - Added"
	titleHealthIssueBranded        = "Sonarr - Health Check Failure"
	titleHealthRestoredBranded     = "Sonarr - Health Check Restored"
	titleApplicationUpdateBranded  = "Sonarr - Application Updated"
	titleManualInteractionRequired = "Manual Interaction"
)

// Xbmc ports the C# `Xbmc` notification provider at
// `Notifications/Xbmc/Xbmc.cs:13-168`.
type Xbmc struct {
	def      *notifications.Notification
	settings Settings
	doer     httpDoer
	queue    *mediaserverqueue.Queue[bool]
}

// Compile-time guard.
var _ notifications.Provider = (*Xbmc)(nil)

// New constructs an *Xbmc from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("xbmc: nil definition")
	}
	// Match C# `XbmcSettings()` constructor defaults at
	// `XbmcSettings.cs:24-29`: Port=8080, UrlBase=/jsonrpc,
	// DisplayTime=5.
	s := Settings{Port: 8080, UrlBase: "/jsonrpc", DisplayTime: 5}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("xbmc: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Xbmc{
		def:      def,
		settings: s,
		doer:     doer,
		queue:    mediaserverqueue.New[bool](),
	}, nil
}

// Name returns the provider display label. C# `:103`.
func (x *Xbmc) Name() string { return "Kodi" }

// Link returns the docs URL. C# `:26`.
func (x *Xbmc) Link() string { return "https://kodi.tv/" }

// Definition returns the persisted row.
func (x *Xbmc) Definition() *notifications.Notification { return x.def }

// notifyIfEnabled fires a Kodi notification when Settings.Notify is
// on. Errors are returned to the caller (the C# code swallows
// SocketException at the OnX layer — the Go port leaves that to the
// orchestrator's per-provider try/catch). Mirrors C# `Notify` at
// `:143-157`.
func (x *Xbmc) notifyIfEnabled(ctx context.Context, header, message string) error {
	if !x.settings.Notify {
		return nil
	}
	return notify(ctx, x.doer, &x.settings, header, message)
}

// updateAndClean enqueues a per-(host,series) library-refresh +
// clean-flag pair. Mirrors C# `UpdateAndClean` at `:159-166`. The
// actual Player.GetActivePlayers / VideoLibrary.Scan / .Clean calls
// happen later in [Xbmc.ProcessQueue].
//
// Returns error for signature parity with the OnX hooks; enqueue is
// in-process and cannot fail.
func (x *Xbmc) updateAndClean(_ context.Context, ser *series.Series, clean bool) error {
	if !x.settings.UpdateLibrary && !x.settings.CleanLibrary {
		return nil
	}
	if ser == nil {
		return nil
	}
	x.queue.Add(x.settings.Host, ser, clean)
	return nil
}

// OnGrab mirrors C# `:28-33`. Notify-only (no library update).
func (x *Xbmc) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	return x.notifyIfEnabled(ctx, titleGrabbed, msg.Message)
}

// OnDownload mirrors C# `:35-41`. Notify + library update; clean
// flag = OldFiles non-empty.
func (x *Xbmc) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	if err := x.notifyIfEnabled(ctx, titleDownloaded, msg.Message); err != nil {
		return err
	}
	return x.updateAndClean(ctx, msg.Series, len(msg.OldFiles) > 0)
}

// OnImportComplete mirrors C# `:43-49`. Default clean=true.
func (x *Xbmc) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	if err := x.notifyIfEnabled(ctx, titleImported, msg.Message); err != nil {
		return err
	}
	return x.updateAndClean(ctx, msg.Series, true)
}

// OnRename mirrors C# `:51-54`. Library update only; clean=true.
func (x *Xbmc) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	if ser, ok := msg.Series.(*series.Series); ok && ser != nil {
		return x.updateAndClean(ctx, ser, true)
	}
	return nil
}

// OnEpisodeFileDelete mirrors C# `:56-62`. Clean=true.
func (x *Xbmc) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	if err := x.notifyIfEnabled(ctx, titleDeleted, msg.Message); err != nil {
		return err
	}
	return x.updateAndClean(ctx, msg.Series, true)
}

// OnSeriesAdd mirrors C# `:64-70`. Clean=true.
func (x *Xbmc) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	if err := x.notifyIfEnabled(ctx, titleAdded, msg.Message); err != nil {
		return err
	}
	return x.updateAndClean(ctx, msg.Series, true)
}

// OnSeriesDelete mirrors C# `:72-81`. Only fires when DeletedFiles=true.
func (x *Xbmc) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	if !msg.DeletedFiles {
		return nil
	}
	if err := x.notifyIfEnabled(ctx, titleDeleted, msg.Message); err != nil {
		return err
	}
	return x.updateAndClean(ctx, msg.Series, true)
}

// OnHealthIssue mirrors C# `:83-86`.
func (x *Xbmc) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	return x.notifyIfEnabled(ctx, titleHealthIssueBranded, msg.Check.Message)
}

// OnHealthRestored mirrors C# `:88-91`.
func (x *Xbmc) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	body := fmt.Sprintf("The following issue is now resolved: %s", msg.Check.Message)
	return x.notifyIfEnabled(ctx, titleHealthRestoredBranded, body)
}

// OnApplicationUpdate mirrors C# `:93-96`.
func (x *Xbmc) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	return x.notifyIfEnabled(ctx, titleApplicationUpdateBranded, msg.Message)
}

// OnManualInteractionRequired mirrors C# `:98-101`. The C# constant
// here is the PLAIN MANUAL_INTERACTION_REQUIRED_TITLE per the missing
// BRANDED suffix in `:100`.
func (x *Xbmc) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	return x.notifyIfEnabled(ctx, titleManualInteractionRequired, msg.Message)
}

// ProcessQueue drains the per-host coalescing buffer and fires the
// VideoLibrary.Scan + optional .Clean RPCs per batched series. Mirrors
// C# `:105-132` + the player-busy gate from C# `XbmcService.Update`
// at `:37-47`.
//
// For each drained series:
//   - When AlwaysUpdate is false, Player.GetActivePlayers gates the
//     update — a busy video player short-circuits the whole iteration.
//   - VideoLibrary.Scan fires when UpdateLibrary is on.
//   - VideoLibrary.Clean fires when CleanLibrary is on AND any
//     accumulated clean flag for the series was true (mirrors C#
//     `:120` `item.Info.Contains(true)`).
//
// First per-series error is returned; remaining items are still
// attempted (mirrors C# catch-and-continue at `:125-129`).
func (x *Xbmc) ProcessQueue(ctx context.Context) error {
	var firstErr error
	x.queue.Process(x.settings.Host, func(items []*mediaserverqueue.Item[bool]) {
		for _, item := range items {
			if !x.settings.AlwaysUpdate {
				busy, err := videoPlayerActive(ctx, x.doer, &x.settings)
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				if busy {
					continue
				}
			}
			if x.settings.UpdateLibrary {
				// GetSeriesPath discovery is deferred (see package
				// doc) — pass empty path to trigger a full library
				// scan.
				if err := updateLibrary(ctx, x.doer, &x.settings, ""); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			if x.settings.CleanLibrary {
				if _, ok := item.Info[true]; ok {
					if err := cleanLibrary(ctx, x.doer, &x.settings); err != nil && firstErr == nil {
						firstErr = err
					}
				}
			}
		}
	})
	return firstErr
}

// Test sends a synthetic Kodi notification. Mirrors C# `Xbmc.Test`
// at `:134-141` + `XbmcService.Test` at `:127-140`. The C# test
// sends "Success! Kodi has been successfully configured!" — failures
// surface with PropertyName "Host" per `:136`.
func (x *Xbmc) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := x.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	if err := notify(ctx, x.doer, &x.settings, "Test Notification", "Success! Kodi has been successfully configured!"); err != nil {
		failures = append(failures, notifications.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to send test message: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# Xbmc overrides every
// OnX hook — every Supports* flag is true including SupportsOnRename.
func (x *Xbmc) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnRename() bool { return true }

// SupportsOnSeriesAdd — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [Xbmc.SupportsOnGrab].
func (x *Xbmc) SupportsOnManualInteractionRequired() bool { return true }

// init registers the Xbmc constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
