// Orphan cleanup housekeepers. Port the C# `CleanupOrphaned*` family
// at `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupOrphaned*.cs`.
//
// Every housekeeper in this family runs a single
// `DELETE FROM <child> WHERE Id IN (
//   SELECT child.Id FROM child LEFT OUTER JOIN parent
//   ON child.<fk> = parent.Id WHERE parent.Id IS NULL)`
// statement to remove rows whose foreign-key target no longer exists.
// The Go port factors the query template into `deleteOrphansLeftJoin`
// so per-housekeeper code stays tiny and the shape is uniform.
//
// Phase 3.17 ships:
//
//   - CleanupOrphanedEpisodes — Episodes whose Series row was deleted.
//   - CleanupOrphanedEpisodeFiles — EpisodeFiles whose every linked
//     Episode row was deleted (LEFT JOIN through Episode.EpisodeFileId).
//   - CleanupOrphanedHistoryItems — History rows whose Series OR
//     Episode row was deleted (two passes).
//   - CleanupOrphanedBlocklist — Blocklist rows whose Series row was
//     deleted.
//
// All four ports preserve the C# query shape verbatim — same join,
// same WHERE clause, same NULL semantics. PostgreSQL and SQLite both
// accept the `LEFT OUTER JOIN ... WHERE parent.Id IS NULL` idiom
// without modification.

package housekeeping

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/datastore"
)

// deleteOrphansLeftJoin issues the standard "delete child rows whose
// parent FK target no longer exists" statement. The query is written
// against the `Id` PK on both sides; if a future schema introduces a
// non-Id PK, drop a per-Task handler instead of widening this helper.
//
// Returns rows-affected so callers can log or assert.
func deleteOrphansLeftJoin(ctx context.Context, db *datastore.DB, child, fk, parent string) (int64, error) {
	q := fmt.Sprintf(
		`DELETE FROM %q WHERE "Id" IN (
			SELECT %q."Id" FROM %q
			LEFT OUTER JOIN %q ON %q.%q = %q."Id"
			WHERE %q."Id" IS NULL)`,
		child,
		child, child,
		parent,
		child, fk,
		parent,
		parent,
	)
	res, err := db.SQL.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("housekeeping: delete orphans %s.%s → %s: %w", child, fk, parent, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("housekeeping: delete orphans rowsAffected: %w", err)
	}
	return n, nil
}

// orphanCleanupTask is a generic Task scaffold for the
// "single LEFT JOIN + DELETE" shape the four ports share.
type orphanCleanupTask struct {
	name  string
	db    *datastore.DB
	log   *slog.Logger
	pairs []orphanPair
}

type orphanPair struct {
	child, fk, parent string
}

func newOrphanCleanupTask(name string, db *datastore.DB, log *slog.Logger, pairs []orphanPair) (*orphanCleanupTask, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &orphanCleanupTask{name: name, db: db, log: log, pairs: pairs}, nil
}

// Name implements Task.
func (t *orphanCleanupTask) Name() string { return t.name }

// Clean implements Task. Iterates each configured pair and issues
// the standard left-join-orphan delete.
func (t *orphanCleanupTask) Clean(ctx context.Context) error {
	for _, p := range t.pairs {
		n, err := deleteOrphansLeftJoin(ctx, t.db, p.child, p.fk, p.parent)
		if err != nil {
			return err
		}
		if n > 0 {
			t.log.LogAttrs(ctx, slog.LevelInfo,
				"housekeeping: deleted orphan rows",
				slog.String("task", t.name),
				slog.String("child", p.child),
				slog.String("parent", p.parent),
				slog.Int64("rows", n))
		}
	}
	return nil
}

// NewCleanupOrphanedEpisodes ports
// `Housekeepers/CleanupOrphanedEpisodes.cs:1-26`. Drops Episodes rows
// whose SeriesId no longer points at a Series.
func NewCleanupOrphanedEpisodes(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newOrphanCleanupTask("CleanupOrphanedEpisodes", db, log, []orphanPair{
		{child: "Episodes", fk: "SeriesId", parent: "Series"},
	})
}

// NewCleanupOrphanedEpisodeFiles ports
// `Housekeepers/CleanupOrphanedEpisodeFiles.cs:1-26`. Drops
// EpisodeFiles whose every linked Episode row was deleted (no row in
// Episodes points at the file).
//
// The C# query joins `EpisodeFiles.Id = Episodes.EpisodeFileId`. The
// generic helper takes (child=EpisodeFiles, fk=Id, parent=Episodes,
// parentFK=EpisodeFileId) — which doesn't fit the helper's
// `child.fk = parent.Id` shape. Fall back to a hand-rolled query.
func NewCleanupOrphanedEpisodeFiles(db *datastore.DB, log *slog.Logger) (Task, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &cleanupOrphanedEpisodeFiles{db: db, log: log}, nil
}

type cleanupOrphanedEpisodeFiles struct {
	db  *datastore.DB
	log *slog.Logger
}

// Name implements Task.
func (t *cleanupOrphanedEpisodeFiles) Name() string { return "CleanupOrphanedEpisodeFiles" }

// Clean implements Task.
func (t *cleanupOrphanedEpisodeFiles) Clean(ctx context.Context) error {
	q := `DELETE FROM "EpisodeFiles" WHERE "Id" IN (
		SELECT "EpisodeFiles"."Id" FROM "EpisodeFiles"
		LEFT OUTER JOIN "Episodes"
		ON "EpisodeFiles"."Id" = "Episodes"."EpisodeFileId"
		WHERE "Episodes"."Id" IS NULL)`
	res, err := t.db.SQL.ExecContext(ctx, q)
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupOrphanedEpisodeFiles: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupOrphanedEpisodeFiles: rowsAffected: %w", err)
	}
	if n > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: deleted orphan EpisodeFiles",
			slog.Int64("rows", n))
	}
	return nil
}

// NewCleanupOrphanedHistoryItems ports
// `Housekeepers/CleanupOrphanedHistoryItems.cs:1-43`. Two-phase delete:
// History rows whose SeriesId is dangling, then History rows whose
// EpisodeId is dangling. Order doesn't matter — both passes are
// commutative.
func NewCleanupOrphanedHistoryItems(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newOrphanCleanupTask("CleanupOrphanedHistoryItems", db, log, []orphanPair{
		{child: "History", fk: "SeriesId", parent: "Series"},
		{child: "History", fk: "EpisodeId", parent: "Episodes"},
	})
}

// NewCleanupOrphanedBlocklist ports
// `Housekeepers/CleanupOrphanedBlocklist.cs:1-26`. Drops Blocklist
// rows whose SeriesId no longer points at a Series.
func NewCleanupOrphanedBlocklist(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newOrphanCleanupTask("CleanupOrphanedBlocklist", db, log, []orphanPair{
		{child: "Blocklist", fk: "SeriesId", parent: "Series"},
	})
}

// NewCleanupOrphanedIndexerStatus ports
// `Housekeepers/CleanupOrphanedIndexerStatus.cs:1-26`. Drops
// IndexerStatus rows whose ProviderId no longer points at an
// Indexers row.
func NewCleanupOrphanedIndexerStatus(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newOrphanCleanupTask("CleanupOrphanedIndexerStatus", db, log, []orphanPair{
		{child: "IndexerStatus", fk: "ProviderId", parent: "Indexers"},
	})
}

// NewCleanupOrphanedDownloadClientStatus ports
// `Housekeepers/CleanupOrphanedDownloadClientStatus.cs:1-27`. Drops
// DownloadClientStatus rows whose ProviderId no longer points at a
// DownloadClients row.
func NewCleanupOrphanedDownloadClientStatus(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newOrphanCleanupTask("CleanupOrphanedDownloadClientStatus", db, log, []orphanPair{
		{child: "DownloadClientStatus", fk: "ProviderId", parent: "DownloadClients"},
	})
}

// NewCleanupOrphanedImportListStatus ports
// `Housekeepers/CleanupOrphanedImportListStatus.cs:1-26`. Drops
// ImportListStatus rows whose ProviderId no longer points at an
// ImportLists row.
func NewCleanupOrphanedImportListStatus(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newOrphanCleanupTask("CleanupOrphanedImportListStatus", db, log, []orphanPair{
		{child: "ImportListStatus", fk: "ProviderId", parent: "ImportLists"},
	})
}

// NewCleanupOrphanedNotificationStatus ports
// `Housekeepers/CleanupOrphanedNotificationStatus.cs:1-27`. Drops
// NotificationStatus rows whose ProviderId no longer points at a
// Notifications row.
func NewCleanupOrphanedNotificationStatus(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newOrphanCleanupTask("CleanupOrphanedNotificationStatus", db, log, []orphanPair{
		{child: "NotificationStatus", fk: "ProviderId", parent: "Notifications"},
	})
}

// NewCleanupOrphanedPendingReleases ports
// `Housekeepers/CleanupOrphanedPendingReleases.cs:1-26`. Drops
// PendingReleases rows whose SeriesId no longer points at a Series.
func NewCleanupOrphanedPendingReleases(db *datastore.DB, log *slog.Logger) (Task, error) {
	return newOrphanCleanupTask("CleanupOrphanedPendingReleases", db, log, []orphanPair{
		{child: "PendingReleases", fk: "SeriesId", parent: "Series"},
	})
}

// fileRefTask is the shape used by the three "media-companion file"
// housekeepers (`CleanupOrphanedExtraFiles`, `CleanupOrphanedSubtitleFiles`,
// `CleanupOrphanedMetadataFiles`). Each runs three passes:
//
//  1. Delete rows whose SeriesId is dangling (standard
//     LEFT-JOIN-WHERE-NULL).
//  2. Delete rows where `EpisodeFileId > 0` AND the EpisodeFile is
//     gone (LEFT JOIN through EpisodeFiles, with the > 0 predicate
//     so unlinked rows aren't swept here).
//  3. Delete unlinked rows — the C# tree picks the predicate
//     (`EpisodeFileId = 0` for ExtraFiles/SubtitleFiles, additional
//     `Type IN (2,5)` for MetadataFiles).
//
// `extraUnlinkedPredicate` lets MetadataFiles add the `Type IN (2,5)`
// filter without a separate Task type; pass empty for the other two.
type fileRefTask struct {
	name                   string
	table                  string
	db                     *datastore.DB
	log                    *slog.Logger
	extraUnlinkedPredicate string
}

// Name implements Task.
func (t *fileRefTask) Name() string { return t.name }

// Clean implements Task. Runs the three-pass cleanup.
func (t *fileRefTask) Clean(ctx context.Context) error {
	// Pass 1 — series orphan.
	n, err := deleteOrphansLeftJoin(ctx, t.db, t.table, "SeriesId", "Series")
	if err != nil {
		return err
	}
	t.logIfRows(ctx, "series-orphan", n)

	// Pass 2 — EpisodeFile orphan with `EpisodeFileId > 0` predicate.
	pass2 := fmt.Sprintf(
		`DELETE FROM %q WHERE "Id" IN (
			SELECT %q."Id" FROM %q
			LEFT OUTER JOIN "EpisodeFiles" ON %q."EpisodeFileId" = "EpisodeFiles"."Id"
			WHERE %q."EpisodeFileId" > 0 AND "EpisodeFiles"."Id" IS NULL)`,
		t.table, t.table, t.table, t.table, t.table,
	)
	res, err := t.db.SQL.ExecContext(ctx, pass2)
	if err != nil {
		return fmt.Errorf("housekeeping: %s pass2: %w", t.name, err)
	}
	if n, err = res.RowsAffected(); err != nil {
		return fmt.Errorf("housekeeping: %s pass2 rowsAffected: %w", t.name, err)
	}
	t.logIfRows(ctx, "episodefile-orphan", n)

	// Pass 3 — unlinked rows (EpisodeFileId = 0, plus optional
	// per-Task predicate like `Type IN (2,5)` for MetadataFiles).
	pass3 := fmt.Sprintf(`DELETE FROM %q WHERE "EpisodeFileId" = 0`, t.table)
	if t.extraUnlinkedPredicate != "" {
		pass3 += " AND " + t.extraUnlinkedPredicate
	}
	res, err = t.db.SQL.ExecContext(ctx, pass3)
	if err != nil {
		return fmt.Errorf("housekeeping: %s pass3: %w", t.name, err)
	}
	if n, err = res.RowsAffected(); err != nil {
		return fmt.Errorf("housekeeping: %s pass3 rowsAffected: %w", t.name, err)
	}
	t.logIfRows(ctx, "unlinked", n)

	return nil
}

func (t *fileRefTask) logIfRows(ctx context.Context, pass string, n int64) {
	if n > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: deleted file-ref rows",
			slog.String("task", t.name),
			slog.String("pass", pass),
			slog.Int64("rows", n))
	}
}

// NewCleanupOrphanedExtraFiles ports
// `Housekeepers/CleanupOrphanedExtraFiles.cs:1-58`. Three-pass DELETE
// across ExtraFiles: series-orphan, episodefile-orphan (where
// EpisodeFileId > 0 AND the EpisodeFile is gone), and unlinked
// (EpisodeFileId = 0).
func NewCleanupOrphanedExtraFiles(db *datastore.DB, log *slog.Logger) (Task, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &fileRefTask{name: "CleanupOrphanedExtraFiles", table: "ExtraFiles", db: db, log: log}, nil
}

// NewCleanupOrphanedSubtitleFiles ports
// `Housekeepers/CleanupOrphanedSubtitleFiles.cs:1-58`. Same three-pass
// shape as ExtraFiles, against `SubtitleFiles`.
func NewCleanupOrphanedSubtitleFiles(db *datastore.DB, log *slog.Logger) (Task, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &fileRefTask{name: "CleanupOrphanedSubtitleFiles", table: "SubtitleFiles", db: db, log: log}, nil
}

// NewCleanupOrphanedMetadataFiles ports
// `Housekeepers/CleanupOrphanedMetadataFiles.cs:1-58`. Same three-pass
// shape as ExtraFiles, against `MetadataFiles`, with the additional
// `Type IN (2, 5)` predicate on the unlinked-rows pass — those values
// match the C# `MetadataType.EpisodeImage` (2) and
// `MetadataType.EpisodeMetadata` (5), the two episode-scoped metadata
// types for which a zero EpisodeFileId is a corruption signal. Type 1
// (SeriesImage), 3 (SeriesMetadata), 4 (SeasonImage) are series-scoped
// and legitimately store EpisodeFileId = 0.
func NewCleanupOrphanedMetadataFiles(db *datastore.DB, log *slog.Logger) (Task, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &fileRefTask{
		name:                   "CleanupOrphanedMetadataFiles",
		table:                  "MetadataFiles",
		db:                     db,
		log:                    log,
		extraUnlinkedPredicate: `"Type" IN (2, 5)`,
	}, nil
}
