package episodefiles

import (
	"context"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/datastore"
)

// AllBySeriesIDs ports `MediaFileRepository.GetFilesBySeriesIds(List<int>)`
// at `MediaFileRepository.cs:33-36`. Empty input returns an empty
// slice; missing IDs are silently skipped.
func (r *Repo) AllBySeriesIDs(ctx context.Context, seriesIDs []int) ([]File, error) {
	if len(seriesIDs) == 0 {
		return []File{}, nil
	}
	db := r.DB()
	placeholders := make([]string, len(seriesIDs))
	args := make([]any, len(seriesIDs))
	for i, id := range seriesIDs {
		placeholders[i] = datastore.Placeholder(db.Engine, i+1)
		args[i] = id
	}
	q := fmt.Sprintf(
		`SELECT %s FROM "EpisodeFiles" WHERE "SeriesId" IN (%s) ORDER BY "Id"`,
		selectColumnList(), strings.Join(placeholders, ","),
	)
	return r.runQuery(ctx, q, "AllBySeriesIDs", args...)
}

// AllBySeason ports `MediaFileRepository.GetFilesBySeason(int, int)`
// at `:38-41`.
func (r *Repo) AllBySeason(ctx context.Context, seriesID, seasonNumber int) ([]File, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "EpisodeFiles" WHERE "SeriesId" = %s AND "SeasonNumber" = %s ORDER BY "Id"`,
		selectColumnList(),
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
	)
	return r.runQuery(ctx, q, "AllBySeason", seriesID, seasonNumber)
}

// GetMany ports `BasicRepository<EpisodeFile>.Get(IEnumerable<int>)`
// — the multi-id read used by `MediaFileService.GetFiles(IEnumerable<int>)`
// at `MediaFileService.cs:88-91`. Empty input returns empty slice;
// missing IDs are silently skipped.
func (r *Repo) GetMany(ctx context.Context, ids []int) ([]File, error) {
	if len(ids) == 0 {
		return []File{}, nil
	}
	db := r.DB()
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = datastore.Placeholder(db.Engine, i+1)
		args[i] = id
	}
	q := fmt.Sprintf(
		`SELECT %s FROM "EpisodeFiles" WHERE "Id" IN (%s)`,
		selectColumnList(), strings.Join(placeholders, ","),
	)
	return r.runQuery(ctx, q, "GetMany", args...)
}

// AllWithoutMediaInfo ports
// `MediaFileRepository.GetFilesWithoutMediaInfo()` at `:43-46`.
// Returns rows whose MediaInfo column is NULL — used by
// UpdateMediaInfoService to scan for files needing a ffprobe pass.
//
// In the Go port the MediaInfo column scans as an empty string when
// the underlying value is NULL (the persistence layer uses
// `sql.NullString` and yields "" on .String for NULL). To preserve
// the C# `MediaInfo == null` semantic at the SQL layer, this
// method filters on `IS NULL` directly so post-scan callers can
// reliably treat "" as "no MediaInfo recorded".
func (r *Repo) AllWithoutMediaInfo(ctx context.Context) ([]File, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM "EpisodeFiles" WHERE "MediaInfo" IS NULL ORDER BY "Id"`,
		selectColumnList(),
	)
	return r.runQuery(ctx, q, "AllWithoutMediaInfo")
}

// AllWithRelativePath ports
// `MediaFileRepository.GetFilesWithRelativePath(int, string)` at
// `:48-52`. Used by the import pipeline's de-duplication check.
func (r *Repo) AllWithRelativePath(ctx context.Context, seriesID int, relativePath string) ([]File, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "EpisodeFiles" WHERE "SeriesId" = %s AND "RelativePath" = %s ORDER BY "Id"`,
		selectColumnList(),
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
	)
	return r.runQuery(ctx, q, "AllWithRelativePath", seriesID, relativePath)
}

// DeleteForSeries ports
// `MediaFileRepository.DeleteForSeries(List<int>)` at `:54-56`.
// Bulk DELETE matching the given SeriesIds. Returns the rows
// affected. Empty input is a no-op (0, nil).
func (r *Repo) DeleteForSeries(ctx context.Context, seriesIDs []int) (int64, error) {
	if len(seriesIDs) == 0 {
		return 0, nil
	}
	db := r.DB()
	placeholders := make([]string, len(seriesIDs))
	args := make([]any, len(seriesIDs))
	for i, id := range seriesIDs {
		placeholders[i] = datastore.Placeholder(db.Engine, i+1)
		args[i] = id
	}
	q := fmt.Sprintf(
		`DELETE FROM "EpisodeFiles" WHERE "SeriesId" IN (%s)`,
		strings.Join(placeholders, ","),
	)
	res, err := db.SQL.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("episodefiles: DeleteForSeries: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("episodefiles: DeleteForSeries: rowsAffected: %w", err)
	}
	return n, nil
}

// runQuery is the shared multi-row scanner.
func (r *Repo) runQuery(ctx context.Context, query, op string, args ...any) ([]File, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("episodefiles: %s: %w", op, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]File, 0)
	for rows.Next() {
		var f File
		if err := mapper.Scan(&f, rows); err != nil {
			return nil, fmt.Errorf("episodefiles: %s scan: %w", op, err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episodefiles: %s rows: %w", op, err)
	}
	return out, nil
}

// selectColumnList returns the canonical SELECT column list. Hand-rolled
// to keep per-method SELECTs aligned with the Mapper's scan order
// (matches the AllBySeriesID hand-rolled list at episodefiles.go:124).
func selectColumnList() string {
	return `"Id","SeriesId","Quality","Size","DateAdded","SeasonNumber","SceneName","ReleaseGroup","MediaInfo","RelativePath","OriginalFilePath","Languages","IndexerFlags","ReleaseType","ReleaseHash"`
}
