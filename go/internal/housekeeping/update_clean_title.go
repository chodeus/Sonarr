// UpdateCleanTitleForSeries. Ports
// `src/NzbDrone.Core/Housekeeping/Housekeepers/UpdateCleanTitleForSeries.cs:1-31`.
//
// Walks every Series row, recomputes CleanTitle via
// parser.CleanSeriesTitle, and persists when the value drifted from
// what's stored. Why it exists: the CleanTitle algorithm has been
// tweaked across releases (commonword stripping rules, diacritic
// table additions, etc.); this task lazily reconciles old rows after
// an upgrade so search lookups don't miss-match against stale clean
// titles.

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// updateCleanTitleForSeries is the Task. Owns the series.Repo
// reference because the housekeeper needs both All() and Update().
type updateCleanTitleForSeries struct {
	repo *series.Repo
	log  *slog.Logger
}

// Name implements Task.
func (t *updateCleanTitleForSeries) Name() string { return "UpdateCleanTitleForSeries" }

// Clean implements Task. Iterates every Series row; if CleanTitle has
// drifted from `parser.CleanSeriesTitle(Title)`, persist the
// corrected value.
func (t *updateCleanTitleForSeries) Clean(ctx context.Context) error {
	rows, err := t.repo.All(ctx)
	if err != nil {
		return fmt.Errorf("housekeeping: UpdateCleanTitleForSeries: All: %w", err)
	}
	var updated int
	for i := range rows {
		row := rows[i]
		want := parser.CleanSeriesTitle(row.Title)
		if row.CleanTitle == want {
			continue
		}
		row.CleanTitle = want
		if err := t.repo.Update(ctx, &row); err != nil {
			return fmt.Errorf("housekeeping: UpdateCleanTitleForSeries: Update(%d): %w", row.ID, err)
		}
		updated++
	}
	if updated > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: refreshed Series.CleanTitle",
			slog.Int("rows", updated))
	}
	return nil
}

// NewUpdateCleanTitleForSeries builds the housekeeper. seriesRepo
// must be non-nil.
func NewUpdateCleanTitleForSeries(seriesRepo *series.Repo, log *slog.Logger) (Task, error) {
	if seriesRepo == nil {
		return nil, errors.New("housekeeping: NewUpdateCleanTitleForSeries: nil series repo")
	}
	if log == nil {
		log = slog.Default()
	}
	return &updateCleanTitleForSeries{repo: seriesRepo, log: log}, nil
}
