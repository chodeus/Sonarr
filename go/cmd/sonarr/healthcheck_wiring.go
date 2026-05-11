// Boot-time adapters that bridge the production services into the
// `internal/healthcheck` per-check narrow interfaces. Same posture as
// `decisionengine_wiring.go` / `downloadedimport_wiring.go`: the
// adapter only exists where a real service exists but its surface
// differs from what a check needs.
//
// The healthcheck.Service check list is fixed at construction time
// (see [healthcheck.NewService]); each new check requires a boot-site
// constructor invocation. Adapters live here so main.go's serve()
// stays focused on ordering / dependency wiring.

package main

import (
	"context"
	"fmt"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/rootfolders"
)

// seriesFolderFormatAdapter bridges *namingconfig.Service to the
// narrow [rootfolders.NamingConfigProvider] which only needs the
// SeriesFolderFormat field. Pulls the singleton row via Service.GetConfig
// (cached per slice 3.A2) and forwards the one field.
//
// Errors propagate verbatim. nil-Config arm is defensive only — the
// Service guarantees a non-nil row whenever GetConfig returns nil err.
type seriesFolderFormatAdapter struct {
	svc *namingconfig.Service
}

func (a seriesFolderFormatAdapter) GetSeriesFolderFormat(ctx context.Context) (string, error) {
	cfg, err := a.svc.GetConfig(ctx)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", nil
	}
	return cfg.SeriesFolderFormat, nil
}

// Compile-time guards.
var (
	_ rootfolders.NamingConfigProvider = seriesFolderFormatAdapter{}
	_ healthcheck.RootFolderResolver   = (*rootfolders.Service)(nil)
)

// enabledNotificationListerAdapter implements
// healthcheck.EnabledNotificationLister — the "list every enabled
// notification provider" projection the slice 4.AI
// NotificationStatusCheck consumes. Wraps notifications.Repo.All and
// applies the C# `NotificationDefinition.Enable` predicate (any
// per-event flag true) at the boundary.
//
// C# parallel: `NotificationStatusCheck.cs:27` —
// `_providerFactory.GetAvailableProviders()` returns the Active()
// subset; the Go port flattens to (ID, Name) since the healthcheck
// doesn't need anything else.
type enabledNotificationListerAdapter struct {
	repo *notifications.Repo
}

// newEnabledNotificationListerAdapter binds the dependency.
func newEnabledNotificationListerAdapter(repo *notifications.Repo) enabledNotificationListerAdapter {
	return enabledNotificationListerAdapter{repo: repo}
}

// EnabledNotifications implements healthcheck.EnabledNotificationLister.
// Applies the same "any per-event flag true" predicate the C#
// `NotificationDefinition.Enable` computed field at
// `Notifications/NotificationDefinition.cs:66` uses.
func (a enabledNotificationListerAdapter) EnabledNotifications(ctx context.Context) ([]healthcheck.EnabledNotification, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("notifications.All: %w", err)
	}
	out := make([]healthcheck.EnabledNotification, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if !notificationEnabled(row) {
			continue
		}
		out = append(out, healthcheck.EnabledNotification{ID: row.ID, Name: row.Name})
	}
	return out, nil
}

// notificationEnabled mirrors the C# `NotificationDefinition.Enable`
// computed-field at `Notifications/NotificationDefinition.cs:66` —
// the row is enabled iff ANY OnX flag is true.
func notificationEnabled(n *notifications.Notification) bool {
	if n == nil {
		return false
	}
	if n.OnGrab || n.OnDownload || n.OnRename || n.OnImportComplete ||
		n.OnSeriesAdd || n.OnSeriesDelete || n.OnEpisodeFileDelete ||
		n.OnEpisodeFileDeleteForUpgrade || n.OnHealthIssue ||
		n.OnHealthRestored || n.OnApplicationUpdate ||
		n.OnManualInteractionRequired {
		return true
	}
	if n.OnUpgrade != nil && *n.OnUpgrade {
		return true
	}
	return false
}
