package extrafiles

import (
	"context"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/datastore"
)

// AllBySeriesID returns every ExtraFile row for the given series.
// Mirrors C# `IExtraFileRepository<T>.GetFilesBySeries(seriesId)` at
// `src/NzbDrone.Core/Extras/Files/ExtraFileRepository.cs` (the
// generic per-series fetch every extras consumer calls before fanning
// out per-file).
//
// Empty result on no matches. Errors propagate wrapped.
func (r *Repo) AllBySeriesID(ctx context.Context, seriesID int) ([]ExtraFile, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","SeriesId","SeasonNumber","EpisodeFileId","RelativePath","Extension","Added","LastUpdated" `+
			`FROM "ExtraFiles" WHERE "SeriesId" = %s ORDER BY "Id"`,
		datastore.Placeholder(db.Engine, 1),
	)
	rows, err := db.SQL.QueryContext(ctx, q, seriesID)
	if err != nil {
		return nil, fmt.Errorf("extrafiles: AllBySeriesID(%d): %w", seriesID, err)
	}
	defer rows.Close() //nolint:errcheck // sql.Rows.Close is idempotent.

	var out []ExtraFile
	for rows.Next() {
		var f ExtraFile
		if scanErr := mapper.Scan(&f, rows); scanErr != nil {
			return nil, fmt.Errorf("extrafiles: AllBySeriesID(%d) scan: %w", seriesID, scanErr)
		}
		out = append(out, f)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("extrafiles: AllBySeriesID(%d) rows: %w", seriesID, rowsErr)
	}
	if out == nil {
		out = []ExtraFile{}
	}
	return out, nil
}

// DeleteByIDs removes every ExtraFile row whose Id is in the supplied
// list. Mirrors C# `IExtraFileRepository<T>.DeleteMany(IEnumerable<int>)`
// at `ExtraFileRepository.cs`. Empty input is a no-op with nil error.
// Returns rows-affected.
func (r *Repo) DeleteByIDs(ctx context.Context, ids []int) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db := r.DB()
	holders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		holders[i] = datastore.Placeholder(db.Engine, i+1)
		args[i] = id
	}
	q := fmt.Sprintf(`DELETE FROM "ExtraFiles" WHERE "Id" IN (%s)`, strings.Join(holders, ","))
	res, err := db.SQL.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("extrafiles: DeleteByIDs(%d ids): %w", len(ids), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("extrafiles: DeleteByIDs rowsAffected: %w", err)
	}
	return n, nil
}
