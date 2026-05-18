package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/notifications"
)

// NotificationResource mirrors
// `src/Sonarr.Api.V3/Notifications/NotificationResource.cs`. Inherits
// providerResourceCore + the per-controller on-event flags + the
// includeHealthWarnings opt-in.
//
// Populated-row mapping (Tags expansion, polymorphic Settings via
// schema-builder) is DEFERRED — the empty-DB recording has zero rows
// so the unmapped fields are unobservable today. STATUS line 9
// carries the follow-up note.
type NotificationResource struct {
	providerResourceCore
	OnGrab                        bool `json:"onGrab"`
	OnDownload                    bool `json:"onDownload"`
	OnUpgrade                     bool `json:"onUpgrade"`
	OnRename                      bool `json:"onRename"`
	OnSeriesAdd                   bool `json:"onSeriesAdd"`
	OnSeriesDelete                bool `json:"onSeriesDelete"`
	OnEpisodeFileDelete           bool `json:"onEpisodeFileDelete"`
	OnEpisodeFileDeleteForUpgrade bool `json:"onEpisodeFileDeleteForUpgrade"`
	OnImportComplete              bool `json:"onImportComplete"`
	OnHealthIssue                 bool `json:"onHealthIssue"`
	OnHealthRestored              bool `json:"onHealthRestored"`
	OnApplicationUpdate           bool `json:"onApplicationUpdate"`
	OnManualInteractionRequired   bool `json:"onManualInteractionRequired"`
	IncludeHealthWarnings         bool `json:"includeHealthWarnings"`
}

// NotificationLister is the read side the handler depends on.
// *notifications.Repo satisfies it via the embedded
// repo.Repository[T].All.
type NotificationLister interface {
	All(ctx context.Context) ([]notifications.Notification, error)
}

// NotificationHandler returns the http.Handler for
// GET /api/v3/notification. Mirrors `NotificationController.GetAll`.
// Empty-DB emits `[]`; populated rows go through
// toNotificationResource which currently stubs Fields/InfoLink/
// Message/Presets (DEFERRED schema-builder port).
func NotificationHandler(lister NotificationLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notifications: %w", err), nil)
			return
		}
		out := make([]NotificationResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toNotificationResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toNotificationResource(n notifications.Notification) NotificationResource {
	tags := n.Tags
	if tags == nil {
		tags = []int{}
	}
	cfg := ""
	if n.ConfigContract != nil {
		cfg = *n.ConfigContract
	}
	upgrade := false
	if n.OnUpgrade != nil {
		upgrade = *n.OnUpgrade
	}
	return NotificationResource{
		providerResourceCore: providerResourceCore{
			ID:             n.ID,
			Name:           n.Name,
			Implementation: n.Implementation,
			// ImplementationName comes from the schema-builder
			// reflection (DEFERRED) — for now copy
			// Implementation as a pragmatic stub.
			ImplementationName: n.Implementation,
			ConfigContract:     cfg,
			Tags:               tags,
		},
		OnGrab:                        n.OnGrab,
		OnDownload:                    n.OnDownload,
		OnUpgrade:                     upgrade,
		OnRename:                      n.OnRename,
		OnSeriesAdd:                   n.OnSeriesAdd,
		OnSeriesDelete:                n.OnSeriesDelete,
		OnEpisodeFileDelete:           n.OnEpisodeFileDelete,
		OnEpisodeFileDeleteForUpgrade: n.OnEpisodeFileDeleteForUpgrade,
		OnImportComplete:              n.OnImportComplete,
		OnHealthIssue:                 n.OnHealthIssue,
		OnHealthRestored:              n.OnHealthRestored,
		OnApplicationUpdate:           n.OnApplicationUpdate,
		OnManualInteractionRequired:   n.OnManualInteractionRequired,
		IncludeHealthWarnings:         n.IncludeHealthWarnings,
	}
}
