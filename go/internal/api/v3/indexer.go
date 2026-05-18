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
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/signalr"
)

// IndexerResource mirrors
// `src/Sonarr.Api.V3/Indexers/IndexerResource.cs`. Inherits
// providerResourceCore + the per-indexer enable flags / priority /
// per-protocol settings.
//
// Populated-row mapping defers Fields (schema-builder) and Protocol
// (DownloadProtocol enum-as-string). Empty-DB recording has zero
// rows so the gaps are unobservable today.
type IndexerResource struct {
	providerResourceCore
	EnableRss                           bool            `json:"enableRss"`
	EnableAutomaticSearch               bool            `json:"enableAutomaticSearch"`
	EnableInteractiveSearch             bool            `json:"enableInteractiveSearch"`
	SupportsRss                         bool            `json:"supportsRss"`
	SupportsSearch                      bool            `json:"supportsSearch"`
	Protocol                            string          `json:"protocol"`
	Priority                            int             `json:"priority"`
	DownloadClientID                    int             `json:"downloadClientId"`
	SeasonSearchMaximumSingleEpisodeAge int             `json:"seasonSearchMaximumSingleEpisodeAge"`
	Fields                              []ProviderField `json:"fields,omitempty"`
}

// IndexerLister is the read side the handler depends on.
// *indexers.Repo satisfies it.
type IndexerLister interface {
	All(ctx context.Context) ([]indexers.Indexer, error)
}

// IndexerByIDGetter narrows the read surface for GET /api/v3/indexer/{id}.
// Satisfied by *indexers.Repo via the embedded *repo.Repository.
type IndexerByIDGetter interface {
	Get(ctx context.Context, id int) (*indexers.Indexer, error)
}

// IndexerByIDHandler returns GET /api/v3/indexer/{id}. Mirrors the
// C# RestController base GetResourceById behaviour - 404 on miss,
// 200 + IndexerResource on success.
func IndexerByIDHandler(svc IndexerByIDGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "indexer: invalid id")
			return
		}
		row, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, indexers.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("indexer %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("indexer: get %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(toIndexerResource(*row))
		_, _ = w.Write(buf)
	})
}

// IndexerHandler returns the http.Handler for GET /api/v3/indexer.
// Mirrors `IndexerController.GetAll`.
func IndexerHandler(lister IndexerLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("indexers: %w", err), nil)
			return
		}
		out := make([]IndexerResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toIndexerResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// IndexerWriter is the CUD surface for the POST/PUT/DELETE handlers.
// *indexers.Repo satisfies this via the embedded
// repo.Repository[Indexer] (Insert/Update/Delete) + the inherited
// Get + All from the same generic.
type IndexerWriter interface {
	Get(ctx context.Context, id int) (*indexers.Indexer, error)
	All(ctx context.Context) ([]indexers.Indexer, error)
	Insert(ctx context.Context, n *indexers.Indexer) (*indexers.Indexer, error)
	Update(ctx context.Context, n *indexers.Indexer) error
	Delete(ctx context.Context, id int) error
}

// IndexerEmitter mirrors the subset of
// *signalr.ResourceBroadcaster[IndexerResource] the write handlers
// call. Same shape as NotificationEmitter — collapses the C#
// `ProviderControllerBase.Handle(ProviderXxxEvent)` shims at
// `:276-291` into a direct emitter call.
type IndexerEmitter interface {
	Created(ctx context.Context, resource IndexerResource) error
	Updated(ctx context.Context, resource IndexerResource) error
	Deleted(ctx context.Context, resource IndexerResource) error
}

// IndexerCreateHandler returns POST /api/v3/indexer.
// Mirrors `ProviderControllerBase.CreateProvider` at C# :81-93.
// Returns 201 + the persisted resource. SignalR Created fires after
// Insert succeeds.
//
// Validation chain ports the C# SharedValidator rules at :44-49
// minus the registry-membership check (deferred to 5.C2b once an
// indexers.Registry package is ported — notifications.Registry has
// no counterpart in internal/indexers/ yet):
//
//   - Name non-empty + unique (case-insensitive) across existing rows.
//   - Implementation non-empty.
//   - ConfigContract non-empty.
//
// The C# `Test()` call before Insert (gated by `Enable` + `!forceSave`)
// is DEFERRED to slice 5.C2b — the Test endpoint hasn't ported yet.
// 5.C2a accepts all valid rows; `?forceSave=true` is parsed and
// ignored for forward-compat.
func IndexerCreateHandler(svc IndexerWriter, emitter IndexerEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeIndexerBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("indexer: malformed body: %v", err))
			return
		}
		if failures := validateIndexerResource(r.Context(), body, 0, svc); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromIndexerResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("indexer: invalid fields: %v", err))
			return
		}
		saved, err := svc.Insert(r.Context(), row)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("indexer: insert: %w", err), nil)
			return
		}
		res := toIndexerResource(*saved)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "indexer", "created", func() error {
			return emitter.Created(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// IndexerUpdateHandler returns PUT /api/v3/indexer/{id}.
// Mirrors `ProviderControllerBase.UpdateProvider(id, resource)` at C#
// :98-125. Same C# body-Id fallback ("// TODO: Remove fallback in
// next API version bump") as the notification PUT.
func IndexerUpdateHandler(svc IndexerWriter, emitter IndexerEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "indexer: invalid id")
			return
		}
		body, err := decodeIndexerBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("indexer: malformed body: %v", err))
			return
		}
		existing, err := svc.Get(r.Context(), id)
		if err != nil && !errors.Is(err, indexers.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("indexer: get %d: %w", id, err), nil)
			return
		}
		if errors.Is(err, indexers.ErrNotFound) {
			if body.ID > 0 && body.ID != id {
				if e2, e := svc.Get(r.Context(), body.ID); e == nil {
					existing = e2
					id = body.ID
				}
			}
		}
		if existing == nil {
			httperr.NotFound(w, fmt.Sprintf("indexer %d not found", id))
			return
		}
		if failures := validateIndexerResource(r.Context(), body, id, svc); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromIndexerResource(body, existing)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("indexer: invalid fields: %v", err))
			return
		}
		row.ID = id
		if err := svc.Update(r.Context(), row); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("indexer: update %d: %w", id, err), nil)
			return
		}
		res := toIndexerResource(*row)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "indexer", "updated", func() error {
			return emitter.Updated(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// IndexerDeleteHandler returns DELETE /api/v3/indexer/{id}.
// Mirrors `ProviderControllerBase.DeleteProvider` at C# :181-186.
func IndexerDeleteHandler(svc IndexerWriter, emitter IndexerEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "indexer: invalid id")
			return
		}
		err = svc.Delete(r.Context(), id)
		if errors.Is(err, indexers.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("indexer %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("indexer: delete %d: %w", id, err), nil)
			return
		}
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "indexer", "deleted", func() error {
			return emitter.Deleted(r.Context(), IndexerResource{providerResourceCore: providerResourceCore{ID: id}})
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeIndexerBody(r *http.Request) (IndexerResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out IndexerResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return IndexerResource{}, err
	}
	return out, nil
}

// validateIndexerResource ports the SharedValidator rules from
// `ProviderControllerBase.cs:44-49` minus the registry-membership
// check. `existingID` is 0 for POST and the row id for PUT — used to
// skip the self-row in the name-unique check.
func validateIndexerResource(ctx context.Context, body IndexerResource, existingID int, svc IndexerWriter) []httperr.Failure {
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

// fromIndexerResource is the inverse of toIndexerResource. On PUT,
// `existing` is the persisted row — its Settings JSON is the
// fallback when the request body omits `fields[]`.
func fromIndexerResource(res IndexerResource, existing *indexers.Indexer) (*indexers.Indexer, error) {
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
	enableRss := res.EnableRss
	enableAuto := res.EnableAutomaticSearch
	return &indexers.Indexer{
		ID:                                  res.ID,
		Name:                                res.Name,
		Implementation:                      res.Implementation,
		Settings:                            &settings,
		ConfigContract:                      &cfg,
		EnableRss:                           &enableRss,
		EnableAutomaticSearch:               &enableAuto,
		EnableInteractiveSearch:             res.EnableInteractiveSearch,
		Priority:                            res.Priority,
		Tags:                                tags,
		DownloadClientID:                    res.DownloadClientID,
		SeasonSearchMaximumSingleEpisodeAge: res.SeasonSearchMaximumSingleEpisodeAge,
	}, nil
}

// IndexerTester invokes a per-row provider Test. The cmd-side
// adapter wires this against indexers.Registry.Construct +
// indexers.Provider.Test under the host-supplied
// indexers.Dependencies. Tests can pass a stub.
//
// An error return is reserved for "provider couldn't be
// constructed" (unknown impl, malformed settings); these are
// projected to 400 with the C# unknown-implementation envelope.
type IndexerTester interface {
	Test(ctx context.Context, n *indexers.Indexer) ([]indexers.TestFailure, error)
}

// IndexerTestHandler returns POST /api/v3/indexer/test. Mirrors the
// notification Test endpoint (5.C1b) shape: 200 `{}` on success,
// 400 FluentValidation array on failures, 400 single-Implementation
// failure on ErrNotRegistered, 500 on transport errors.
//
// SharedValidator is skipped (matches C# `[SkipValidation(true,false)]`)
// — Settings.Validate runs inside Provider.Test under the hood.
func IndexerTestHandler(tester IndexerTester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeIndexerBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("indexer: malformed body: %v", err))
			return
		}
		row, err := fromIndexerResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("indexer: invalid fields: %v", err))
			return
		}
		failures, err := tester.Test(r.Context(), row)
		if err != nil {
			if errors.Is(err, indexers.ErrNotRegistered) {
				httperr.WriteValidation(w, []httperr.Failure{{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q (configContract %q)", body.Implementation, body.ConfigContract),
					Severity:     "error",
				}})
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("indexer: test: %w", err), nil)
			return
		}
		if len(failures) > 0 {
			httperr.WriteValidation(w, indexerTestFailuresToFluentArray(failures))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// indexerTestFailuresToFluentArray ports the projection
// `STJson.ToJson(validationException.Errors)` for the
// [indexers.TestFailure] shape. DetailedDescription has no
// FluentValidation counterpart in httperr.Failure — dropped
// pending the shared cross-family type.
func indexerTestFailuresToFluentArray(failures []indexers.TestFailure) []httperr.Failure {
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

func toIndexerResource(i indexers.Indexer) IndexerResource {
	tags := i.Tags
	if tags == nil {
		tags = []int{}
	}
	cfg := ""
	if i.ConfigContract != nil {
		cfg = *i.ConfigContract
	}
	enableRss := false
	if i.EnableRss != nil {
		enableRss = *i.EnableRss
	}
	enableAuto := false
	if i.EnableAutomaticSearch != nil {
		enableAuto = *i.EnableAutomaticSearch
	}
	return IndexerResource{
		providerResourceCore: providerResourceCore{
			ID:                 i.ID,
			Name:               i.Name,
			Implementation:     i.Implementation,
			ImplementationName: i.Implementation,
			ConfigContract:     cfg,
			Tags:               tags,
		},
		EnableRss:               enableRss,
		EnableAutomaticSearch:   enableAuto,
		EnableInteractiveSearch: i.EnableInteractiveSearch,
		// SupportsRss / SupportsSearch / Protocol come from the
		// indexer-factory introspection (DEFERRED). Stubbed false /
		// "unknown" until the factory wiring lands.
		SupportsRss:                         false,
		SupportsSearch:                      false,
		Protocol:                            "unknown",
		Priority:                            i.Priority,
		DownloadClientID:                    i.DownloadClientID,
		SeasonSearchMaximumSingleEpisodeAge: i.SeasonSearchMaximumSingleEpisodeAge,
	}
}
