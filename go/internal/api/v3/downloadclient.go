package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/downloadclients"
)

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
	Enable                   bool   `json:"enable"`
	Protocol                 string `json:"protocol"`
	Priority                 int    `json:"priority"`
	RemoveCompletedDownloads bool   `json:"removeCompletedDownloads"`
	RemoveFailedDownloads    bool   `json:"removeFailedDownloads"`
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
			http.Error(w, fmt.Sprintf("downloadclients: %v", err), http.StatusInternalServerError)
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
			ImplementationName: d.Implementation,
			ConfigContract:     d.ConfigContract,
			Tags:               tags,
		},
		Enable:                   d.Enable,
		Protocol:                 "unknown", // DEFERRED: factory introspection
		Priority:                 d.Priority,
		RemoveCompletedDownloads: d.RemoveCompletedDownloads,
		RemoveFailedDownloads:    d.RemoveFailedDownloads,
	}
}
