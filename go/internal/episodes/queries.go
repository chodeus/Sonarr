package episodes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
)

// Find ports `EpisodeRepository.Find(int seriesId, int season, int episodeNumber)`
// at `EpisodeRepository.cs:53-57`. Returns ErrNotFound on miss; the C#
// `SingleOrDefault()` returns null which the Go port surfaces as the
// sentinel.
//
// (SeriesId, SeasonNumber, EpisodeNumber) is the natural unique key but
// not enforced as a UNIQUE constraint at the schema level. Multiple
// matches are possible in principle and are silently coalesced to the
// first row (matches `SingleOrDefault` — extra rows are ignored).
func (r *Repo) Find(ctx context.Context, seriesID, seasonNumber, episodeNumber int) (*Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "SeriesId" = %s AND "SeasonNumber" = %s AND "EpisodeNumber" = %s LIMIT 1`,
		selectColumnList(),
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
		datastore.Placeholder(db.Engine, 3),
	)
	return r.firstFromRow(ctx, q, seriesID, seasonNumber, episodeNumber)
}

// FindByAbsolute ports `EpisodeRepository.Find(int seriesId, int absoluteEpisodeNumber)`
// at `EpisodeRepository.cs:59-63`.
func (r *Repo) FindByAbsolute(ctx context.Context, seriesID, absoluteEpisodeNumber int) (*Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "SeriesId" = %s AND "AbsoluteEpisodeNumber" = %s LIMIT 1`,
		selectColumnList(),
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
	)
	return r.firstFromRow(ctx, q, seriesID, absoluteEpisodeNumber)
}

// FindByAirDate ports `EpisodeRepository.Find(int seriesId, string date)`
// at `:65-68`. Returns every episode whose AirDate (string) equals
// `date`. Multiple matches are normal (specials + regular share an
// air date); the service layer applies the FindOneByAirDate disambiguation.
func (r *Repo) FindByAirDate(ctx context.Context, seriesID int, date string) ([]Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "SeriesId" = %s AND "AirDate" = %s ORDER BY "SeasonNumber", "EpisodeNumber"`,
		selectColumnList(),
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
	)
	return r.runQuery(ctx, q, "FindByAirDate", seriesID, date)
}

// AllBySeriesIDs ports `EpisodeRepository.GetEpisodesBySeriesIds(List<int>)`
// at `:80-83`. Empty input returns an empty slice (matches the C#
// `Contains` semantic on an empty list — no rows match).
func (r *Repo) AllBySeriesIDs(ctx context.Context, seriesIDs []int) ([]Episode, error) {
	if len(seriesIDs) == 0 {
		return []Episode{}, nil
	}
	db := r.DB()
	placeholders := make([]string, len(seriesIDs))
	args := make([]any, len(seriesIDs))
	for i, id := range seriesIDs {
		placeholders[i] = datastore.Placeholder(db.Engine, i+1)
		args[i] = id
	}
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "SeriesId" IN (%s) ORDER BY "SeriesId", "SeasonNumber", "EpisodeNumber"`,
		selectColumnList(), strings.Join(placeholders, ","),
	)
	return r.runQuery(ctx, q, "AllBySeriesIDs", args...)
}

// AllByFileID ports `EpisodeRepository.GetEpisodeByFileId(int fileId)`
// at `:90-93`. Returns every episode linked to the given EpisodeFileId.
// Multi-episode files (anime double-bills) link two or more episodes
// to the same EpisodeFile, so the result is intentionally a list.
func (r *Repo) AllByFileID(ctx context.Context, fileID int) ([]Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "EpisodeFileId" = %s ORDER BY "SeasonNumber", "EpisodeNumber"`,
		selectColumnList(), datastore.Placeholder(db.Engine, 1),
	)
	return r.runQuery(ctx, q, "AllByFileID", fileID)
}

// AllBySceneSeason ports `EpisodeRepository.GetEpisodesBySceneSeason(int, int)`
// at `:85-88`. Filters on the persisted SceneSeasonNumber column;
// rows whose SceneSeasonNumber is NULL are excluded (NULL = sceneSeason
// is never true at SQL level).
func (r *Repo) AllBySceneSeason(ctx context.Context, seriesID, sceneSeasonNumber int) ([]Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "SeriesId" = %s AND "SceneSeasonNumber" = %s ORDER BY "SceneEpisodeNumber"`,
		selectColumnList(),
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
	)
	return r.runQuery(ctx, q, "AllBySceneSeason", seriesID, sceneSeasonNumber)
}

// FindBySceneNumbering ports
// `EpisodeRepository.FindEpisodesBySceneNumbering(int seriesId, int seasonNumber, int episodeNumber)`
// at `:143-146`. Multi-episode scene numbering (e.g. anime
// double-episode releases mapped to two episodes) means the result is
// a list, not a single row — matches the C# `.ToList()`.
func (r *Repo) FindBySceneNumbering(ctx context.Context, seriesID, sceneSeasonNumber, sceneEpisodeNumber int) ([]Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "SeriesId" = %s AND "SceneSeasonNumber" = %s AND "SceneEpisodeNumber" = %s ORDER BY "EpisodeNumber"`,
		selectColumnList(),
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
		datastore.Placeholder(db.Engine, 3),
	)
	return r.runQuery(ctx, q, "FindBySceneNumbering", seriesID, sceneSeasonNumber, sceneEpisodeNumber)
}

// FindBySceneAbsolute ports
// `EpisodeRepository.FindEpisodesBySceneNumbering(int seriesId, int sceneAbsoluteEpisodeNumber)`
// at `:148-151`. Same multi-row semantic as the season+episode variant.
func (r *Repo) FindBySceneAbsolute(ctx context.Context, seriesID, sceneAbsoluteEpisodeNumber int) ([]Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "SeriesId" = %s AND "SceneAbsoluteEpisodeNumber" = %s ORDER BY "EpisodeNumber"`,
		selectColumnList(),
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
	)
	return r.runQuery(ctx, q, "FindBySceneAbsolute", seriesID, sceneAbsoluteEpisodeNumber)
}

// EpisodesBetweenDates ports
// `EpisodeRepository.EpisodesBetweenDates(DateTime, DateTime, bool, bool)`
// at `:153-170`. Filters by AirDateUtc inclusive of both bounds; flags
// control whether unmonitored episodes and specials (SeasonNumber == 0)
// are included.
//
// Implementation notes:
//
//   - When `includeUnmonitored == false` the C# version JOINs on Series
//     and requires `Series.Monitored == true` as well. The Go port
//     uses an EXISTS subquery to avoid coupling this method to the
//     series package. Same row set, simpler dependencies.
//   - AirDateUtc bounds are converted to UTC at the call boundary
//     (matches `DateTime.ToUniversalTime()` in the C# `EpisodeService`
//     wrapper at `EpisodeService.cs:206`).
//   - Result is ordered by AirDateUtc ascending — the C# version
//     relies on default ordering; the Go port pins it for stability
//     against the calendar widget.
func (r *Repo) EpisodesBetweenDates(ctx context.Context, start, end time.Time, includeUnmonitored, includeSpecials bool) ([]Episode, error) {
	db := r.DB()
	clauses := []string{
		`"AirDateUtc" >= ` + datastore.Placeholder(db.Engine, 1),
		`"AirDateUtc" <= ` + datastore.Placeholder(db.Engine, 2),
	}
	args := []any{start.UTC(), end.UTC()}
	if !includeSpecials {
		clauses = append(clauses, `"SeasonNumber" != 0`)
	}
	if !includeUnmonitored {
		clauses = append(clauses,
			`"Monitored" = `+sqlBoolTrue(db.Engine),
			`EXISTS (SELECT 1 FROM "Series" WHERE "Series"."Id" = "Episodes"."SeriesId" AND "Series"."Monitored" = `+sqlBoolTrue(db.Engine)+`)`,
		)
	}
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE %s ORDER BY "AirDateUtc"`,
		selectColumnList(), strings.Join(clauses, " AND "),
	)
	return r.runQuery(ctx, q, "EpisodesBetweenDates", args...)
}

// EpisodesWithFiles ports `EpisodeRepository.EpisodesWithFiles(int seriesId)`
// at `:95-108`. Returns every episode in the series whose
// EpisodeFileId resolves to a real EpisodeFiles row.
//
// The C# version JOINs on EpisodeFiles and re-hydrates the join via
// `QueryJoined<Episode, EpisodeFile>`. The Go port doesn't yet have an
// EpisodeFile struct (Phase 3 mediafiles), so the join is expressed as
// an INNER JOIN that filters but doesn't project — the returned
// Episode rows have `EpisodeFile` left unset. Once mediafiles ships,
// this method can grow a sister `EpisodesWithFilesJoined` that
// projects the joined columns into a richer return type.
func (r *Repo) EpisodesWithFiles(ctx context.Context, seriesID int) ([]Episode, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" INNER JOIN "EpisodeFiles" ON "Episodes"."EpisodeFileId" = "EpisodeFiles"."Id" WHERE "Episodes"."SeriesId" = %s ORDER BY "Episodes"."SeasonNumber", "Episodes"."EpisodeNumber"`,
		qualifiedSelectColumnList(), datastore.Placeholder(db.Engine, 1),
	)
	return r.runQuery(ctx, q, "EpisodesWithFiles", seriesID)
}

// EpisodesWithoutFiles ports the C# `EpisodeService.EpisodesWithoutFiles`
// at `:200-216`. Returns every episode whose `EpisodeFileId` is NULL
// (or 0) AND has aired before `now` AND whose monitored state matches
// `monitored`. When `monitored == true` the Series must also be
// monitored (matches C# `:130-138` filter clause `v.Monitored == true
// && v.Series.Monitored == true`). When `monitored == false`, the
// filter inverts to `v.Monitored == false || v.Series.Monitored ==
// false` (the C# expression at `:140-143`).
//
// Used by the `MissingEpisodeSearch` command handler (slice 5.J39 —
// audit B.3 close-out). Specials (SeasonNumber == 0) are excluded
// — the C# missing-search wraps PagingSpec with the
// `IncludeSpecialEpisodes = false` default; missing-search never
// considers specials.
func (r *Repo) EpisodesWithoutFiles(ctx context.Context, monitored bool, now time.Time) ([]Episode, error) {
	db := r.DB()
	clauses := []string{
		`("EpisodeFileId" IS NULL OR "EpisodeFileId" = 0)`,
		`"AirDateUtc" IS NOT NULL`,
		`"AirDateUtc" < ` + datastore.Placeholder(db.Engine, 1),
		`"SeasonNumber" != 0`,
	}
	args := []any{now.UTC()}

	if monitored {
		clauses = append(clauses,
			`"Monitored" = `+sqlBoolTrue(db.Engine),
			`EXISTS (SELECT 1 FROM "Series" WHERE "Series"."Id" = "Episodes"."SeriesId" AND "Series"."Monitored" = `+sqlBoolTrue(db.Engine)+`)`,
		)
	} else {
		clauses = append(clauses,
			`("Monitored" = `+sqlBoolFalse(db.Engine)+
				` OR EXISTS (SELECT 1 FROM "Series" WHERE "Series"."Id" = "Episodes"."SeriesId" AND "Series"."Monitored" = `+sqlBoolFalse(db.Engine)+`))`,
		)
	}
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE %s ORDER BY "Id"`,
		selectColumnList(), strings.Join(clauses, " AND "),
	)
	return r.runQuery(ctx, q, "EpisodesWithoutFiles", args...)
}

// GetMany ports `BasicRepository<Episode>.Get(IEnumerable<int>)` —
// the multi-id read used by `EpisodeService.GetEpisodes(IEnumerable<int>)`
// at `EpisodeService.cs:68-71`. Empty input returns an empty slice.
// Missing IDs are silently dropped; the C# `BasicRepository.Get(ids)`
// only returns matching rows.
func (r *Repo) GetMany(ctx context.Context, ids []int) ([]Episode, error) {
	if len(ids) == 0 {
		return []Episode{}, nil
	}
	db := r.DB()
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = datastore.Placeholder(db.Engine, i+1)
		args[i] = id
	}
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" WHERE "Id" IN (%s)`,
		selectColumnList(), strings.Join(placeholders, ","),
	)
	return r.runQuery(ctx, q, "GetMany", args...)
}

// firstFromRow runs a SELECT-LIMIT-1 query and scans one row. Returns
// ErrNotFound when no row matches. Unlike scanQuery this uses
// QueryRowContext so the not-found path doesn't allocate a Rows.
func (r *Repo) firstFromRow(ctx context.Context, q string, args ...any) (*Episode, error) {
	row := r.DB().SQL.QueryRowContext(ctx, q, args...)
	var e Episode
	if err := mapper.Scan(&e, row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("episodes: firstFromRow: %w", err)
	}
	return &e, nil
}

// runQuery runs a multi-row SELECT and scans every row into a fresh
// []Episode. Used by every list-returning helper above. Variadic args
// keeps the call sites short.
func (r *Repo) runQuery(ctx context.Context, query, op string, args ...any) ([]Episode, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("episodes: %s: %w", op, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Episode, 0)
	for rows.Next() {
		var e Episode
		if err := mapper.Scan(&e, rows); err != nil {
			return nil, fmt.Errorf("episodes: %s scan: %w", op, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episodes: %s rows: %w", op, err)
	}
	return out, nil
}

// qualifiedSelectColumnList returns the column list with `"Episodes".`
// table-qualifier prefix. Required for queries that JOIN another table
// — the bare list collides on `"Id"` between Episodes and EpisodeFiles.
func qualifiedSelectColumnList() string {
	return `"Episodes"."Id","Episodes"."SeriesId","Episodes"."SeasonNumber","Episodes"."EpisodeNumber","Episodes"."Title","Episodes"."Overview",` +
		`"Episodes"."EpisodeFileId","Episodes"."AbsoluteEpisodeNumber","Episodes"."SceneAbsoluteEpisodeNumber",` +
		`"Episodes"."SceneSeasonNumber","Episodes"."SceneEpisodeNumber","Episodes"."Monitored","Episodes"."AirDateUtc","Episodes"."AirDate",` +
		`"Episodes"."Ratings","Episodes"."Images","Episodes"."UnverifiedSceneNumbering","Episodes"."LastSearchTime",` +
		`"Episodes"."AiredAfterSeasonNumber","Episodes"."AiredBeforeSeasonNumber","Episodes"."AiredBeforeEpisodeNumber",` +
		`"Episodes"."TvdbId","Episodes"."Runtime","Episodes"."FinaleType"`
}

// sqlBoolTrue returns the engine-appropriate literal for boolean true.
// SQLite has no boolean type — booleans persist as integer 0/1 — so the
// `BOOLEAN` column comparison must be against `1`, not `TRUE`. Postgres
// has a native boolean and uses `TRUE`.
func sqlBoolTrue(engine datastore.Engine) string {
	if engine == datastore.EnginePostgres {
		return "TRUE"
	}
	return "1"
}

// sqlBoolFalse is the false counterpart to [sqlBoolTrue].
func sqlBoolFalse(engine datastore.Engine) string {
	if engine == datastore.EnginePostgres {
		return "FALSE"
	}
	return "0"
}
