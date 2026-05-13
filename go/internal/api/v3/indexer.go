package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

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

// IndexerHandler returns the http.Handler for GET /api/v3/indexer.
// Mirrors `IndexerController.GetAll`.
func IndexerHandler(lister IndexerLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("indexers: %v", err), http.StatusInternalServerError)
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
