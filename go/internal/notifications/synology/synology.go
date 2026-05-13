package synology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/series"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `SynologyIndexer.cs:12` class name.
const Implementation = "SynologyIndexer"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `SynologyIndexerSettings.cs:11` class name.
const ConfigContract = "SynologyIndexerSettings"

// Synology ports the C# `SynologyIndexer` notification provider at
// `Notifications/Synology/SynologyIndexer.cs:12-113`.
//
// Only 6 of the 11 OnX hooks are overridden in C# (Download /
// ImportComplete / Rename / EpisodeFileDelete / SeriesAdd /
// SeriesDelete). The other 5 (Grab / HealthIssue / HealthRestored /
// ApplicationUpdate / ManualInteractionRequired) return nil with
// SupportsOnX false per the C# `NotificationBase` defaults.
type Synology struct {
	def      *notifications.Notification
	settings Settings
}

// Compile-time guard.
var _ notifications.Provider = (*Synology)(nil)

// New constructs a *Synology from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, _ notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("synology: nil definition")
	}
	// Match C# `SynologyIndexerSettings()` constructor default at
	// `SynologyIndexerSettings.cs:15-18`: UpdateLibrary=true.
	s := Settings{UpdateLibrary: true}
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("synology: unmarshal settings: %w", err)
		}
	}
	return &Synology{def: def, settings: s}, nil
}

// Name returns the provider display label. C# `:24`.
func (s *Synology) Name() string { return "Synology Indexer" }

// Link returns the docs URL. C# `:23`.
func (s *Synology) Link() string { return "https://www.synology.com" }

// Definition returns the persisted row.
func (s *Synology) Definition() *notifications.Notification { return s.def }

// OnGrab — not overridden in C#; no-op.
func (s *Synology) OnGrab(_ context.Context, _ notifications.GrabMessage) error { return nil }

// OnDownload mirrors C# `:26-43`. Deletes each old EpisodeFile path
// via synoindex -d then adds the new EpisodeFile path via -a.
func (s *Synology) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	if !s.settings.UpdateLibrary {
		return nil
	}
	if msg.Series == nil || msg.EpisodeFile == nil {
		return nil
	}
	for _, old := range msg.OldFiles {
		if old == nil || old.File == nil {
			continue
		}
		oldPath := filepath.Join(msg.Series.Path, old.File.RelativePath)
		if err := deleteFile(ctx, oldPath); err != nil {
			return err
		}
	}
	full := filepath.Join(msg.Series.Path, msg.EpisodeFile.RelativePath)
	return addFile(ctx, full)
}

// OnImportComplete mirrors C# `:45-51`. Calls synoindex -R for the
// series folder.
func (s *Synology) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	if !s.settings.UpdateLibrary {
		return nil
	}
	if msg.Series == nil || msg.Series.Path == "" {
		return nil
	}
	return updateFolder(ctx, msg.Series.Path)
}

// OnRename mirrors C# `:53-59`. Calls synoindex -R for the series
// folder. The RenameMessage carries the Series as `any` — type-assert
// for the Path field.
func (s *Synology) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	if !s.settings.UpdateLibrary {
		return nil
	}
	path := seriesPathFromAny(msg.Series)
	if path == "" {
		return nil
	}
	return updateFolder(ctx, path)
}

// OnEpisodeFileDelete mirrors C# `:61-68`.
func (s *Synology) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	if !s.settings.UpdateLibrary {
		return nil
	}
	if msg.Series == nil || msg.EpisodeFile == nil {
		return nil
	}
	full := filepath.Join(msg.Series.Path, msg.EpisodeFile.RelativePath)
	return deleteFile(ctx, full)
}

// OnSeriesAdd mirrors C# `:70-76`.
func (s *Synology) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	if !s.settings.UpdateLibrary {
		return nil
	}
	if msg.Series == nil || msg.Series.Path == "" {
		return nil
	}
	return updateFolder(ctx, msg.Series.Path)
}

// OnSeriesDelete mirrors C# `:78-87`. Only fires when DeletedFiles is
// true (the C# code branches on the deleteMessage.DeletedFiles flag).
func (s *Synology) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	if !msg.DeletedFiles || !s.settings.UpdateLibrary {
		return nil
	}
	if msg.Series == nil || msg.Series.Path == "" {
		return nil
	}
	return deleteFolder(ctx, msg.Series.Path)
}

// OnHealthIssue — not overridden in C#; no-op.
func (s *Synology) OnHealthIssue(_ context.Context, _ notifications.HealthCheckMessage) error {
	return nil
}

// OnHealthRestored — not overridden in C#; no-op.
func (s *Synology) OnHealthRestored(_ context.Context, _ notifications.HealthCheckMessage) error {
	return nil
}

// OnApplicationUpdate — not overridden in C#; no-op.
func (s *Synology) OnApplicationUpdate(_ context.Context, _ notifications.ApplicationUpdateMessage) error {
	return nil
}

// OnManualInteractionRequired — not overridden in C#; no-op.
func (s *Synology) OnManualInteractionRequired(_ context.Context, _ notifications.ManualInteractionRequiredMessage) error {
	return nil
}

// ProcessQueue is a no-op — Synology has no queue.
func (s *Synology) ProcessQueue(_ context.Context) error { return nil }

// Test mirrors C# `SynologyIndexer.Test` + `TestConnection` at
// `:89-111`. Returns ErrNotLinux failure on non-Linux hosts;
// otherwise execs `synoindex --help` to confirm the binary is
// reachable.
func (s *Synology) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	if !isLinux() {
		return []notifications.TestFailure{{PropertyName: "", ErrorMessage: "Synology Indexer is only available on Linux"}}, nil
	}
	if err := testHelp(ctx); err != nil {
		return []notifications.TestFailure{{
			PropertyName:        "",
			ErrorMessage:        fmt.Sprintf("Synology Indexer test failed: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	return nil, nil
}

// SupportsOnGrab reports OnGrab capability. Synology only overrides
// OnDownload / OnImportComplete / OnRename / OnEpisodeFileDelete /
// OnSeriesAdd / OnSeriesDelete — the rest return false.
func (s *Synology) SupportsOnGrab() bool { return false }

// SupportsOnDownload — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Synology.SupportsOnGrab]. C# base
// `NotificationBase.cs:99` ties this to SupportsOnDownload, so true.
func (s *Synology) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnRename() bool { return true }

// SupportsOnSeriesAdd — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnHealthIssue() bool { return false }

// SupportsOnHealthRestored — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnHealthRestored() bool { return false }

// SupportsOnApplicationUpdate — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnApplicationUpdate() bool { return false }

// SupportsOnManualInteractionRequired — see [Synology.SupportsOnGrab].
func (s *Synology) SupportsOnManualInteractionRequired() bool { return false }

// RequestAction is a no-op — the C# `Synology` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (s *Synology) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// seriesPathFromAny extracts series.Path from the RenameMessage's
// any-typed Series field. Returns "" when the assertion fails —
// the hook then no-ops rather than synoindex-erroring on an empty
// path. Mirrors the slack precedent at
// internal/notifications/slack/slack.go titleFromSeriesAny.
func seriesPathFromAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(*series.Series); ok && s != nil {
		return s.Path
	}
	return ""
}

// init registers the Synology constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
