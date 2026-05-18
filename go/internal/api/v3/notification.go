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

// NotificationTester invokes a per-row provider Test against the
// (Implementation, ConfigContract, Settings) trio. The cmd-side
// adapter wires this against [notifications.Registry.Construct] +
// [notifications.Provider.Test] under the host-supplied
// [notifications.Dependencies]. Tests can pass a stub returning the
// desired TestFailure list without dragging the registry or any
// concrete provider package into v3.
//
// An error return is reserved for "the provider couldn't be
// constructed" (unknown impl, malformed settings); these are
// projected to 400 with the C# unknown-implementation envelope. A
// successful Test with no failures returns (nil, nil). A non-empty
// failures slice projects to a 400 FluentValidation array.
type NotificationTester interface {
	Test(ctx context.Context, n *notifications.Notification) ([]notifications.TestFailure, error)
}

// NotificationTestHandler returns POST /api/v3/notification/test.
// Mirrors `ProviderControllerBase.Test([FromBody] resource, [FromQuery] forceTest=false)`
// at C# `ProviderControllerBase.cs:220-231`. The C#
// `[SkipValidation(true,false)]` attribute at :220 disables the
// PostValidator (Fields-not-null check) but keeps the per-Settings
// `Settings.Validate()` invocation that runs inside the provider's
// own Test() under the hood — the Go port mirrors that by skipping
// the [validateNotificationResource] call here and relying on the
// tester to surface settings-shape failures via [TestFailure]s.
//
// Wire shape: success returns 200 with `"{}"` (string-equal C# `return "{}"`).
// Failures project to a 400 FluentValidation array. An unknown
// implementation (Construct returns ErrNotRegistered) returns 400
// with a single PropertyName=Implementation failure to match the
// 5.C1a [validateNotificationResource] shape.
//
// The `?forceTest=` query parameter is currently parsed and ignored
// — the C# action only uses it to gate include-warnings behavior on
// the SharedValidator. With the SharedValidator skipped at this
// endpoint the flag is moot until 5.C1b grows a warning-vs-error
// distinction (DEFERRED).
func NotificationTestHandler(tester NotificationTester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeNotificationBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("notification: malformed body: %v", err))
			return
		}
		row, err := fromNotificationResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("notification: invalid fields: %v", err))
			return
		}
		failures, err := tester.Test(r.Context(), row)
		if err != nil {
			// Construct/setup errors are 400 with a synthesised
			// Implementation-field failure — matches the FluentValidation
			// envelope clients expect on the test endpoint. Distinguish
			// `notifications.ErrNotRegistered` from other unexpected errors
			// so future 500s have somewhere to land if they appear.
			if errors.Is(err, notifications.ErrNotRegistered) {
				httperr.WriteValidation(w, []httperr.Failure{{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q (configContract %q)", body.Implementation, body.ConfigContract),
					Severity:     "error",
				}})
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notification: test: %w", err), nil)
			return
		}
		if len(failures) > 0 {
			httperr.WriteValidation(w, testFailuresToFluentArray(failures))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// NotificationActioner invokes a per-row provider RequestAction. The
// cmd-side adapter wires this against
// [notifications.Registry.Construct] + [notifications.Provider.RequestAction]
// under the host-supplied [notifications.Dependencies]. Tests can
// pass a stub.
//
// The C# action map is per-provider: Plex (startOAuth, continueOAuth,
// getOAuthToken), Twitter (oauth1 PIN), PushBullet (getDevices), etc.
// The Go port treats each action as an opaque string and lets the
// provider decode it.
//
// Return shape: any JSON-marshalable value; nil → emits `null`.
type NotificationActioner interface {
	RequestAction(ctx context.Context, n *notifications.Notification, action string, query map[string]string) (any, error)
}

// NotificationRequestActionHandler returns POST /api/v3/notification/action/{name}.
// Mirrors `ProviderControllerBase.RequestAction` at C# :259-273. The
// C# `[SkipValidation]` attribute at :259 disables the entire
// SharedValidator + PostValidator pair — the Go port skips the
// `validateNotificationResource` call to match.
//
// Wire shape: body is a [NotificationResource]; route carries the
// action name; query parameters are passed through to the provider
// as a `map[string]string` (matches C#
// `Request.Query.ToDictionary(x => x.Key, x => x.Value.ToString())`).
// Response is the provider's returned JSON value (Content-Type set
// to application/json regardless of the value's underlying type).
//
// Error projection mirrors the test endpoint: ErrNotRegistered → 400
// with single Implementation failure; other errors → 500. Empty
// action name → 400 BadRequest. The C# action requires a non-empty
// `{name}` route segment too (ASP.NET routing won't match `:259` for
// `/action/` with empty name).
func NotificationRequestActionHandler(actioner NotificationActioner) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if strings.TrimSpace(name) == "" {
			httperr.BadRequest(w, "notification/action: empty name")
			return
		}
		body, err := decodeNotificationBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("notification: malformed body: %v", err))
			return
		}
		row, err := fromNotificationResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("notification: invalid fields: %v", err))
			return
		}
		query := make(map[string]string, len(r.URL.Query()))
		for k, vs := range r.URL.Query() {
			if len(vs) > 0 {
				// C# `Request.Query.ToDictionary` collapses multi-valued
				// query params via `x.Value.ToString()` which on
				// `Microsoft.Extensions.Primitives.StringValues` emits
				// comma-joined values. The Go port takes the first value
				// — the C#-side behaviour is rarely meaningful (only
				// `?action=a&action=b` triggers it, and provider actions
				// don't use multi-valued params today).
				query[k] = vs[0]
			}
		}
		data, err := actioner.RequestAction(r.Context(), row, name, query)
		if err != nil {
			if errors.Is(err, notifications.ErrNotRegistered) {
				httperr.WriteValidation(w, []httperr.Failure{{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q (configContract %q)", body.Implementation, body.ConfigContract),
					Severity:     "error",
				}})
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notification: action %q: %w", name, err), nil)
			return
		}
		buf, err := json.Marshal(data)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notification: action %q: marshal: %w", name, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	})
}

// ProviderTestAllResult mirrors C# `Sonarr.Api.V3.ProviderTestAllResult`
// at `src/Sonarr.Api.V3/ProviderTestAllResult.cs`. The C# `IsValid`
// is a derived property (`ValidationFailures.Empty()`); the Go port
// computes it at projection time and emits it as a regular field so
// the wire shape is byte-equal.
//
// validationFailures defaults to `[]` (non-nil) so JSON marshalling
// matches the C# `new List<ValidationFailure>()` ctor default.
type ProviderTestAllResult struct {
	ID                 int               `json:"id"`
	IsValid            bool              `json:"isValid"`
	ValidationFailures []httperr.Failure `json:"validationFailures"`
}

// NotificationTestAllHandler returns POST /api/v3/notification/testall.
// Mirrors `ProviderControllerBase.TestAll` at C# :233-257. Iterates
// every persisted row and runs the per-row provider Test sequentially.
// Aggregates the outcomes into a `ProviderTestAllResult` array.
// Returns 400 when any row's Test returned at least one failure, 200
// when every row passed (mirrors C# `result.Any(c => !c.IsValid) ?
// BadRequest(result) : Ok(result)` at :256).
//
// C# filters the iteration to `c.Settings.Validate().IsValid && c.Enable`
// — the Settings filter is moot once Provider.Test is the source of
// truth (a settings-invalid row will surface that as a TestFailure),
// and the Go port's [notifications.Notification] has no Enable
// column. Every row is tested; empty-DB returns `[]` either way which
// preserves the byte-equal contract.
//
// Parallelism: C# is sequential, Go port matches. Per-row
// concurrency is a perf optimization that can land in a later slice
// once the empty-DB byte-equal contract has a populated counterpart
// to compare against.
func NotificationTestAllHandler(lister NotificationLister, tester NotificationTester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("notification/testall: list: %w", err), nil)
			return
		}
		results := make([]ProviderTestAllResult, 0, len(rows))
		anyInvalid := false
		for _, row := range rows {
			row := row
			failures, err := tester.Test(r.Context(), &row)
			projected := make([]httperr.Failure, 0)
			switch {
			case errors.Is(err, notifications.ErrNotRegistered):
				projected = append(projected, httperr.Failure{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q", row.Implementation),
					Severity:     "error",
				})
			case err != nil:
				// Unexpected per-row error — surface it as a whole-form
				// failure so the row reports IsValid=false rather than
				// collapsing the entire response. Matches C# where any
				// thrown exception inside the foreach is caught by the
				// surrounding pipeline and a fallback failure is added
				// (see `NotificationFactory.cs:210-229`).
				projected = append(projected, httperr.Failure{
					PropertyName: "",
					ErrorMessage: fmt.Sprintf("Test failed: %v", err),
					Severity:     "error",
				})
			default:
				projected = append(projected, testFailuresToFluentArray(failures)...)
			}
			isValid := len(projected) == 0
			if !isValid {
				anyInvalid = true
			}
			results = append(results, ProviderTestAllResult{
				ID:                 row.ID,
				IsValid:            isValid,
				ValidationFailures: projected,
			})
		}
		status := http.StatusOK
		if anyInvalid {
			status = http.StatusBadRequest
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		buf, _ := json.Marshal(results)
		_, _ = w.Write(buf)
	})
}

// testFailuresToFluentArray ports the projection
// `STJson.ToJson(validationException.Errors)` at C#
// `SonarrErrorPipeline.cs:52` for the [notifications.TestFailure]
// shape. The DetailedDescription field has no FluentValidation
// equivalent in `httperr.Failure` — it's dropped here pending a
// shared cross-family failure type. Severity defaults to "error" via
// the [httperr.WriteValidation] helper.
func testFailuresToFluentArray(failures []notifications.TestFailure) []httperr.Failure {
	out := make([]httperr.Failure, 0, len(failures))
	for _, f := range failures {
		out = append(out, httperr.Failure{
			PropertyName: f.PropertyName,
			ErrorMessage: f.ErrorMessage,
			Severity:     "error",
		})
	}
	return out
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
