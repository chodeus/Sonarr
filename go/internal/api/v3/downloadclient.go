package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/signalr"
)

// DownloadClientByIDGetter narrows the read for /downloadclient/{id}.
type DownloadClientByIDGetter interface {
	Get(ctx context.Context, id int) (*downloadclients.DownloadClient, error)
}

// DownloadClientByIDHandler returns GET /api/v3/downloadclient/{id}.
func DownloadClientByIDHandler(svc DownloadClientByIDGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "downloadclient: invalid id")
			return
		}
		row, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, downloadclients.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("downloadclient %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient: get %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(toDownloadClientResource(*row))
		_, _ = w.Write(buf)
	})
}

// DownloadClientResource mirrors
// `src/Sonarr.Api.V3/DownloadClient/DownloadClientResource.cs`.
// Embeds providerResourceCore + Enable, Protocol, Priority,
// RemoveCompletedDownloads, RemoveFailedDownloads.
//
// Protocol comes from the download-client-factory introspection in
// C# (DownloadProtocol enum-as-string), which the Go
// downloadclients.DownloadClient struct doesn't carry. Stubbed
// "unknown" until the factory port lands — DEFERRED.
type DownloadClientResource struct {
	providerResourceCore
	Enable                   bool            `json:"enable"`
	Protocol                 string          `json:"protocol"`
	Priority                 int             `json:"priority"`
	RemoveCompletedDownloads bool            `json:"removeCompletedDownloads"`
	RemoveFailedDownloads    bool            `json:"removeFailedDownloads"`
	Fields                   []ProviderField `json:"fields,omitempty"`
}

// DownloadClientLister is the read side the handler depends on.
type DownloadClientLister interface {
	All(ctx context.Context) ([]downloadclients.DownloadClient, error)
}

// DownloadClientHandler returns the http.Handler for
// GET /api/v3/downloadclient. Mirrors
// `DownloadClientController.GetAll`.
func DownloadClientHandler(lister DownloadClientLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclients: %w", err), nil)
			return
		}
		out := make([]DownloadClientResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toDownloadClientResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// DownloadClientWriter is the CUD surface. *downloadclients.Repo
// satisfies via the embedded repo.Repository[DownloadClient].
type DownloadClientWriter interface {
	Get(ctx context.Context, id int) (*downloadclients.DownloadClient, error)
	All(ctx context.Context) ([]downloadclients.DownloadClient, error)
	Insert(ctx context.Context, n *downloadclients.DownloadClient) (*downloadclients.DownloadClient, error)
	Update(ctx context.Context, n *downloadclients.DownloadClient) error
	Delete(ctx context.Context, id int) error
}

// DownloadClientEmitter mirrors the
// *signalr.ResourceBroadcaster[DownloadClientResource] subset the
// write handlers call.
type DownloadClientEmitter interface {
	Created(ctx context.Context, resource DownloadClientResource) error
	Updated(ctx context.Context, resource DownloadClientResource) error
	Deleted(ctx context.Context, resource DownloadClientResource) error
}

// DownloadClientCreateHandler returns POST /api/v3/downloadclient.
// Same shape as IndexerCreateHandler / ImportListCreateHandler.
// Registry membership check deferred to 5.C4b's test endpoint.
func DownloadClientCreateHandler(svc DownloadClientWriter, emitter DownloadClientEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeDownloadClientBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient: malformed body: %v", err))
			return
		}
		if failures := validateDownloadClientResource(r.Context(), body, 0, svc); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromDownloadClientResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient: invalid fields: %v", err))
			return
		}
		saved, err := svc.Insert(r.Context(), row)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient: insert: %w", err), nil)
			return
		}
		res := toDownloadClientResource(*saved)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "downloadclient", "created", func() error {
			return emitter.Created(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// DownloadClientUpdateHandler returns PUT /api/v3/downloadclient/{id}.
func DownloadClientUpdateHandler(svc DownloadClientWriter, emitter DownloadClientEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "downloadclient: invalid id")
			return
		}
		body, err := decodeDownloadClientBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient: malformed body: %v", err))
			return
		}
		existing, err := svc.Get(r.Context(), id)
		if err != nil && !errors.Is(err, downloadclients.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient: get %d: %w", id, err), nil)
			return
		}
		if errors.Is(err, downloadclients.ErrNotFound) {
			if body.ID > 0 && body.ID != id {
				if e2, e := svc.Get(r.Context(), body.ID); e == nil {
					existing = e2
					id = body.ID
				}
			}
		}
		if existing == nil {
			httperr.NotFound(w, fmt.Sprintf("downloadclient %d not found", id))
			return
		}
		if failures := validateDownloadClientResource(r.Context(), body, id, svc); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromDownloadClientResource(body, existing)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient: invalid fields: %v", err))
			return
		}
		row.ID = id
		if err := svc.Update(r.Context(), row); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient: update %d: %w", id, err), nil)
			return
		}
		res := toDownloadClientResource(*row)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "downloadclient", "updated", func() error {
			return emitter.Updated(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// DownloadClientDeleteHandler returns DELETE /api/v3/downloadclient/{id}.
func DownloadClientDeleteHandler(svc DownloadClientWriter, emitter DownloadClientEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "downloadclient: invalid id")
			return
		}
		// Idempotent DELETE — C.1.i-follow-4 alignment (J232). C# returns 200 + `{}` on missing id.
		// SignalR Deleted broadcast only fires on actual delete (not on missing-id swallow).
		err = svc.Delete(r.Context(), id)
		if err != nil && !errors.Is(err, downloadclients.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient: delete %d: %w", id, err), nil)
			return
		}
		if err == nil {
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "downloadclient", "deleted", func() error {
				return emitter.Deleted(r.Context(), DownloadClientResource{providerResourceCore: providerResourceCore{ID: id}})
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeDownloadClientBody(r *http.Request) (DownloadClientResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out DownloadClientResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return DownloadClientResource{}, err
	}
	return out, nil
}

func validateDownloadClientResource(ctx context.Context, body DownloadClientResource, existingID int, svc DownloadClientWriter) []httperr.Failure {
	failures := make([]httperr.Failure, 0, 3)
	if strings.TrimSpace(body.Name) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "Name", ErrorMessage: "'Name' must not be empty.", Severity: "error"})
	}
	if strings.TrimSpace(body.Implementation) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "Implementation", ErrorMessage: "'Implementation' must not be empty.", Severity: "error"})
	}
	if strings.TrimSpace(body.ConfigContract) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "ConfigContract", ErrorMessage: "'Config Contract' must not be empty.", Severity: "error"})
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

// fromDownloadClientResource is the inverse of
// toDownloadClientResource. Note: DownloadClient's Settings +
// ConfigContract are plain `string` (not *string like indexer +
// importlist) — the DB schema makes them NOT NULL.
func fromDownloadClientResource(res DownloadClientResource, existing *downloadclients.DownloadClient) (*downloadclients.DownloadClient, error) {
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
	tags := res.Tags
	if tags == nil {
		tags = []int{}
	}
	return &downloadclients.DownloadClient{
		ID:                       res.ID,
		Enable:                   res.Enable,
		Name:                     res.Name,
		Implementation:           res.Implementation,
		Settings:                 settings,
		ConfigContract:           res.ConfigContract,
		Priority:                 res.Priority,
		RemoveCompletedDownloads: res.RemoveCompletedDownloads,
		RemoveFailedDownloads:    res.RemoveFailedDownloads,
		Tags:                     tags,
	}, nil
}

// DownloadClientTester invokes a per-row provider Test. The cmd-side
// adapter wires this against downloadclients.Registry.Construct +
// downloadclients.Provider.Test under the host-supplied
// downloadclients.Dependencies.
//
// Production Test works TODAY for Sabnzbd + Transmission impls;
// other 18+ schema-only impls (qBittorrent/Deluge/NZBGet/rTorrent/
// uTorrent/Vuze/...) return ErrNotRegistered until the 5.G slices
// port them.
type DownloadClientTester interface {
	Test(ctx context.Context, n *downloadclients.DownloadClient) ([]downloadclients.TestFailure, error)
}

// DownloadClientTestHandler returns POST /api/v3/downloadclient/test.
// Same shape as the 5.C1b/5.C2b/5.C3b twins: 200 `{}` on success,
// 400 FluentValidation array on failures, 400 single-Implementation
// failure on ErrNotRegistered, 500 on transport errors.
//
// SharedValidator skipped (matches C# `[SkipValidation(true,false)]`).
func DownloadClientTestHandler(tester DownloadClientTester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeDownloadClientBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient: malformed body: %v", err))
			return
		}
		row, err := fromDownloadClientResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient: invalid fields: %v", err))
			return
		}
		failures, err := tester.Test(r.Context(), row)
		if err != nil {
			if errors.Is(err, downloadclients.ErrNotRegistered) {
				httperr.WriteValidation(w, []httperr.Failure{{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q (configContract %q)", body.Implementation, body.ConfigContract),
					Severity:     "error",
				}})
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient: test: %w", err), nil)
			return
		}
		if len(failures) > 0 {
			httperr.WriteValidation(w, downloadClientTestFailuresToFluentArray(failures))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// DownloadClientBulkResource mirrors C# `DownloadClientBulkResource`
// at `src/Sonarr.Api.V3/DownloadClient/DownloadClientBulkResource.cs`.
// Four per-controller optional fields plus the inherited
// Ids/Tags/ApplyTags.
//
// All booleans + Priority are pointer-typed so null = "not supplied"
// (matches C# nullable shapes at :8-11 + the null-coalesce mapper at
// :26-30).
//
// applyTagsAction (defined in indexer.go for 5.C2e) is family-
// agnostic and reused here.
type DownloadClientBulkResource struct {
	IDs                      []int           `json:"ids"`
	Tags                     []int           `json:"tags"`
	ApplyTags                applyTagsAction `json:"applyTags"`
	Enable                   *bool           `json:"enable,omitempty"`
	Priority                 *int            `json:"priority,omitempty"`
	RemoveCompletedDownloads *bool           `json:"removeCompletedDownloads,omitempty"`
	RemoveFailedDownloads    *bool           `json:"removeFailedDownloads,omitempty"`
}

// DownloadClientBulkUpdateHandler returns PUT /api/v3/downloadclient/bulk.
// Mirrors `ProviderControllerBase.UpdateProvider(TBulkProviderResource)`
// at C# :130-166 + `DownloadClientBulkResourceMapper.UpdateModel` at
// `DownloadClientBulkResource.cs:17-33`.
func DownloadClientBulkUpdateHandler(svc DownloadClientWriter, emitter DownloadClientEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeDownloadClientBulkBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient/bulk: malformed body: %v", err))
			return
		}
		if len(body.IDs) == 0 {
			httperr.BadRequest(w, "ids must be provided")
			return
		}
		updated := make([]DownloadClientResource, 0, len(body.IDs))
		for _, id := range body.IDs {
			existing, err := svc.Get(r.Context(), id)
			if errors.Is(err, downloadclients.ErrNotFound) {
				continue
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient/bulk: get %d: %w", id, err), nil)
				return
			}
			applyDownloadClientBulkUpdate(existing, body)
			if err := svc.Update(r.Context(), existing); err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient/bulk: update %d: %w", id, err), nil)
				return
			}
			res := toDownloadClientResource(*existing)
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "downloadclient", "updated", func() error {
				return emitter.Updated(r.Context(), res)
			})
			updated = append(updated, res)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(updated)
		_, _ = w.Write(buf)
	})
}

// DownloadClientBulkDeleteHandler returns DELETE /api/v3/downloadclient/bulk.
func DownloadClientBulkDeleteHandler(svc DownloadClientWriter, emitter DownloadClientEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeDownloadClientBulkBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient/bulk: malformed body: %v", err))
			return
		}
		if len(body.IDs) == 0 {
			httperr.BadRequest(w, "ids must be provided")
			return
		}
		for _, id := range body.IDs {
			err := svc.Delete(r.Context(), id)
			if errors.Is(err, downloadclients.ErrNotFound) {
				continue
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient/bulk: delete %d: %w", id, err), nil)
				return
			}
			id := id
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "downloadclient", "deleted", func() error {
				return emitter.Deleted(r.Context(), DownloadClientResource{providerResourceCore: providerResourceCore{ID: id}})
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeDownloadClientBulkBody(r *http.Request) (DownloadClientBulkResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out DownloadClientBulkResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return DownloadClientBulkResource{}, err
	}
	return out, nil
}

// applyDownloadClientBulkUpdate mutates `existing` per the
// [DownloadClientBulkResource] semantics. Mirrors C#
// `DownloadClientBulkResourceMapper.UpdateModel` at :17-33 + the
// inherited Tags-apply switch at :148-159.
func applyDownloadClientBulkUpdate(existing *downloadclients.DownloadClient, body DownloadClientBulkResource) {
	if body.Tags != nil {
		switch body.ApplyTags {
		case applyTagsReplace:
			existing.Tags = append([]int(nil), body.Tags...)
		case applyTagsAdd:
			for _, t := range body.Tags {
				if !containsInt(existing.Tags, t) {
					existing.Tags = append(existing.Tags, t)
				}
			}
		case applyTagsRemove:
			out := existing.Tags[:0]
			for _, t := range existing.Tags {
				if !containsInt(body.Tags, t) {
					out = append(out, t)
				}
			}
			existing.Tags = out
		}
	}
	if body.Enable != nil {
		existing.Enable = *body.Enable
	}
	if body.Priority != nil {
		existing.Priority = *body.Priority
	}
	if body.RemoveCompletedDownloads != nil {
		existing.RemoveCompletedDownloads = *body.RemoveCompletedDownloads
	}
	if body.RemoveFailedDownloads != nil {
		existing.RemoveFailedDownloads = *body.RemoveFailedDownloads
	}
}

// DownloadClientActioner invokes a per-row provider RequestAction.
// The cmd-side adapter wires this against
// downloadclients.Registry.Construct + downloadclients.Provider.RequestAction
// under host-supplied downloadclients.Dependencies.
//
// Common downloadclient actions: Sabnzbd `getCategories` /
// `getCustomCategories` for category-picker enumeration; Transmission
// `getCustomTorrentFolders` for writable-destination enumeration.
type DownloadClientActioner interface {
	RequestAction(ctx context.Context, n *downloadclients.DownloadClient, action string, query map[string]string) (any, error)
}

// DownloadClientRequestActionHandler returns POST /api/v3/downloadclient/action/{name}.
// Same shape as the 5.C1d/5.C2d/5.C3d twins.
func DownloadClientRequestActionHandler(actioner DownloadClientActioner) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if strings.TrimSpace(name) == "" {
			httperr.BadRequest(w, "downloadclient/action: empty name")
			return
		}
		body, err := decodeDownloadClientBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient: malformed body: %v", err))
			return
		}
		row, err := fromDownloadClientResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("downloadclient: invalid fields: %v", err))
			return
		}
		query := make(map[string]string, len(r.URL.Query()))
		for k, vs := range r.URL.Query() {
			if len(vs) > 0 {
				query[k] = vs[0]
			}
		}
		data, err := actioner.RequestAction(r.Context(), row, name, query)
		if err != nil {
			if errors.Is(err, downloadclients.ErrNotRegistered) {
				httperr.WriteValidation(w, []httperr.Failure{{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q (configContract %q)", body.Implementation, body.ConfigContract),
					Severity:     "error",
				}})
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient: action %q: %w", name, err), nil)
			return
		}
		buf, err := json.Marshal(data)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient: action %q: marshal: %w", name, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	})
}

// DownloadClientTestAllHandler returns POST /api/v3/downloadclient/testall.
// Mirrors C# ProviderControllerBase.TestAll at :233-257 and the
// 5.C1c / 5.C2c / 5.C3c twins. Returns 400 if any row failed, 200
// if all passed. Empty list returns [].
func DownloadClientTestAllHandler(lister DownloadClientLister, tester DownloadClientTester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("downloadclient/testall: list: %w", err), nil)
			return
		}
		results := make([]ProviderTestAllResult, 0, len(rows))
		anyInvalid := false
		for _, row := range rows {
			row := row
			failures, err := tester.Test(r.Context(), &row)
			projected := make([]httperr.Failure, 0)
			switch {
			case errors.Is(err, downloadclients.ErrNotRegistered):
				projected = append(projected, httperr.Failure{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q", row.Implementation),
					Severity:     "error",
				})
			case err != nil:
				projected = append(projected, httperr.Failure{
					PropertyName: "",
					ErrorMessage: fmt.Sprintf("Test failed: %v", err),
					Severity:     "error",
				})
			default:
				projected = append(projected, downloadClientTestFailuresToFluentArray(failures)...)
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

// downloadClientTestFailuresToFluentArray projects the family's
// TestFailure to httperr.Failure.
func downloadClientTestFailuresToFluentArray(failures []downloadclients.TestFailure) []httperr.Failure {
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

func toDownloadClientResource(d downloadclients.DownloadClient) DownloadClientResource {
	tags := d.Tags
	if tags == nil {
		tags = []int{}
	}
	return DownloadClientResource{
		providerResourceCore: providerResourceCore{
			ID:                 d.ID,
			Name:               d.Name,
			Implementation:     d.Implementation,
			ImplementationName: implementationNameOr(downloadClientImplNames, d.Implementation),
			ConfigContract:     d.ConfigContract,
			Tags:               tags,
		},
		Fields:                   fieldsFromSettingsJSON(d.Settings), // G4 (J425)
		Enable:                   d.Enable,
		Protocol:                 "unknown", // DEFERRED: factory introspection
		Priority:                 d.Priority,
		RemoveCompletedDownloads: d.RemoveCompletedDownloads,
		RemoveFailedDownloads:    d.RemoveFailedDownloads,
	}
}
