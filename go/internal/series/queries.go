package series

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/datastore"
)

// FindByTvRageID ports `SeriesRepository.FindByTvRageId(int tvRageId)` at
// `SeriesRepository.cs:73-76`. Returns ErrNotFound on miss; SingleOrDefault
// in C# returns null which the Go port surfaces as a clean not-found.
func (r *Repo) FindByTvRageID(ctx context.Context, tvRageID int) (*Series, error) {
	return r.firstWhere(ctx, `"TvRageId" = `+datastore.Placeholder(r.DB().Engine, 1), tvRageID)
}

// FindByImdbID ports `SeriesRepository.FindByImdbId(string imdbId)` at
// `:78-81`.
func (r *Repo) FindByImdbID(ctx context.Context, imdbID string) (*Series, error) {
	return r.firstWhere(ctx, `"ImdbId" = `+datastore.Placeholder(r.DB().Engine, 1), imdbID)
}

// FindByPath ports `SeriesRepository.FindByPath(string path)` at `:83-87`.
// The C# version uses `FirstOrDefault()` because Path is not unique
// (defensive — schema doesn't enforce). The Go port preserves the
// first-row semantic.
func (r *Repo) FindByPath(ctx context.Context, path string) (*Series, error) {
	return r.firstWhere(ctx, `"Path" = `+datastore.Placeholder(r.DB().Engine, 1), path)
}

// FindByTitleSlug returns the first row whose TitleSlug matches.
// The C# tree iterates `_seriesService.GetAllSeries().FirstOrDefault(...)`
// at `SeriesTitleSlugValidator.cs:30-33` — i.e. scans every row in
// memory. The Go port presses the lookup down to a single-row query
// so the validator stays O(1) DB-side instead of pulling the whole
// table.
//
// Empty slug returns ErrNotFound without a query (mirrors the
// C# guard at `:21-24` returning IsValid=true when PropertyValue is
// null).
func (r *Repo) FindByTitleSlug(ctx context.Context, slug string) (*Series, error) {
	if slug == "" {
		return nil, ErrNotFound
	}
	return r.firstWhere(ctx, `"TitleSlug" = `+datastore.Placeholder(r.DB().Engine, 1), slug)
}

// FindByTitle ports `SeriesRepository.FindByTitle(string cleanTitle)` at
// `:37-45`. Caller passes the cleaned title (the service layer applies
// `parser.CleanSeriesTitle` first, mirroring `SeriesService.cs:106`).
//
// The C# version lower-cases `cleanTitle` before the equality check; the
// CleanTitle column is also lowercased on persist (the Series.Bind
// pipeline does that, matching the C# `Series.CleanTitle` setter). The
// Go port lower-cases here too for safety.
//
// On multiple matches, returns [ErrMultipleSeriesFound] — the C# version
// throws `MultipleSeriesFoundException` at `:136`.
func (r *Repo) FindByTitle(ctx context.Context, cleanTitle string) (*Series, error) {
	cleanTitle = strings.ToLower(cleanTitle)
	rows, err := r.queryWhere(ctx, `"CleanTitle" = `+datastore.Placeholder(r.DB().Engine, 1), cleanTitle)
	if err != nil {
		return nil, err
	}
	return singleOrThrow(rows)
}

// FindByTitleAndYear ports `SeriesRepository.FindByTitle(string, int)` at
// `:47-54`. Same multi-match semantics as [FindByTitle].
func (r *Repo) FindByTitleAndYear(ctx context.Context, cleanTitle string, year int) (*Series, error) {
	cleanTitle = strings.ToLower(cleanTitle)
	db := r.DB()
	q := fmt.Sprintf(`"CleanTitle" = %s AND "Year" = %s`,
		datastore.Placeholder(db.Engine, 1), datastore.Placeholder(db.Engine, 2))
	rows, err := r.queryWhere(ctx, q, cleanTitle, year)
	if err != nil {
		return nil, err
	}
	return singleOrThrow(rows)
}

// FindByTitleInexact ports `SeriesRepository.FindByTitleInexact` at
// `:56-66`. Returns every series whose CleanTitle is a substring of
// `cleanTitle`. SQLite uses `instr(... > 0)`; Postgres uses
// `strpos(... > 0)` semantically. Both are case-sensitive but the
// CleanTitle column is normalized to lowercase on persist, so the
// caller is responsible for lowercasing.
//
// The further service-layer scoring (longest-leftmost match) is in
// [Service.FindByTitleInexact], not here.
func (r *Repo) FindByTitleInexact(ctx context.Context, cleanTitle string) ([]Series, error) {
	cleanTitle = strings.ToLower(cleanTitle)
	db := r.DB()
	var clause string
	switch db.Engine {
	case datastore.EnginePostgres:
		clause = fmt.Sprintf(`strpos(%s, "CleanTitle") > 0`, datastore.Placeholder(db.Engine, 1))
	default:
		clause = fmt.Sprintf(`instr(%s, "CleanTitle") > 0`, datastore.Placeholder(db.Engine, 1))
	}
	return r.queryWhere(ctx, clause, cleanTitle)
}

// SeriesPathExists ports `SeriesRepository.SeriesPathExists(string path)`
// at `:32-35`. Returns true iff at least one row's Path equals `path`.
func (r *Repo) SeriesPathExists(ctx context.Context, path string) (bool, error) {
	db := r.DB()
	q := fmt.Sprintf(`SELECT COUNT(1) FROM "Series" WHERE "Path" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var count int
	if err := db.SQL.QueryRowContext(ctx, q, path).Scan(&count); err != nil {
		return false, fmt.Errorf("series: SeriesPathExists(%q): %w", path, err)
	}
	return count > 0, nil
}

// AllSeriesPaths ports `SeriesRepository.AllSeriesPaths()` at `:97-104`.
// Returns a fresh `Id → Path` map — the same shape `RootFolderService`
// expects via the [rootfolders.SeriesPaths] interface.
func (r *Repo) AllSeriesPaths(ctx context.Context) (map[int]string, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, `SELECT "Id", "Path" FROM "Series"`)
	if err != nil {
		return nil, fmt.Errorf("series: AllSeriesPaths: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int]string)
	for rows.Next() {
		var id int
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, fmt.Errorf("series: AllSeriesPaths: scan: %w", err)
		}
		out[id] = path
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("series: AllSeriesPaths: rows: %w", err)
	}
	return out, nil
}

// AllSeriesTvdbIDs ports `SeriesRepository.AllSeriesTvdbIds()` at `:89-95`.
// Returns the raw column values; rows with TvdbId == 0 (placeholder
// "missing") are included to match the C# `SELECT "TvdbId" FROM
// "Series"` semantic.
func (r *Repo) AllSeriesTvdbIDs(ctx context.Context) ([]int, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, `SELECT "TvdbId" FROM "Series"`)
	if err != nil {
		return nil, fmt.Errorf("series: AllSeriesTvdbIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]int, 0)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("series: AllSeriesTvdbIDs: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("series: AllSeriesTvdbIDs: rows: %w", err)
	}
	return out, nil
}

// AllSeriesTags ports `SeriesRepository.AllSeriesTags()` at `:106-113`.
// Returns `Id → []int tags`. Rows with a NULL Tags column are excluded
// (matches the C# `WHERE "Tags" IS NOT NULL` clause). Rows with `[]`
// JSON tags are included with an empty slice.
func (r *Repo) AllSeriesTags(ctx context.Context) (map[int][]int, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, `SELECT "Id", "Tags" FROM "Series" WHERE "Tags" IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("series: AllSeriesTags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int][]int)
	for rows.Next() {
		var id int
		var tagsJSON string
		if err := rows.Scan(&id, &tagsJSON); err != nil {
			return nil, fmt.Errorf("series: AllSeriesTags: scan: %w", err)
		}
		var tags []int
		if tagsJSON != "" {
			if err := datastore.UnmarshalEmbedded(tagsJSON, &tags); err != nil {
				return nil, fmt.Errorf("series: AllSeriesTags: unmarshal id=%d: %w", id, err)
			}
		}
		if tags == nil {
			tags = []int{}
		}
		out[id] = tags
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("series: AllSeriesTags: rows: %w", err)
	}
	return out, nil
}

// AllSeriesQualityProfiles ports
// `SeriesRepository.AllSeriesQualityProfiles()` at `:115-122`. Returns
// `Id → QualityProfileId`. Rows whose QualityProfileId is NULL are
// included with the value 0 — matches the C# default-int unmarshalling
// semantic (KeyValuePair<int, int> with NULL value coerces to 0).
func (r *Repo) AllSeriesQualityProfiles(ctx context.Context) (map[int]int, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, `SELECT "Id", "QualityProfileId" FROM "Series"`)
	if err != nil {
		return nil, fmt.Errorf("series: AllSeriesQualityProfiles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int]int)
	for rows.Next() {
		var id int
		var profileID sql.NullInt64
		if err := rows.Scan(&id, &profileID); err != nil {
			return nil, fmt.Errorf("series: AllSeriesQualityProfiles: scan: %w", err)
		}
		if profileID.Valid {
			out[id] = int(profileID.Int64)
		} else {
			out[id] = 0
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("series: AllSeriesQualityProfiles: rows: %w", err)
	}
	return out, nil
}

// ErrMultipleSeriesFound is returned by [Repo.FindByTitle] /
// [Repo.FindByTitleAndYear] when more than one row matches — mirrors
// the C# `MultipleSeriesFoundException` thrown by
// `ReturnSingleSeriesOrThrow` at `SeriesRepository.cs:136`. Callers
// should classify via `errors.Is`.
var ErrMultipleSeriesFound = errors.New("series: multiple series matched")

// firstWhere is the shared "single-or-nil" helper for the FindBy*
// methods that use SingleOrDefault in C#. Returns ErrNotFound on miss.
func (r *Repo) firstWhere(ctx context.Context, clause string, args ...any) (*Series, error) {
	db := r.DB()
	q := fmt.Sprintf(`SELECT %s FROM "Series" WHERE %s LIMIT 1`, selectColumnList(), clause)
	row := db.SQL.QueryRowContext(ctx, q, args...)
	var s Series
	if err := mapper.Scan(&s, row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("series: firstWhere(%q): %w", clause, err)
	}
	return &s, nil
}

// queryWhere runs the WHERE clause and scans every matching row into a
// []Series. Used by the multi-row queries (FindByTitleInexact + the
// single-or-throw helpers).
func (r *Repo) queryWhere(ctx context.Context, clause string, args ...any) ([]Series, error) {
	db := r.DB()
	q := fmt.Sprintf(`SELECT %s FROM "Series" WHERE %s`, selectColumnList(), clause)
	rows, err := db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("series: queryWhere(%q): %w", clause, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Series
	for rows.Next() {
		var s Series
		if err := mapper.Scan(&s, rows); err != nil {
			return nil, fmt.Errorf("series: queryWhere scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("series: queryWhere rows: %w", err)
	}
	return out, nil
}

func singleOrThrow(rows []Series) (*Series, error) {
	switch len(rows) {
	case 0:
		return nil, ErrNotFound
	case 1:
		s := rows[0]
		return &s, nil
	default:
		return nil, fmt.Errorf("%w: matched %d rows", ErrMultipleSeriesFound, len(rows))
	}
}
