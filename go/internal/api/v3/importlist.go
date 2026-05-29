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
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/signalr"
)

// ImportListResource mirrors
// `src/Sonarr.Api.V3/ImportLists/ImportListResource.cs`. Embeds the
// providerResourceCore + the per-import-list configuration knobs.
//
// Populated-row mapping is partial — fields like ListType, ListOrder,
// MinRefreshInterval come from the import-list-factory introspection
// in C# which the Go importlists.Definition layer doesn't expose
// yet. Empty-DB recording has zero rows so the gaps are unobservable;
// flagged DEFERRED in STATUS.
type ImportListResource struct {
	providerResourceCore
	EnableAutomaticAdd       bool            `json:"enableAutomaticAdd"`
	SearchForMissingEpisodes bool            `json:"searchForMissingEpisodes"`
	ShouldMonitor            int             `json:"shouldMonitor"`
	MonitorNewItems          int             `json:"monitorNewItems"`
	RootFolderPath           string          `json:"rootFolderPath"`
	QualityProfileID         int             `json:"qualityProfileId"`
	SeriesType               int             `json:"seriesType"`
	SeasonFolder             bool            `json:"seasonFolder"`
	Fields                   []ProviderField `json:"fields,omitempty"`
}

// ImportListLister is the read side the handler depends on.
type ImportListLister interface {
	All(ctx context.Context) ([]importlists.ImportList, error)
}

// ImportListByIDGetter narrows the read for GET /api/v3/importlist/{id}.
type ImportListByIDGetter interface {
	Get(ctx context.Context, id int) (*importlists.ImportList, error)
}

// ImportListByIDHandler returns GET /api/v3/importlist/{id}.
func ImportListByIDHandler(svc ImportListByIDGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "importlist: invalid id")
			return
		}
		row, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, importlists.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("importlist %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist: get %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(toImportListResource(*row))
		_, _ = w.Write(buf)
	})
}

// ImportListHandler returns the http.Handler for
// GET /api/v3/importlist. Mirrors `ImportListController.GetAll`.
func ImportListHandler(lister ImportListLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlists: %w", err), nil)
			return
		}
		out := make([]ImportListResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toImportListResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// ImportListWriter is the CUD surface for the POST/PUT/DELETE
// handlers. *importlists.Repo satisfies this via the embedded
// repo.Repository[ImportList] (Insert/Update/Delete) + Get + All.
type ImportListWriter interface {
	Get(ctx context.Context, id int) (*importlists.ImportList, error)
	All(ctx context.Context) ([]importlists.ImportList, error)
	Insert(ctx context.Context, n *importlists.ImportList) (*importlists.ImportList, error)
	Update(ctx context.Context, n *importlists.ImportList) error
	Delete(ctx context.Context, id int) error
}

// ImportListEmitter mirrors the subset of
// *signalr.ResourceBroadcaster[ImportListResource] the write
// handlers call. Same shape as IndexerEmitter / NotificationEmitter.
type ImportListEmitter interface {
	Created(ctx context.Context, resource ImportListResource) error
	Updated(ctx context.Context, resource ImportListResource) error
	Deleted(ctx context.Context, resource ImportListResource) error
}

// ImportListCreateHandler returns POST /api/v3/importlist. Mirrors
// `ProviderControllerBase.CreateProvider` at C# :81-93. Returns 201
// + resource + emitter.Created. Validation chain ports the C#
// SharedValidator rules: Name + Implementation + ConfigContract
// non-empty + name unique (case-insensitive). The
// importlists.Registry membership check is DEFERRED to 5.C3b's test
// endpoint — the registry exists but the concrete providers don't
// all register today (only Sonarr + Plex are wired per Phase-4
// subset-4 follow-up (c)).
func ImportListCreateHandler(svc ImportListWriter, emitter ImportListEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeImportListBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist: malformed body: %v", err))
			return
		}
		if failures := validateImportListResource(r.Context(), body, 0, svc); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromImportListResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist: invalid fields: %v", err))
			return
		}
		saved, err := svc.Insert(r.Context(), row)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist: insert: %w", err), nil)
			return
		}
		res := toImportListResource(*saved)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "importlist", "created", func() error {
			return emitter.Created(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// ImportListUpdateHandler returns PUT /api/v3/importlist/{id}.
// Mirrors `ProviderControllerBase.UpdateProvider(id, resource)` at
// C# :98-125. Body-Id fallback preserved.
func ImportListUpdateHandler(svc ImportListWriter, emitter ImportListEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "importlist: invalid id")
			return
		}
		body, err := decodeImportListBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist: malformed body: %v", err))
			return
		}
		existing, err := svc.Get(r.Context(), id)
		if err != nil && !errors.Is(err, importlists.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist: get %d: %w", id, err), nil)
			return
		}
		if errors.Is(err, importlists.ErrNotFound) {
			if body.ID > 0 && body.ID != id {
				if e2, e := svc.Get(r.Context(), body.ID); e == nil {
					existing = e2
					id = body.ID
				}
			}
		}
		if existing == nil {
			httperr.NotFound(w, fmt.Sprintf("importlist %d not found", id))
			return
		}
		if failures := validateImportListResource(r.Context(), body, id, svc); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromImportListResource(body, existing)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist: invalid fields: %v", err))
			return
		}
		row.ID = id
		if err := svc.Update(r.Context(), row); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist: update %d: %w", id, err), nil)
			return
		}
		res := toImportListResource(*row)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "importlist", "updated", func() error {
			return emitter.Updated(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// ImportListDeleteHandler returns DELETE /api/v3/importlist/{id}.
// Mirrors `ProviderControllerBase.DeleteProvider` at C# :181-186.
func ImportListDeleteHandler(svc ImportListWriter, emitter ImportListEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "importlist: invalid id")
			return
		}
		// Idempotent DELETE — C.1.i-follow-4 alignment (J232). C# returns 200 + `{}` on missing id.
		// SignalR Deleted broadcast only fires on actual delete (not on missing-id swallow).
		err = svc.Delete(r.Context(), id)
		if err != nil && !errors.Is(err, importlists.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist: delete %d: %w", id, err), nil)
			return
		}
		if err == nil {
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "importlist", "deleted", func() error {
				return emitter.Deleted(r.Context(), ImportListResource{providerResourceCore: providerResourceCore{ID: id}})
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeImportListBody(r *http.Request) (ImportListResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out ImportListResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return ImportListResource{}, err
	}
	return out, nil
}

// validateImportListResource ports the C# SharedValidator rules
// minus the registry-membership check (deferred to 5.C3b).
func validateImportListResource(ctx context.Context, body ImportListResource, existingID int, svc ImportListWriter) []httperr.Failure {
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

// fromImportListResource is the inverse of toImportListResource. On
// PUT, `existing` is the persisted row — its *Settings is the
// fallback when the request body omits `fields[]`.
func fromImportListResource(res ImportListResource, existing *importlists.ImportList) (*importlists.ImportList, error) {
	settings := "{}"
	if res.Fields != nil {
		s, err := settingsJSONFromFields(res.Fields)
		if err != nil {
			return nil, err
		}
		settings = s
	} else if existing != nil && existing.Settings != nil {
		settings = *existing.Settings
	}
	cfg := res.ConfigContract
	tags := res.Tags
	if tags == nil {
		tags = []int{}
	}
	enableAuto := res.EnableAutomaticAdd
	return &importlists.ImportList{
		ID:                       res.ID,
		Name:                     res.Name,
		Implementation:           res.Implementation,
		Settings:                 &settings,
		ConfigContract:           &cfg,
		EnableAutomaticAdd:       &enableAuto,
		RootFolderPath:           res.RootFolderPath,
		ShouldMonitor:            res.ShouldMonitor,
		QualityProfileID:         res.QualityProfileID,
		Tags:                     tags,
		SeriesType:               res.SeriesType,
		SeasonFolder:             res.SeasonFolder,
		SearchForMissingEpisodes: res.SearchForMissingEpisodes,
		MonitorNewItems:          res.MonitorNewItems,
	}, nil
}

// ImportListTester invokes a per-row provider Test. The cmd-side
// adapter wires this against importlists.Registry.Construct +
// importlists.Provider.Test under the host-supplied
// importlists.Dependencies. Tests can pass a stub.
//
// Unlike 5.C2b (indexers needed a port pre-work because no Registry
// existed), the importlists.Registry already ships from Phase 4
// subset 4 with Sonarr + Plex providers registered. Real Test calls
// will work for those impls today; the other 8 follow-up impls
// (Trakt user/popular/list, Simkl, AniList, MyAnimeList, Custom,
// PlexRss per subset-4 follow-up (d)) still 400 with "Unknown
// implementation" until they port.
type ImportListTester interface {
	Test(ctx context.Context, n *importlists.ImportList) ([]importlists.TestFailure, error)
}

// ImportListTestHandler returns POST /api/v3/importlist/test. Same
// shape as 5.C1b notification + 5.C2b indexer Test endpoints: 200
// `{}` on success, 400 FluentValidation array on failures, 400
// single-Implementation failure on ErrNotRegistered, 500 on
// transport errors.
//
// SharedValidator skipped (matches C# `[SkipValidation(true,false)]`).
func ImportListTestHandler(tester ImportListTester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeImportListBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist: malformed body: %v", err))
			return
		}
		row, err := fromImportListResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist: invalid fields: %v", err))
			return
		}
		failures, err := tester.Test(r.Context(), row)
		if err != nil {
			if errors.Is(err, importlists.ErrNotRegistered) {
				httperr.WriteValidation(w, []httperr.Failure{{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q (configContract %q)", body.Implementation, body.ConfigContract),
					Severity:     "error",
				}})
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist: test: %w", err), nil)
			return
		}
		if len(failures) > 0 {
			httperr.WriteValidation(w, importListTestFailuresToFluentArray(failures))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// ImportListBulkResource mirrors C# `ImportListBulkResource` at
// `src/Sonarr.Api.V3/ImportLists/ImportListBulkResource.cs`. Three
// per-controller optional fields plus the inherited Ids/Tags/ApplyTags.
//
// Note: RootFolderPath is *string (not plain string) so the
// distinction between "not supplied" and "empty string" is wire-
// preservable — matches the C# null-coalesce semantics at the
// mapper at :26 (`existing.RootFolderPath = resource.RootFolderPath
// ?? existing.RootFolderPath`).
//
// applyTagsAction (defined in indexer.go for 5.C2e) is family-
// agnostic and reused here.
type ImportListBulkResource struct {
	IDs                []int           `json:"ids"`
	Tags               []int           `json:"tags"`
	ApplyTags          applyTagsAction `json:"applyTags"`
	EnableAutomaticAdd *bool           `json:"enableAutomaticAdd,omitempty"`
	RootFolderPath     *string         `json:"rootFolderPath,omitempty"`
	QualityProfileID   *int            `json:"qualityProfileId,omitempty"`
}

// ImportListBulkUpdateHandler returns PUT /api/v3/importlist/bulk.
// Mirrors `ProviderControllerBase.UpdateProvider(TBulkProviderResource)`
// at C# :130-166 + `ImportListBulkResourceMapper.UpdateModel` at
// `ImportListBulkResource.cs:17-32`. Importlists support bulk (C#
// ImportListController inherits the base method unchanged).
func ImportListBulkUpdateHandler(svc ImportListWriter, emitter ImportListEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeImportListBulkBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist/bulk: malformed body: %v", err))
			return
		}
		if len(body.IDs) == 0 {
			httperr.BadRequest(w, "ids must be provided")
			return
		}
		updated := make([]ImportListResource, 0, len(body.IDs))
		for _, id := range body.IDs {
			existing, err := svc.Get(r.Context(), id)
			if errors.Is(err, importlists.ErrNotFound) {
				continue
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist/bulk: get %d: %w", id, err), nil)
				return
			}
			applyImportListBulkUpdate(existing, body)
			if err := svc.Update(r.Context(), existing); err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist/bulk: update %d: %w", id, err), nil)
				return
			}
			res := toImportListResource(*existing)
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "importlist", "updated", func() error {
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

// ImportListBulkDeleteHandler returns DELETE /api/v3/importlist/bulk.
// Same shape as IndexerBulkDeleteHandler.
func ImportListBulkDeleteHandler(svc ImportListWriter, emitter ImportListEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeImportListBulkBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist/bulk: malformed body: %v", err))
			return
		}
		if len(body.IDs) == 0 {
			httperr.BadRequest(w, "ids must be provided")
			return
		}
		for _, id := range body.IDs {
			err := svc.Delete(r.Context(), id)
			if errors.Is(err, importlists.ErrNotFound) {
				continue
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist/bulk: delete %d: %w", id, err), nil)
				return
			}
			id := id
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "importlist", "deleted", func() error {
				return emitter.Deleted(r.Context(), ImportListResource{providerResourceCore: providerResourceCore{ID: id}})
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeImportListBulkBody(r *http.Request) (ImportListBulkResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out ImportListBulkResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return ImportListBulkResource{}, err
	}
	return out, nil
}

// applyImportListBulkUpdate mutates `existing` per the
// [ImportListBulkResource] semantics. Mirrors C#
// `ImportListBulkResourceMapper.UpdateModel` at :17-32 for the
// per-field block plus the inherited
// `ProviderControllerBase.UpdateProvider` Tags-apply switch at
// :148-159 for the tag-mutation block.
func applyImportListBulkUpdate(existing *importlists.ImportList, body ImportListBulkResource) {
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
	if body.EnableAutomaticAdd != nil {
		v := *body.EnableAutomaticAdd
		existing.EnableAutomaticAdd = &v
	}
	if body.RootFolderPath != nil {
		existing.RootFolderPath = *body.RootFolderPath
	}
	if body.QualityProfileID != nil {
		existing.QualityProfileID = *body.QualityProfileID
	}
}

// ImportListActioner invokes a per-row provider RequestAction. The
// cmd-side adapter wires this against importlists.Registry.Construct
// + importlists.Provider.RequestAction under host-supplied
// importlists.Dependencies.
//
// Common import-list actions: Plex OAuth handshake
// (startOAuth/continueOAuth/getOAuthToken from the Plex import-list
// provider RequestAction map) and Sonarr provider helpers
// (getQualityProfiles / getRootFolders / getTags /
// getLanguageProfiles) for the React UI's Sonarr-import setup
// wizard's ID-to-name resolution.
type ImportListActioner interface {
	RequestAction(ctx context.Context, n *importlists.ImportList, action string, query map[string]string) (any, error)
}

// ImportListRequestActionHandler returns POST /api/v3/importlist/action/{name}.
// Same shape as 5.C1d/5.C2d.
func ImportListRequestActionHandler(actioner ImportListActioner) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if strings.TrimSpace(name) == "" {
			httperr.BadRequest(w, "importlist/action: empty name")
			return
		}
		body, err := decodeImportListBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist: malformed body: %v", err))
			return
		}
		row, err := fromImportListResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("importlist: invalid fields: %v", err))
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
			if errors.Is(err, importlists.ErrNotRegistered) {
				httperr.WriteValidation(w, []httperr.Failure{{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q (configContract %q)", body.Implementation, body.ConfigContract),
					Severity:     "error",
				}})
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist: action %q: %w", name, err), nil)
			return
		}
		buf, err := json.Marshal(data)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist: action %q: marshal: %w", name, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	})
}

// ImportListTestAllHandler returns POST /api/v3/importlist/testall.
// Mirrors C# ProviderControllerBase.TestAll at :233-257 and the
// 5.C1c / 5.C2c twins. Iterates every persisted row, runs per-row
// Provider.Test sequentially, aggregates into the family-agnostic
// [ProviderTestAllResult] array.
//
// Returns 400 when any row failed, 200 when all passed. Empty list
// returns []. C# `Settings.Validate().IsValid && c.Enable` filter
// dropped — same rationale as the notification + indexer twins.
func ImportListTestAllHandler(lister ImportListLister, tester ImportListTester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("importlist/testall: list: %w", err), nil)
			return
		}
		results := make([]ProviderTestAllResult, 0, len(rows))
		anyInvalid := false
		for _, row := range rows {
			row := row
			failures, err := tester.Test(r.Context(), &row)
			projected := make([]httperr.Failure, 0)
			switch {
			case errors.Is(err, importlists.ErrNotRegistered):
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
				projected = append(projected, importListTestFailuresToFluentArray(failures)...)
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

// importListTestFailuresToFluentArray ports the projection for the
// [importlists.TestFailure] shape. Same as the notification /
// indexer twins.
func importListTestFailuresToFluentArray(failures []importlists.TestFailure) []httperr.Failure {
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

func toImportListResource(i importlists.ImportList) ImportListResource {
	tags := i.Tags
	if tags == nil {
		tags = []int{}
	}
	cfg := ""
	if i.ConfigContract != nil {
		cfg = *i.ConfigContract
	}
	enableAuto := false
	if i.EnableAutomaticAdd != nil {
		enableAuto = *i.EnableAutomaticAdd
	}
	return ImportListResource{
		providerResourceCore: providerResourceCore{
			ID:                 i.ID,
			Name:               i.Name,
			Implementation:     i.Implementation,
			ImplementationName: implementationNameOr(importListImplNames, i.Implementation),
			ConfigContract:     cfg,
			Tags:               tags,
		},
		Fields:                   fieldsFromSettingsJSON(derefSettings(i.Settings)), // G4 (J425)
		EnableAutomaticAdd:       enableAuto,
		SearchForMissingEpisodes: i.SearchForMissingEpisodes,
		ShouldMonitor:            i.ShouldMonitor,
		MonitorNewItems:          i.MonitorNewItems,
		RootFolderPath:           i.RootFolderPath,
		QualityProfileID:         i.QualityProfileID,
		SeriesType:               i.SeriesType,
		SeasonFolder:             i.SeasonFolder,
	}
}
