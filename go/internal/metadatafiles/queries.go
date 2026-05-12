package metadatafiles

import (
	"context"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
)

// AllBySeriesID returns every MetadataFile row for the given series.
// Mirrors C# `IMetadataFileService.GetFilesBySeries(seriesId)` at
// `MetadataFileService.cs` (the per-series fetch every MetadataService
// event handler calls before fanning out across writers).
//
// Empty result on no matches. Errors propagate.
func (r *Repo) AllBySeriesID(ctx context.Context, seriesID int) ([]MetadataFile, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","SeriesId","Consumer","Type","RelativePath","LastUpdated",`+
			`"SeasonNumber","EpisodeFileId","Hash","Added","Extension" `+
			`FROM "MetadataFiles" WHERE "SeriesId" = %s ORDER BY "Id"`,
		datastore.Placeholder(db.Engine, 1),
	)
	rows, err := db.SQL.QueryContext(ctx, q, seriesID)
	if err != nil {
		return nil, fmt.Errorf("metadatafiles: AllBySeriesID(%d): %w", seriesID, err)
	}
	defer rows.Close() //nolint:errcheck // sql.Rows.Close is idempotent and the deferred error has no useful surface here.

	var out []MetadataFile
	for rows.Next() {
		var m MetadataFile
		if scanErr := mapper.Scan(&m, rows); scanErr != nil {
			return nil, fmt.Errorf("metadatafiles: AllBySeriesID(%d) scan: %w", seriesID, scanErr)
		}
		out = append(out, m)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("metadatafiles: AllBySeriesID(%d) rows: %w", seriesID, rowsErr)
	}
	if out == nil {
		out = []MetadataFile{}
	}
	return out, nil
}
