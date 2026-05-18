package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesFolderGetter narrows *series.Service to the single GetSeries
// call this handler needs.
type SeriesFolderGetter interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// NamingConfigReader narrows *namingconfig.Service to GetConfig. Both
// service interfaces are kept narrow so test stubs can stay tiny.
type NamingConfigReader interface {
	GetConfig(ctx context.Context) (*namingconfig.Config, error)
}

// SeriesFolderHandler returns GET /api/v3/series/{id}/folder. Mirrors
// `SeriesFolderController.GetFolder` at `:20-30`:
//
//	var series = _seriesService.GetSeries(id);
//	var folder = _fileNameBuilder.GetSeriesFolder(series);
//	return new { folder };
//
// 404 when the series id doesn't match a row. 400 when id is
// non-positive. 500 on naming-config read failure.
func SeriesFolderHandler(seriesSvc SeriesFolderGetter, naming NamingConfigReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "series: invalid id")
			return
		}
		s, err := seriesSvc.GetSeries(r.Context(), id)
		if err != nil {
			if errors.Is(err, series.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("series %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/folder: get %d: %w", id, err), nil)
			return
		}
		cfg, err := naming.GetConfig(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("series/folder: naming config: %w", err), nil)
			return
		}
		folder := organizer.GetSeriesFolder(s, organizer.FolderOptions{
			SeriesFolderFormat:   cfg.SeriesFolderFormat,
			SeasonFolderFormat:   cfg.SeasonFolderFormat,
			SpecialsFolderFormat: cfg.SpecialsFolderFormat,
			Clean: organizer.CleanNameOptions{
				ReplaceIllegalCharacters:     cfg.ReplaceIllegalCharacters,
				ColonReplacementFormat:       organizer.ColonReplacementFormat(cfg.ColonReplacementFormat),
				CustomColonReplacementFormat: cfg.CustomColonReplacementFormat,
			},
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(struct {
			Folder string `json:"folder"`
		}{Folder: folder})
		_, _ = w.Write(buf)
	})
}
