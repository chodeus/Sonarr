// CleanupUnusedTags. Ports
// `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupUnusedTags.cs:1-77`.
//
// Tags rows accumulate when users assign-then-remove tags from
// providers, profiles, or series. The C# task sweeps Tag rows whose Id
// is not referenced by any of:
//
//   - The `Tags` JSON-int-array column on each of 8 owner tables
//     (Series / Notifications / DelayProfiles / ReleaseProfiles /
//     ImportLists / Indexers / AutoTagging / DownloadClients).
//   - Any `TagSpecification` inside an AutoTag's `Specifications`
//     envelope — those reference Tag IDs by value.
//
// Implementation:
//
// The C# version ships the `usedTags` set into the SQL DELETE via
// string interpolation (`Id IN (1,2,3)` for SQLite, `= ANY('{1,2,3}'::int[])`
// for Postgres). The Go port adopts the same shape — we have a
// pre-computed integer set, no need for placeholders. Empty-set is
// special-cased: `WHERE NOT IN ()` is invalid SQL on SQLite, so we
// fall back to `DELETE FROM "Tags"` (sweep everything) when no Tag is
// in use.

package housekeeping

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/datastore"
)

// taggedTables ports the 8-element table list at C# `:27-30`. Order
// is preserved purely to keep diff-noise low against the C# tree;
// the per-table query is order-independent.
var taggedTables = []string{
	"Series",
	"Notifications",
	"DelayProfiles",
	"ReleaseProfiles",
	"ImportLists",
	"Indexers",
	"AutoTagging",
	"DownloadClients",
}

// CleanupUnusedTags is the housekeeper that drops Tags rows whose Id
// isn't referenced anywhere.
type CleanupUnusedTags struct {
	db          *datastore.DB
	autoTagRepo *autotagging.Repo
	log         *slog.Logger
}

// NewCleanupUnusedTags builds the housekeeper. db must be non-nil;
// autoTagRepo may be nil — when nil, the AutoTag-Specifications scan
// is skipped (callers without autotagging wired still get the 8-table
// sweep). log defaults to slog.Default.
//
// The C# DI graph wires both unconditionally; the Go port keeps the
// nil-tolerance pattern established by other housekeepers
// (UpdateCleanTitleForSeries, etc.).
func NewCleanupUnusedTags(db *datastore.DB, autoTagRepo *autotagging.Repo, log *slog.Logger) (*CleanupUnusedTags, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	if log == nil {
		log = slog.Default()
	}
	return &CleanupUnusedTags{db: db, autoTagRepo: autoTagRepo, log: log}, nil
}

// Name implements Task.
func (t *CleanupUnusedTags) Name() string { return "CleanupUnusedTags" }

// Clean implements Task. Builds the in-use Tag set from every owner
// table + every AutoTag TagSpecification value, then DELETEs rows
// not in that set.
func (t *CleanupUnusedTags) Clean(ctx context.Context) error {
	used := make(map[int]struct{})

	for _, table := range taggedTables {
		if err := t.collectTagsFromTable(ctx, table, used); err != nil {
			return err
		}
	}

	if t.autoTagRepo != nil {
		tags, err := autotagging.LoadAll(ctx, t.autoTagRepo)
		if err != nil {
			return fmt.Errorf("housekeeping: CleanupUnusedTags: load autotagging: %w", err)
		}
		for _, at := range tags {
			for _, spec := range at.Specifications {
				if ts, ok := spec.(*autotagging.TagSpec); ok {
					used[ts.Value] = struct{}{}
				}
			}
		}
	}

	var query string
	if len(used) == 0 {
		// No tags in use → sweep every Tag row.
		query = `DELETE FROM "Tags"`
	} else {
		ids := make([]string, 0, len(used))
		for id := range used {
			ids = append(ids, strconv.Itoa(id))
		}
		query = fmt.Sprintf(`DELETE FROM "Tags" WHERE "Id" NOT IN (%s)`, strings.Join(ids, ","))
	}
	res, err := t.db.SQL.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupUnusedTags: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupUnusedTags: rowsAffected: %w", err)
	}
	if n > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: deleted unused Tag rows",
			slog.Int64("rows", n),
			slog.Int("usedTags", len(used)))
	}
	return nil
}

// collectTagsFromTable runs `SELECT DISTINCT "Tags" FROM <table>
// WHERE NOT "Tags" = '[]' AND NOT "Tags" IS NULL`, then unmarshals
// each result into the `used` set. Mirrors C# `GetUsedTags` at `:49-57`.
func (t *CleanupUnusedTags) collectTagsFromTable(ctx context.Context, table string, used map[int]struct{}) error {
	q := fmt.Sprintf(
		`SELECT DISTINCT "Tags" FROM %q WHERE NOT "Tags" = '[]' AND NOT "Tags" IS NULL`,
		table,
	)
	rows, err := t.db.SQL.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupUnusedTags: select %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var tagsJSON sql.NullString
		if err := rows.Scan(&tagsJSON); err != nil {
			return fmt.Errorf("housekeeping: CleanupUnusedTags: scan %s: %w", table, err)
		}
		if !tagsJSON.Valid || tagsJSON.String == "" {
			continue
		}
		var ids []int
		if err := datastore.UnmarshalEmbedded(tagsJSON.String, &ids); err != nil {
			// A row whose Tags column is unparseable JSON is a
			// data-integrity bug, not a housekeeping cleanup target —
			// surface it rather than silently skip.
			return fmt.Errorf("housekeeping: CleanupUnusedTags: decode %s.Tags %q: %w", table, tagsJSON.String, err)
		}
		for _, id := range ids {
			used[id] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("housekeeping: CleanupUnusedTags: rows %s: %w", table, err)
	}
	return nil
}
