// CleanupDuplicateMetadataFiles housekeeper. Ports the C# task at
// `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupDuplicateMetadataFiles.cs`.
//
// MetadataFiles rows are inserted by per-consumer (Kodi, Roksbox,
// WDTV, ...) metadata writers; a bug in any writer that re-emits a
// sidecar without dropping the existing row produces a duplicate.
// This housekeeper sweeps duplicates in three passes, one per
// metadata Type that the C# tree dedups:
//
//   - Type=1 (SeriesMetadata) deduped on (SeriesId, Consumer).
//   - Type=2 (EpisodeMetadata) deduped on (EpisodeFileId, Consumer).
//   - Type=5 (EpisodeImage) deduped on (EpisodeFileId, Consumer).
//
// SeriesImage (Type=3) and SeasonImage (Type=4) are NOT deduped by
// the C# tree — the metadata-writers for those Types own the
// uniqueness invariant elsewhere and the housekeeper has never
// touched them.
//
// Per-pass shape: `DELETE FROM MetadataFiles WHERE Id IN (SELECT
// MIN(Id) FROM MetadataFiles WHERE Type = ? GROUP BY <key>, Consumer
// HAVING COUNT(<key>) > 1)`. This DELETEs ONE row per duplicate
// group per pass — for a group of three duplicates the first pass
// removes one (the MIN-Id row), leaving two; the next pass removes
// another, leaving one. Convergence is O(max-group-size) passes,
// which is fine for a housekeeping task that runs daily.

package housekeeping

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/datastore"
)

// NewCleanupDuplicateMetadataFiles ports
// `Housekeepers/CleanupDuplicateMetadataFiles.cs:1-58`. Three DELETE
// passes, one per Type the C# tree dedups.
func NewCleanupDuplicateMetadataFiles(db *datastore.DB, log *slog.Logger) (Task, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &cleanupDuplicateMetadata{db: db, log: log}, nil
}

type cleanupDuplicateMetadata struct {
	db  *datastore.DB
	log *slog.Logger
}

// Name implements Task.
func (t *cleanupDuplicateMetadata) Name() string {
	return "CleanupDuplicateMetadataFiles"
}

// Clean implements Task. Three DELETE passes; per-pass failures
// abort the loop and propagate.
func (t *cleanupDuplicateMetadata) Clean(ctx context.Context) error {
	passes := []struct {
		label string
		mtype int
		key   string
	}{
		{label: "series-metadata", mtype: 1, key: "SeriesId"},
		{label: "episode-metadata", mtype: 2, key: "EpisodeFileId"},
		{label: "episode-image", mtype: 5, key: "EpisodeFileId"},
	}
	for _, p := range passes {
		q := fmt.Sprintf(
			`DELETE FROM "MetadataFiles" WHERE "Id" IN (
				SELECT MIN("Id") FROM "MetadataFiles"
				WHERE "Type" = %s
				GROUP BY %q, "Consumer"
				HAVING COUNT(%q) > 1)`,
			datastore.Placeholder(t.db.Engine, 1),
			p.key, p.key,
		)
		res, err := t.db.SQL.ExecContext(ctx, q, p.mtype)
		if err != nil {
			return fmt.Errorf("housekeeping: CleanupDuplicateMetadataFiles (%s): %w", p.label, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("housekeeping: CleanupDuplicateMetadataFiles (%s): rowsAffected: %w", p.label, err)
		}
		if n > 0 {
			t.log.LogAttrs(ctx, slog.LevelInfo,
				"housekeeping: deduped MetadataFiles rows",
				slog.String("pass", p.label),
				slog.Int64("rows", n))
		}
	}
	return nil
}
