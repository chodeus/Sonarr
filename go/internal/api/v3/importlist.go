package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/importlists"
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
	EnableAutomaticAdd       bool   `json:"enableAutomaticAdd"`
	SearchForMissingEpisodes bool   `json:"searchForMissingEpisodes"`
	ShouldMonitor            int    `json:"shouldMonitor"`
	MonitorNewItems          int    `json:"monitorNewItems"`
	RootFolderPath           string `json:"rootFolderPath"`
	QualityProfileID         int    `json:"qualityProfileId"`
	SeriesType               int    `json:"seriesType"`
	SeasonFolder             bool   `json:"seasonFolder"`
}

// ImportListLister is the read side the handler depends on.
type ImportListLister interface {
	All(ctx context.Context) ([]importlists.ImportList, error)
}

// ImportListHandler returns the http.Handler for
// GET /api/v3/importlist. Mirrors `ImportListController.GetAll`.
func ImportListHandler(lister ImportListLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("importlists: %v", err), http.StatusInternalServerError)
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
			ImplementationName: i.Implementation,
			ConfigContract:     cfg,
			Tags:               tags,
		},
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
