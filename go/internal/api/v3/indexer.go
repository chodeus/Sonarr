package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/indexers"
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
	EnableRss                           bool   `json:"enableRss"`
	EnableAutomaticSearch               bool   `json:"enableAutomaticSearch"`
	EnableInteractiveSearch             bool   `json:"enableInteractiveSearch"`
	SupportsRss                         bool   `json:"supportsRss"`
	SupportsSearch                      bool   `json:"supportsSearch"`
	Protocol                            string `json:"protocol"`
	Priority                            int    `json:"priority"`
	DownloadClientID                    int    `json:"downloadClientId"`
	SeasonSearchMaximumSingleEpisodeAge int    `json:"seasonSearchMaximumSingleEpisodeAge"`
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
