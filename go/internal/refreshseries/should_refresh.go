package refreshseries

import (
	"context"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// EpisodeLookup is the narrow slice of [*episodes.Repo] (or any other
// per-series episode source) that [ShouldRefresh] reaches for. The
// predicate inspects every episode in a series row to detect the
// "aired-episode-with-TBA-title" guard at C# ShouldRefreshSeries.cs:34-43.
type EpisodeLookup interface {
	AllBySeriesID(ctx context.Context, seriesID int) ([]episodes.Episode, error)
}

// ShouldRefresh ports `ShouldRefreshSeries.ShouldRefresh(Series)` at
// `src/NzbDrone.Core/Tv/ShouldRefreshSeries.cs:25-77`. Returns true
// when the row warrants a refresh; false to skip. On any error
// classifying the row, returns true — matches the C#
// `catch (Exception) → return true` posture so a transient lookup
// failure errs on the side of refreshing rather than skipping.
//
// Decision tree (line-aligned with C#):
//
//   - `:27-30` if LastInfoSync < now - 30d → refresh (forces stale
//     rows to refresh even if the rest of the predicate would skip).
//   - `:33-43` if any episode with SeasonNumber > 0 has AirDateUtc
//     in the past AND Title == "TBA" → refresh (TheTVDB filled in
//     the title since last sync).
//   - `:45-49` if LastInfoSync >= now - 6h → skip (recent enough).
//   - `:51-54` if Status != Ended → refresh (active series get more
//     frequent updates regardless of LastInfoSync recency).
//   - `:56-62` for ended series: lastEpisode = MaxBy(AirDateUtc); if
//     lastEpisode.AirDateUtc > now - 30d → refresh (still in the
//     trailing 30-day window even after end).
//   - else → skip (ended long ago, no recent activity).
//
// `now` defaults to `time.Now().UTC()` when zero — pinning is for
// deterministic tests. nil log defaults to slog.Default.
func ShouldRefresh(ctx context.Context, s *series.Series, ep EpisodeLookup, now time.Time, log *slog.Logger) bool {
	if log == nil {
		log = slog.Default()
	}
	if s == nil {
		// Defensive — caller should never pass nil but err on the
		// side of "yes, refresh" so a corruption upstream surfaces
		// loudly rather than silently being skipped.
		return true
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// :27-30 — LastInfoSync more than 30 days old (or never synced).
	if s.LastInfoSync == nil || s.LastInfoSync.Before(now.AddDate(0, 0, -30)) {
		log.LogAttrs(ctx, slog.LevelDebug,
			"refreshseries: ShouldRefresh — LastInfoSync >30d ago (or never)",
			slog.Int("seriesId", s.ID),
			slog.String("title", s.Title))
		return true
	}

	// :33-43 — aired episode with TBA title.
	if ep != nil {
		eps, err := ep.AllBySeriesID(ctx, s.ID)
		if err != nil {
			// Lookup failure — C# `:73-76` returns true on any
			// exception. Mirror that posture.
			log.LogAttrs(ctx, slog.LevelWarn,
				"refreshseries: ShouldRefresh — episode lookup failed, defaulting refresh=true",
				slog.Int("seriesId", s.ID),
				slog.Any("err", err))
			return true
		}
		if hasAiredTBAEpisode(eps, now) {
			log.LogAttrs(ctx, slog.LevelDebug,
				"refreshseries: ShouldRefresh — aired episode with TBA title",
				slog.Int("seriesId", s.ID),
				slog.String("title", s.Title))
			return true
		}

		// :45-49 — LastInfoSync within last 6 hours → skip. We
		// evaluate this AFTER the TBA check because TBA-fillins are
		// the one signal that overrides the recency throttle.
		if !s.LastInfoSync.Before(now.Add(-6 * time.Hour)) {
			log.LogAttrs(ctx, slog.LevelDebug,
				"refreshseries: ShouldRefresh — LastInfoSync <6h ago, skip",
				slog.Int("seriesId", s.ID),
				slog.String("title", s.Title))
			return false
		}

		// :51-54 — active (not-ended) series always refresh.
		if s.Status != series.SeriesStatusEnded {
			log.LogAttrs(ctx, slog.LevelDebug,
				"refreshseries: ShouldRefresh — not-ended status, refresh",
				slog.Int("seriesId", s.ID),
				slog.String("title", s.Title),
				slog.Int("status", s.Status))
			return true
		}

		// :56-62 — ended series with last episode in trailing 30 days.
		if last, ok := maxAirDateUtc(eps); ok && last.After(now.AddDate(0, 0, -30)) {
			log.LogAttrs(ctx, slog.LevelDebug,
				"refreshseries: ShouldRefresh — ended series with recent last episode",
				slog.Int("seriesId", s.ID),
				slog.String("title", s.Title))
			return true
		}

		log.LogAttrs(ctx, slog.LevelDebug,
			"refreshseries: ShouldRefresh — ended series, no recent activity, skip",
			slog.Int("seriesId", s.ID),
			slog.String("title", s.Title))
		return false
	}

	// nil ep — same fallback as C# `catch (Exception)`: default to
	// refresh-true so a missing dependency doesn't silently skip
	// rows. The handler binding in main.go always wires the lookup;
	// only test paths take this branch.
	return true
}

// hasAiredTBAEpisode walks the episode list looking for the first
// (SeasonNumber > 0, AirDateUtc < now, Title == "TBA") match. C#
// `:35-39` uses `episodes.Any(...)` which short-circuits the same
// way Go's range+return loop does.
func hasAiredTBAEpisode(eps []episodes.Episode, now time.Time) bool {
	for i := range eps {
		e := &eps[i]
		if e.SeasonNumber <= 0 {
			continue
		}
		if e.AirDateUtc == nil || !e.AirDateUtc.Before(now) {
			continue
		}
		if e.Title == "TBA" {
			return true
		}
	}
	return false
}

// maxAirDateUtc returns the latest AirDateUtc across the episode
// list, ignoring rows with nil AirDateUtc. The second return is
// false when no episode has an AirDateUtc set — caller treats that
// as "no recent activity".
func maxAirDateUtc(eps []episodes.Episode) (time.Time, bool) {
	var max time.Time
	found := false
	for i := range eps {
		air := eps[i].AirDateUtc
		if air == nil {
			continue
		}
		if !found || air.After(max) {
			max = *air
			found = true
		}
	}
	return max, found
}
