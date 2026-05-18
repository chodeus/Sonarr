package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/signalr"
)

// NotificationByIDGetter narrows the read for /notification/{id}.
type NotificationByIDGetter interface {
	Get(ctx context.Context, id int) (*notifications.Notification, error)
}

// NotificationByIDHandler returns GET /api/v3/notification/{id}.
func NotificationByIDHandler(svc NotificationByIDGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "notification: invalid id")
			return
		}
		row, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, notifications.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("notification %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notification: get %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(toNotificationResource(*row))
		_, _ = w.Write(buf)
	})
}

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
	OnGrab                        bool            `json:"onGrab"`
	OnDownload                    bool            `json:"onDownload"`
	OnUpgrade                     bool            `json:"onUpgrade"`
	OnRename                      bool            `json:"onRename"`
	OnSeriesAdd                   bool            `json:"onSeriesAdd"`
	OnSeriesDelete                bool            `json:"onSeriesDelete"`
	OnEpisodeFileDelete           bool            `json:"onEpisodeFileDelete"`
	OnEpisodeFileDeleteForUpgrade bool            `json:"onEpisodeFileDeleteForUpgrade"`
	OnImportComplete              bool            `json:"onImportComplete"`
	OnHealthIssue                 bool            `json:"onHealthIssue"`
	OnHealthRestored              bool            `json:"onHealthRestored"`
	OnApplicationUpdate           bool            `json:"onApplicationUpdate"`
	OnManualInteractionRequired   bool            `json:"onManualInteractionRequired"`
	IncludeHealthWarnings         bool            `json:"includeHealthWarnings"`
	Fields                        []ProviderField `json:"fields,omitempty"`
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

// NotificationWriter is the CUD surface for the POST/PUT/DELETE
// handlers. *notifications.Repo satisfies this via the embedded
// repo.Repository[Notification] (Insert/Update/Delete) + the
// inherited Get + All from the same generic.
type NotificationWriter interface {
	Get(ctx context.Context, id int) (*notifications.Notification, error)
	All(ctx context.Context) ([]notifications.Notification, error)
	Insert(ctx context.Context, n *notifications.Notification) (*notifications.Notification, error)
	Update(ctx context.Context, n *notifications.Notification) error
	Delete(ctx context.Context, id int) error
}

// NotificationEmitter mirrors the subset of
// *signalr.ResourceBroadcaster[NotificationResource] the write
// handlers actually call. Created/Updated/Deleted match the C#
// `BroadcastResourceChange` calls fired by the
// `RestControllerWithSignalR` base + the
// `ProviderControllerBase.Handle(ProviderXxxEvent)` shims at
// `:276-291`. The Go port collapses the C# event-bus indirection
// (ProviderFactory → ProviderAddedEvent → IHandle → BroadcastSignalR)
// into a direct emitter call from the handler — there's no other
// consumer of those provider events in the port today.
type NotificationEmitter interface {
	Created(ctx context.Context, resource NotificationResource) error
	Updated(ctx context.Context, resource NotificationResource) error
	Deleted(ctx context.Context, resource NotificationResource) error
}

// NotificationRegistryChecker narrows the implementation/configContract
// lookup the write handlers run before persisting. *notifications.Registry
// satisfies it via Has. Tests can pass an in-memory map.
type NotificationRegistryChecker interface {
	Has(implementation, configContract string) bool
}

// NotificationCreateHandler returns POST /api/v3/notification.
// Mirrors `ProviderControllerBase.CreateProvider` at
// `:81-93`. Returns 201 + the persisted resource. SignalR Created
// fires after Insert succeeds.
//
// Validation chain matches the C# FluentValidation rules at `:44-49`:
//
//   - Name non-empty + unique (case-insensitive) across existing rows.
//   - Implementation non-empty + registered in the Registry under
//     (Implementation, ConfigContract).
//   - ConfigContract non-empty.
//
// The C# `Test()` call before Insert (gated by `Enable` + `!forceSave`)
// is DEFERRED to slice 5.C1b — the Test endpoint hasn't ported yet.
// 5.C1a accepts all valid rows; passing `?forceSave=true` is a no-op
// today but the param is preserved for forward-compat.
func NotificationCreateHandler(svc NotificationWriter, reg NotificationRegistryChecker, emitter NotificationEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeNotificationBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("notification: malformed body: %v", err))
			return
		}
		if failures := validateNotificationResource(r.Context(), body, 0, svc, reg); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromNotificationResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("notification: invalid fields: %v", err))
			return
		}
		saved, err := svc.Insert(r.Context(), row)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notification: insert: %w", err), nil)
			return
		}
		res := toNotificationResource(*saved)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "notification", "created", func() error {
			return emitter.Created(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// NotificationUpdateHandler returns PUT /api/v3/notification/{id}.
// Mirrors `ProviderControllerBase.UpdateProvider(id, resource)` at
// `:98-125`. The C# action falls back to the body `id` if the route
// id doesn't match an existing row — the Go port preserves that
// fallback under the same comment ("// TODO: Remove fallback in next
// API version bump"). Returns 202 Accepted + the persisted resource
// (matches C# `Accepted(existingDefinition.Id)`).
//
// Tag mutation rules (Add/Remove/Replace from BulkResource.ApplyTags)
// only apply to PUT /bulk — slice 5.C1e — so this handler treats Tags
// as a straight replace, matching the single-row PUT semantics.
//
// SignalR Updated fires regardless of whether the row content
// actually changed; the C# pipeline does the same (its
// `hasDefinitionChanged` short-circuit only gates the DB Update call,
// not the BroadcastResourceChange that the `IHandle<ProviderUpdated>`
// shim fires).
func NotificationUpdateHandler(svc NotificationWriter, reg NotificationRegistryChecker, emitter NotificationEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "notification: invalid id")
			return
		}
		body, err := decodeNotificationBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("notification: malformed body: %v", err))
			return
		}
		existing, err := svc.Get(r.Context(), id)
		if err != nil && !errors.Is(err, notifications.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notification: get %d: %w", id, err), nil)
			return
		}
		if errors.Is(err, notifications.ErrNotFound) {
			// C# fallback: try body.Id when route id misses. Only
			// applies when body.Id > 0 AND a row exists for it.
			if body.ID > 0 && body.ID != id {
				if e2, e := svc.Get(r.Context(), body.ID); e == nil {
					existing = e2
					id = body.ID
				}
			}
		}
		if existing == nil {
			httperr.NotFound(w, fmt.Sprintf("notification %d not found", id))
			return
		}
		if failures := validateNotificationResource(r.Context(), body, id, svc, reg); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromNotificationResource(body, existing)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("notification: invalid fields: %v", err))
			return
		}
		row.ID = id
		if err := svc.Update(r.Context(), row); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notification: update %d: %w", id, err), nil)
			return
		}
		res := toNotificationResource(*row)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "notification", "updated", func() error {
			return emitter.Updated(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// NotificationDeleteHandler returns DELETE /api/v3/notification/{id}.
// Mirrors `ProviderControllerBase.DeleteProvider` at `:181-186`.
// Returns 200 with `{}` body (matches the C# `return new { };`).
// SignalR Deleted fires with a minimal resource carrying only the
// id (matches `RestControllerWithSignalR` behavior where the deleted
// resource is projected to `{Id = id}`).
func NotificationDeleteHandler(svc NotificationWriter, emitter NotificationEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "notification: invalid id")
			return
		}
		err = svc.Delete(r.Context(), id)
		if errors.Is(err, notifications.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("notification %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notification: delete %d: %w", id, err), nil)
			return
		}
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "notification", "deleted", func() error {
			return emitter.Deleted(r.Context(), NotificationResource{providerResourceCore: providerResourceCore{ID: id}})
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeNotificationBody(r *http.Request) (NotificationResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out NotificationResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return NotificationResource{}, err
	}
	return out, nil
}

// validateNotificationResource ports the SharedValidator rules from
// `ProviderControllerBase.cs:44-49`. `existingID` is 0 for POST and
// the row id for PUT — used to skip the self-row in the name-unique
// check so a PUT that preserves Name doesn't 400 itself.
func validateNotificationResource(ctx context.Context, body NotificationResource, existingID int, svc NotificationWriter, reg NotificationRegistryChecker) []httperr.Failure {
	failures := make([]httperr.Failure, 0, 4)
	if strings.TrimSpace(body.Name) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "Name", ErrorMessage: "'Name' must not be empty.", Severity: "error"})
	}
	if strings.TrimSpace(body.Implementation) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "Implementation", ErrorMessage: "'Implementation' must not be empty.", Severity: "error"})
	}
	if strings.TrimSpace(body.ConfigContract) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "ConfigContract", ErrorMessage: "'Config Contract' must not be empty.", Severity: "error"})
	}
	// Registry membership check — equivalent to the
	// `_providerFactory.GetDefaultDefinitions` enumeration the C# tree
	// does implicitly when constructing the Settings instance from
	// the (Implementation, ConfigContract) pair.
	if body.Implementation != "" && body.ConfigContract != "" && reg != nil && !reg.Has(body.Implementation, body.ConfigContract) {
		failures = append(failures, httperr.Failure{
			PropertyName: "Implementation",
			ErrorMessage: fmt.Sprintf("Unknown implementation %q (configContract %q)", body.Implementation, body.ConfigContract),
			Severity:     "error",
		})
	}
	if body.Name != "" {
		rows, err := svc.All(ctx)
		if err == nil {
			for _, existing := range rows {
				if existing.ID == existingID {
					continue
				}
				if strings.EqualFold(existing.Name, body.Name) {
					failures = append(failures, httperr.Failure{PropertyName: "Name", ErrorMessage: "Should be unique", Severity: "error"})
					break
				}
			}
		}
	}
	return failures
}

// fromNotificationResource is the inverse of toNotificationResource.
// On PUT, `existing` is the persisted row — its Settings JSON is the
// fallback when the request body omits `fields[]` (matches the C#
// `_resourceMapper.ToModel(resource, existingDefinition)` contract
// at `:170` which keeps existing settings when the resource doesn't
// re-supply them).
//
// The OnUpgrade + ConfigContract nullable pointers in the DB row
// mirror C# nullable-bool / nullable-string semantics — POST/PUT
// always set them to non-nil values so the wire shape on round-trip
// is consistent.
func fromNotificationResource(res NotificationResource, existing *notifications.Notification) (*notifications.Notification, error) {
	settings := "{}"
	if res.Fields != nil {
		s, err := settingsJSONFromFields(res.Fields)
		if err != nil {
			return nil, err
		}
		settings = s
	} else if existing != nil {
		settings = existing.Settings
	}
	cfg := res.ConfigContract
	tags := res.Tags
	if tags == nil {
		tags = []int{}
	}
	upgrade := res.OnUpgrade
	return &notifications.Notification{
		ID:                            res.ID,
		Name:                          res.Name,
		OnGrab:                        res.OnGrab,
		OnDownload:                    res.OnDownload,
		Settings:                      settings,
		Implementation:                res.Implementation,
		ConfigContract:                &cfg,
		OnUpgrade:                     &upgrade,
		Tags:                          tags,
		OnRename:                      res.OnRename,
		OnHealthIssue:                 res.OnHealthIssue,
		IncludeHealthWarnings:         res.IncludeHealthWarnings,
		OnSeriesDelete:                res.OnSeriesDelete,
		OnEpisodeFileDelete:           res.OnEpisodeFileDelete,
		OnEpisodeFileDeleteForUpgrade: res.OnEpisodeFileDeleteForUpgrade,
		OnApplicationUpdate:           res.OnApplicationUpdate,
		OnManualInteractionRequired:   res.OnManualInteractionRequired,
		OnSeriesAdd:                   res.OnSeriesAdd,
		OnHealthRestored:              res.OnHealthRestored,
		OnImportComplete:              res.OnImportComplete,
	}, nil
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
