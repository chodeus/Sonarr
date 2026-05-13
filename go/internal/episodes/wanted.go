package episodes

import (
	"context"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
)

// WantedMissing returns every Episode satisfying the "missing"
// predicate that drives `/api/v3/wanted/missing` — mirrors
// `EpisodeRepository.EpisodesWithoutFiles` at
// `src/NzbDrone.Core/Tv/EpisodeRepository.cs:110-122` with
// `includeSpecials=true` (the MissingController hard-codes that
// argument, so the Go port matches).
//
// Predicate:
//
//   - JOIN Series ON Series.Id = Episodes.SeriesId
//   - Episodes.EpisodeFileId IS NULL (C# uses `EpisodeFileId == 0`
//     but the Go schema stores 0/unset as NULL — both map to the same
//     "no file linked" semantic)
//   - Episodes.SeasonNumber >= 0 (includeSpecials=true → startingSeasonNumber=0)
//   - AirDateUtc + Runtime*60s <= currentTime (the episode has fully
//     aired by the cutoff; SQL expressed per-engine, see
//     `BuildAirDateUtcCutoffWhereClause` in the C# original)
//   - When monitoredOnly=true (the controller's default), Episodes.Monitored
//     == true AND Series.Monitored == true (Episodes.Monitored is
//     nullable bool — NULL is treated as false, same as C#'s `==
//     true` Linq-to-SQL emit)
//
// The C# implementation pages via SqlBuilder + Dapper; the Go port
// returns the full matching set and the caller (host_wiring's
// `wantedRepoLister`) pages in memory — same shape as the blocklist
// + history adapters introduced in slice 4.EC. SortKey /
// SortDirection at the SQL level remain DEFERRED until a richer
// paged-query helper lands across the repo family.
//
// Sort: SQL emits ORDER BY AirDateUtc DESC, Id ASC so the in-memory
// pager observes the same deterministic ordering the C# default
// produces when sortDirection=descending (the default the recorded
// corpus uses).
func (r *Repo) WantedMissing(ctx context.Context, currentTime time.Time, monitoredOnly bool) ([]Episode, error) {
	db := r.DB()
	cutoff := buildAirDateUtcCutoffClause(db.Engine, currentTime.UTC())
	whereMonitored := ""
	if monitoredOnly {
		t := sqlBoolTrue(db.Engine)
		whereMonitored = fmt.Sprintf(` AND "Episodes"."Monitored" = %s AND "Series"."Monitored" = %s `, t, t)
	}
	q := fmt.Sprintf(
		`SELECT %s FROM "Episodes" `+
			`INNER JOIN "Series" ON "Series"."Id" = "Episodes"."SeriesId" `+
			`WHERE "Episodes"."EpisodeFileId" IS NULL `+
			`AND "Episodes"."SeasonNumber" >= 0 `+
			`AND %s `+
			`%s `+
			`ORDER BY "Episodes"."AirDateUtc" DESC, "Episodes"."Id" ASC`,
		qualifiedSelectColumnList(), cutoff, whereMonitored)
	rows, err := db.SQL.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("episodes: wantedMissing: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Episode
	for rows.Next() {
		var e Episode
		if err := mapper.Scan(&e, rows); err != nil {
			return nil, fmt.Errorf("episodes: wantedMissing scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episodes: wantedMissing rows: %w", err)
	}
	return out, nil
}

// buildAirDateUtcCutoffClause mirrors
// `EpisodeRepository.BuildAirDateUtcCutoffWhereClause`. The cutoff
// is "the episode has aired AND fully run its runtime by now" —
// AirDateUtc + Runtime minutes <= currentTime. Each engine spells
// the arithmetic differently:
//
//   - Postgres: `AirDateUtc + make_interval(mins => Series.Runtime)`
//   - SQLite : `datetime(strftime('%s', substr(AirDateUtc,1,19)) + Series.Runtime*60, 'unixepoch')`
//
// The C# version interpolates the timestamp literal directly into
// the SQL — we do the same since `time.Time` doesn't bind cleanly
// across both drivers for an arithmetic comparison and the value
// comes from server-side `time.Now()`, not user input.
//
// SQLite-specific note: modernc.org/sqlite stores time.Time using
// Go's default `time.String()` format
// (`"2026-05-13 12:00:00 +0000 UTC"`), which strftime can't parse
// directly. `substr(..., 1, 19)` strips the trailing ` +0000 UTC`
// suffix and leaves the `YYYY-MM-DD HH:MM:SS` head that strftime
// accepts. The original C# stack stored ISO timestamps that
// strftime handled natively — the Go-port reaches the same result
// via the substring trim.
func buildAirDateUtcCutoffClause(engine datastore.Engine, currentTime time.Time) string {
	ts := currentTime.Format("2006-01-02 15:04:05")
	if engine == datastore.EnginePostgres {
		return fmt.Sprintf(`"Episodes"."AirDateUtc" + make_interval(mins => "Series"."Runtime") <= '%s'`, ts)
	}
	return fmt.Sprintf(`datetime(strftime('%%s', substr("Episodes"."AirDateUtc", 1, 19)) + "Series"."Runtime" * 60, 'unixepoch') <= '%s'`, ts)
}
