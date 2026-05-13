package hostbootstrap

import (
	"context"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/series"
)

// TestV3SeriesContract pins /api/v3/series to the C#-recorded
// golden — an empty list on a fresh DB. Populated-row projection
// (alternateTitles, statistics, seasons, images, original-language
// full DTO) is DEFERRED until those joined ports converge; the
// empty-DB recording carries zero series so the gaps are
// unobservable today.
func TestV3SeriesContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.series.list"
	checkEmptyJSONContract(t, entryID, apiv3.SeriesListHandler(emptySeriesLister{}))
}

type emptySeriesLister struct{}

func (emptySeriesLister) GetAllSeries(context.Context) ([]series.Series, error) {
	return nil, nil
}
